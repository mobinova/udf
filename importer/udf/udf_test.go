package udfimporter

import (
	"archive/zip"
	"bytes"
	"context"
	"reflect"
	"testing"

	udf "github.com/mobinova/udf"
)

func TestImporterMatchesExistingUDFParser(t *testing.T) {
	data := syntheticUDF(t)
	parsed, err := udf.ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	imported, err := (Importer{}).Import(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Importer.Import: %v", err)
	}
	if !reflect.DeepEqual(imported, parsed) {
		t.Fatal("UDF importer changed the existing parser result")
	}
	if err := udf.Validate(imported); err != nil {
		t.Fatalf("importer returned an invalid model: %v", err)
	}
}

func TestImporterHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Importer{}).Import(ctx, bytes.NewReader(syntheticUDF(t))); err == nil {
		t.Fatal("cancelled import unexpectedly succeeded")
	}
}

func syntheticUDF(t *testing.T) []byte {
	t.Helper()
	const xmlData = `<template format_id="1.8"><content><![CDATA[agent test
]]></content><elements><paragraph><content startOffset="0" length="11"/></paragraph></elements></template>`
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entry, err := writer.Create("content.xml")
	if err != nil {
		t.Fatal("create synthetic content entry")
	}
	if _, err := entry.Write([]byte(xmlData)); err != nil {
		t.Fatal("write synthetic content entry")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("finish synthetic UDF")
	}
	return data.Bytes()
}
