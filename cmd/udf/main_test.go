package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	udf "github.com/mobinova/udf"
)

func makeSyntheticUDF(t *testing.T, signed bool) string {
	t.Helper()
	const visibleText = "synthetic-secret-text\n"
	content := `<template format_id="1.8"><content><![CDATA[` + visibleText + `]]></content><elements><paragraph><content startOffset="0" length="22"/></paragraph></elements></template>`
	path := filepath.Join(t.TempDir(), "synthetic.udf")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal("create synthetic UDF")
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("content.xml")
	if err != nil {
		t.Fatal("create content entry")
	}
	if _, err := io.WriteString(entry, content); err != nil {
		t.Fatal("write content entry")
	}
	if signed {
		entry, err = writer.Create("sign.sgn")
		if err != nil {
			t.Fatal("create synthetic signature entry")
		}
		if _, err := io.WriteString(entry, "synthetic-signature"); err != nil {
			t.Fatal("write synthetic signature entry")
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal("finish synthetic UDF")
	}
	if err := file.Close(); err != nil {
		t.Fatal("close synthetic UDF")
	}
	return path
}

func TestInspectJSONOmitsDocumentTextAndTextCommandEmitsIt(t *testing.T) {
	input := makeSyntheticUDF(t, false)
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"inspect", "--json", input}, &stdout, &stderr); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !strings.Contains(stdout.String(), `"signature_present": false`) || strings.Contains(stdout.String(), "synthetic-secret-text") {
		t.Fatal("JSON inspection must include structural metadata and exclude document text")
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"text", input}, &stdout, &stderr); err != nil {
		t.Fatalf("text export: %v", err)
	}
	if !strings.Contains(stdout.String(), "synthetic-secret-text") {
		t.Fatal("text command did not emit the requested body projection")
	}
}

func TestExportProtectsSourceAndRequiresUnsignedOptIn(t *testing.T) {
	input := makeSyntheticUDF(t, true)
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal("read source fixture")
	}
	var stdout, stderr bytes.Buffer
	err = run(context.Background(), []string{"export", "--format", "udf", "--output", input, input}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatal("export command allowed overwriting its source")
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("source UDF changed during rejected export")
	}
	if err := run(context.Background(), []string{"export", "--format", "udf", "--output", filepath.Join(t.TempDir(), "safe.udf"), "--report", input, input}, &stdout, &stderr); err == nil {
		t.Fatal("export command allowed a report to overwrite its source")
	}
	shared := filepath.Join(t.TempDir(), "shared-output")
	if err := run(context.Background(), []string{"export", "--format", "udf", "--output", shared, "--report", shared, input}, &stdout, &stderr); err == nil {
		t.Fatal("export command allowed output and report to share a path")
	}

	output := filepath.Join(t.TempDir(), "unsigned.udf")
	err = run(context.Background(), []string{"export", "--format", "udf", "--output", output, input}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("signed input exported without explicit unsigned opt-in")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed export left an output file")
	}

	reportPath := filepath.Join(t.TempDir(), "report.json")
	err = run(context.Background(), []string{"export", "--format", "udf", "--allow-unsigned", "--allow-lossy", "--output", output, "--report", reportPath, input}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("explicit unsigned export: %v", err)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal("read JSON export report")
	}
	var report exportReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatal("decode JSON export report")
	}
	if len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != "signature.not_transferred" {
		t.Fatal("JSON report did not identify the unsigned derivative")
	}
	derivative, err := udf.ParseFile(output)
	if err != nil {
		t.Fatalf("parse exported derivative: %v", err)
	}
	if derivative.Signature.Present {
		t.Fatal("unsigned UDF derivative copied the source signature")
	}
}

func TestExportDoesNotLeavePartialOutputForStrictLoss(t *testing.T) {
	const content = `<template format_id="1.8"><content><![CDATA[x
]]></content><elements><unknown/></elements></template>`
	input := filepath.Join(t.TempDir(), "unknown.udf")
	file, err := os.Create(input)
	if err != nil {
		t.Fatal("create synthetic UDF")
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("content.xml")
	if err != nil {
		t.Fatal("create content entry")
	}
	if _, err := io.WriteString(entry, content); err != nil {
		t.Fatal("write content entry")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("finish synthetic UDF")
	}
	if err := file.Close(); err != nil {
		t.Fatal("close synthetic UDF")
	}

	output := filepath.Join(t.TempDir(), "strict.md")
	err = run(context.Background(), []string{"export", "--format", "markdown", "--output", output, input}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("strict export unexpectedly accepted unprojectable content")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed strict export left a partial output file")
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("version command: %v", err)
	}
	if !strings.Contains(stdout.String(), "udf dev") || stderr.Len() != 0 {
		t.Fatal("version command did not emit a clean version line")
	}
}
