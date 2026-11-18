// Command udf inspects and exports local UYAP UDF documents.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	udf "github.com/mobinova/udf"
	docxexport "github.com/mobinova/udf/export/docx"
	markdownexport "github.com/mobinova/udf/export/markdown"
	textexport "github.com/mobinova/udf/export/text"
	udfexport "github.com/mobinova/udf/export/udf"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type diagnosticSummary struct {
	Code     string                 `json:"code"`
	Severity udf.DiagnosticSeverity `json:"severity"`
	Class    udf.DiagnosticClass    `json:"class"`
	Location string                 `json:"location,omitempty"`
}

type inspection struct {
	FormatID         string              `json:"format_id,omitempty"`
	BodyBlocks       int                 `json:"body_blocks"`
	Paragraphs       int                 `json:"paragraphs"`
	Tables           int                 `json:"tables"`
	ImageResources   int                 `json:"image_resources"`
	InlineImages     int                 `json:"inline_images"`
	Headers          int                 `json:"headers"`
	Footers          int                 `json:"footers"`
	SignaturePresent bool                `json:"signature_present"`
	Diagnostics      []diagnosticSummary `json:"diagnostics"`
}

type exportReport struct {
	Diagnostics []diagnosticSummary `json:"diagnostics"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "udf:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "version":
		_, err := fmt.Fprintf(stdout, "udf %s (commit %s, built %s)\n", version, commit, date)
		return err
	case "inspect":
		return runInspect(ctx, args[1:], stdout, stderr)
	case "text":
		return runText(ctx, args[1:], stdout, stderr)
	case "export":
		return runExport(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInspect(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write a machine-readable structural summary without document text")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: udf inspect [--json] FILE.udf")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	doc, err := udf.ParseFile(flags.Arg(0))
	if err != nil {
		return err
	}
	summary := summarize(doc)
	if *jsonOutput {
		return writeJSON(stdout, summary)
	}
	_, err = fmt.Fprintf(stdout,
		"Format: %s\nBody blocks: %d\nParagraphs: %d\nTables: %d\nImage resources: %d\nInline images: %d\nHeaders: %d\nFooters: %d\nSignature present: %t\nParse diagnostics: %d\n",
		emptyAsUnknown(summary.FormatID), summary.BodyBlocks, summary.Paragraphs,
		summary.Tables, summary.ImageResources, summary.InlineImages, summary.Headers, summary.Footers,
		summary.SignaturePresent, len(summary.Diagnostics))
	return err
}

func runText(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: udf text FILE.udf")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	doc, err := udf.ParseFile(args[0])
	if err != nil {
		return err
	}
	report, err := textexport.Export(ctx, doc, stdout)
	writeDiagnostics(stderr, report.Diagnostics)
	return err
}

func runExport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "", "output format: text, markdown, docx, or udf")
	output := flags.String("output", "", "destination path; use - for stdout")
	reportPath := flags.String("report", "", "write diagnostics as JSON to this path; use - for stdout")
	allowLossy := flags.Bool("allow-lossy", false, "allow and report unsupported-content fallbacks")
	allowUnsigned := flags.Bool("allow-unsigned", false, "allow writing an unsigned UDF derivative from signed input")
	includeHeadersFooters := flags.Bool("include-headers-footers", false, "include headers and footers in Markdown")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 || *format == "" || *output == "" {
		return errors.New("usage: udf export --format FORMAT --output PATH [OPTIONS] FILE.udf")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	input := flags.Arg(0)
	if *output != "-" && sameFile(input, *output) {
		return errors.New("refusing to overwrite the source document")
	}
	if *reportPath != "" && *reportPath != "-" {
		if sameFile(input, *reportPath) {
			return errors.New("refusing to overwrite the source document with a report")
		}
		if *output != "-" && sameFile(*output, *reportPath) {
			return errors.New("output and report paths must be different")
		}
	}
	doc, err := udf.ParseFile(input)
	if err != nil {
		return err
	}
	if *output == "-" && *reportPath == "-" {
		return errors.New("document output and JSON report cannot both use stdout")
	}

	export := func(dst io.Writer) (udf.ExportReport, error) {
		switch strings.ToLower(*format) {
		case "text", "txt":
			return textexport.Export(ctx, doc, dst)
		case "markdown", "md":
			return markdownexport.ExportWithOptions(ctx, doc, dst, markdownexport.Options{
				AllowLossy:            *allowLossy,
				IncludeHeadersFooters: *includeHeadersFooters,
			})
		case "docx", "word":
			return docxexport.ExportWithOptions(ctx, doc, dst, docxexport.Options{AllowLossy: *allowLossy})
		case "udf":
			return udfexport.ExportWithOptions(ctx, doc, dst, udfexport.Options{
				AllowLossy:    *allowLossy,
				AllowUnsigned: *allowUnsigned,
			})
		default:
			return udf.ExportReport{}, fmt.Errorf("unsupported format %q", *format)
		}
	}

	var report udf.ExportReport
	var exportErr error
	if *output == "-" {
		report, exportErr = export(stdout)
	} else {
		report, exportErr = exportFile(*output, export)
	}
	reportErr := writeReport(*reportPath, report, stdout, stderr)
	if exportErr != nil {
		return exportErr
	}
	return reportErr
}

func summarize(doc *udf.Document) inspection {
	result := inspection{
		FormatID:         doc.FormatID,
		BodyBlocks:       len(doc.Body),
		ImageResources:   len(doc.Resources),
		Headers:          len(doc.Headers),
		Footers:          len(doc.Footers),
		SignaturePresent: doc.Signature.Present,
		Diagnostics:      summarizeDiagnostics(doc.Diagnostics),
	}
	var count func([]udf.Block)
	count = func(blocks []udf.Block) {
		for _, block := range blocks {
			switch block.Kind {
			case udf.BlockParagraph:
				result.Paragraphs++
				if block.Paragraph != nil {
					for _, inline := range block.Paragraph.Inlines {
						if inline.Kind == udf.InlineImage {
							result.InlineImages++
						}
					}
				}
			case udf.BlockTable:
				result.Tables++
				if block.Table != nil {
					for _, row := range block.Table.Rows {
						for _, cell := range row.Cells {
							count(cell.Blocks)
						}
					}
				}
			}
		}
	}
	count(doc.Body)
	return result
}

func summarizeDiagnostics(diagnostics []udf.Diagnostic) []diagnosticSummary {
	out := make([]diagnosticSummary, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		out = append(out, diagnosticSummary{
			Code: diagnostic.Code, Severity: diagnostic.Severity,
			Class: diagnostic.Class, Location: diagnostic.Location,
		})
	}
	return out
}

func writeReport(path string, report udf.ExportReport, stdout, stderr io.Writer) error {
	summary := exportReport{Diagnostics: summarizeDiagnostics(report.Diagnostics)}
	if path == "" {
		writeDiagnostics(stderr, report.Diagnostics)
		return nil
	}
	if path == "-" {
		return writeJSON(stdout, summary)
	}
	return writeJSONFile(path, summary)
}

func writeJSON(dst io.Writer, value any) error {
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONFile(path string, value any) error {
	_, err := exportFile(path, func(dst io.Writer) (udf.ExportReport, error) {
		return udf.ExportReport{}, writeJSON(dst, value)
	})
	return err
}

func exportFile(path string, export func(io.Writer) (udf.ExportReport, error)) (udf.ExportReport, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return udf.ExportReport{}, err
	}
	tmp, err := os.CreateTemp(dir, ".udf-export-*.tmp")
	if err != nil {
		return udf.ExportReport{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	report, exportErr := export(tmp)
	closeErr := tmp.Close()
	if exportErr != nil {
		return report, exportErr
	}
	if closeErr != nil {
		return report, closeErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		return report, err
	}
	return report, nil
}

func writeDiagnostics(dst io.Writer, diagnostics []udf.Diagnostic) {
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(dst, "%s\t%s\t%s\n", diagnostic.Code, diagnostic.Class, diagnostic.Location)
	}
}

func sameFile(source, destination string) bool {
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return false
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return false
	}
	if filepath.Clean(sourcePath) == filepath.Clean(destinationPath) {
		return true
	}
	sourceInfo, sourceErr := os.Stat(sourcePath)
	destinationInfo, destinationErr := os.Stat(destinationPath)
	return sourceErr == nil && destinationErr == nil && os.SameFile(sourceInfo, destinationInfo)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func emptyAsUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func printUsage(dst io.Writer) {
	fmt.Fprintln(dst, `udf - inspect and export local UYAP UDF documents

Usage:
  udf version
  udf inspect [--json] FILE.udf
  udf text FILE.udf
  udf export --format FORMAT --output PATH [OPTIONS] FILE.udf

Formats: text, markdown, docx, udf
Export options: --report PATH, --allow-lossy, --allow-unsigned,
                --include-headers-footers (Markdown only)
Use --output - to write document bytes to stdout. Inspection never prints body text.`)
}
