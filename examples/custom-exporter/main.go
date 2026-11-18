// Command custom-exporter demonstrates implementing udf.Exporter without
// importing parser internals or relying on a format registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	udf "github.com/mobinova/udf"
)

type JSONExporter struct{}

type jsonDocument struct {
	FormatID         string `json:"format_id,omitempty"`
	Text             string `json:"text"`
	SignaturePresent bool   `json:"source_signature_present"`
}

func (JSONExporter) Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	if err := udf.Validate(doc); err != nil {
		return udf.ExportReport{}, err
	}
	if dst == nil {
		return udf.ExportReport{}, fmt.Errorf("json export: nil writer")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return udf.ExportReport{}, ctx.Err()
		default:
		}
	}

	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(jsonDocument{
		FormatID:         doc.FormatID,
		Text:             doc.Text(),
		SignaturePresent: doc.Signature.Present,
	}); err != nil {
		return udf.ExportReport{}, err
	}
	return udf.ExportReport{Diagnostics: []udf.Diagnostic{{
		Code:     "json.body_text_projection",
		Severity: udf.SeverityWarning,
		Class:    udf.DiagnosticVisualApproximation,
		Location: "document",
		Message:  "This example emits body text and basic metadata; document structure and formatting are omitted",
	}}}, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./examples/custom-exporter <input.udf>")
		os.Exit(2)
	}
	doc, err := udf.ParseFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if _, err := (JSONExporter{}).Export(context.Background(), doc, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
