// Command convert demonstrates parsing a local UDF and exporting Markdown and
// DOCX files. Both exporters are explicitly allowed to use their reported
// lossy fallbacks; inspect the printed diagnostics for each output.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	udf "github.com/mobinova/udf"
	"github.com/mobinova/udf/export/docx"
	"github.com/mobinova/udf/export/markdown"
)

func main() {
	var input, outDir string
	flag.StringVar(&input, "input", "", "path to a local .udf file")
	flag.StringVar(&outDir, "outdir", "out", "directory for exported files")
	flag.Parse()
	if input == "" {
		flag.Usage()
		os.Exit(2)
	}

	doc, err := udf.ParseFile(input)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatal(err)
	}
	base := filepath.Base(input)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	if err := writeExport(filepath.Join(outDir, base+".md"), func(dst io.Writer) (udf.ExportReport, error) {
		return markdown.ExportWithOptions(context.Background(), doc, dst, markdown.Options{AllowLossy: true})
	}); err != nil {
		log.Fatal(err)
	}
	if err := writeExport(filepath.Join(outDir, base+".docx"), func(dst io.Writer) (udf.ExportReport, error) {
		return docx.ExportWithOptions(context.Background(), doc, dst, docx.Options{AllowLossy: true})
	}); err != nil {
		log.Fatal(err)
	}
}

func writeExport(path string, export func(io.Writer) (udf.ExportReport, error)) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".udf-export-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	report, exportErr := export(file)
	closeErr := file.Close()
	if exportErr != nil {
		return exportErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintf(os.Stderr, "%s: %s (%s)\n", diagnostic.Code, diagnostic.Message, diagnostic.Location)
	}
	fmt.Fprintln(os.Stderr, "wrote", path)
	return nil
}
