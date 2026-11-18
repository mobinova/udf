package udf

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, data := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal("create fixture archive")
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal("write fixture archive")
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal("finish fixture archive")
	}
	return out.Bytes()
}

func TestParseUTF16SpansAndTextProjection(t *testing.T) {
	content := `<template format_id="1.8"><content><![CDATA[Hello😀 İ
Second
]]></content><properties><pageFormat mediaSizeName="1" paperOrientation="1" leftMargin="36" rightMargin="36" topMargin="36" bottomMargin="36"/></properties><elements><paragraph Alignment="0"><content startOffset="0" length="10" family="Arial" size="12"/></paragraph><paragraph><content startOffset="10" length="7" family="Arial" size="12"/></paragraph></elements><styles><style name="base" family="Arial" size="12"/></styles></template>`
	data := makeArchive(t, map[string][]byte{"content.xml": []byte(content)})
	doc, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse synthetic document: %v", err)
	}
	if doc.Text() != "Hello😀 İ\nSecond" {
		t.Fatal("unexpected body text projection")
	}
	if len(doc.Body) != 2 || doc.Page.Width <= 0 || len(doc.Styles) != 1 {
		t.Fatal("document structure was not parsed")
	}
	if err := Validate(doc); err != nil {
		t.Fatalf("parsed model validation failed: %v", err)
	}
}

func TestParseTablesAndImagesDeduplicate(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 20, G: 30, B: 40, A: 255})
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal("encode fixture image")
	}
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	pool := "A\nB\nC\n\uFFFC\n\uFFFC\n"
	content := `<template format_id="1.8"><content><![CDATA[` + pool + `]]></content><properties><pageFormat mediaSizeName="1"/></properties><elements><table columnCount="2" columnSpans="100,100"><row><cell><paragraph><content startOffset="0" length="2"/></paragraph></cell><cell><paragraph><content startOffset="2" length="2"/></paragraph></cell></row></table><paragraph><content startOffset="4" length="2"/></paragraph><paragraph><image imageData="` + encoded + `" startOffset="6" length="1" width="1" height="1"/></paragraph><paragraph><image imageData="` + encoded + `" startOffset="8" length="1" width="1" height="1"/></paragraph></elements></template>`
	doc, err := ParseBytes(makeArchive(t, map[string][]byte{"content.xml": []byte(content)}))
	if err != nil {
		t.Fatalf("parse table/image fixture: %v", err)
	}
	if doc.Text() != "A\tB\nC\n\n" {
		t.Fatal("unexpected table/image text projection")
	}
	if len(doc.Resources) != 1 {
		t.Fatal("identical images were not deduplicated")
	}
	if err := Validate(doc); err != nil {
		t.Fatalf("model validation failed: %v", err)
	}
}

func TestParseRejectsInvalidOffsetsAndArchivePaths(t *testing.T) {
	badSpan := `<template format_id="1.8"><content><![CDATA[x
]]></content><elements><paragraph><content startOffset="20" length="2"/></paragraph></elements></template>`
	if _, err := ParseBytes(makeArchive(t, map[string][]byte{"content.xml": []byte(badSpan)})); err == nil {
		t.Fatal("expected invalid offset to fail")
	}
	valid := `<template format_id="1.8"><content><![CDATA[x
]]></content><elements><paragraph><content startOffset="0" length="2"/></paragraph></elements></template>`
	if _, err := ParseBytes(makeArchive(t, map[string][]byte{"content.xml": []byte(valid), "../escape": []byte("x")})); err == nil {
		t.Fatal("expected unsafe archive name to fail")
	}
}

func TestParseRejectsCorruptZipEntry(t *testing.T) {
	content := `<template format_id="1.8"><content><![CDATA[x
]]></content><elements><paragraph><content startOffset="0" length="2"/></paragraph></elements></template>`
	data := makeArchive(t, map[string][]byte{"content.xml": []byte(content)})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal("read fixture archive directory")
	}
	offset, err := zr.File[0].DataOffset()
	if err != nil {
		t.Fatal("locate fixture entry data")
	}
	data[offset] ^= 0xff
	if _, err := ParseBytes(data); err == nil {
		t.Fatal("corrupt entry unexpectedly parsed")
	}
}

func TestLocalFixtureCorpusParses(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".local-fixtures", "*.udf"))
	if err != nil {
		t.Fatal("find local UDF fixtures")
	}
	if len(files) == 0 {
		t.Skip("private local UDF corpus is not present")
	}
	tables, images, withProperties := 0, 0, 0
	for i, name := range files {
		before, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read local sample %d", i)
		}
		doc, err := ParseBytes(before)
		if err != nil {
			t.Fatalf("parse local sample %d: %v", i, err)
		}
		if err := Validate(doc); err != nil {
			t.Fatalf("validate local sample %d: %v", i, err)
		}
		if doc.Text() == "" {
			t.Fatalf("local sample %d has empty body projection", i)
		}
		if len(doc.Properties.Values) > 0 {
			withProperties++
		}
		for _, block := range doc.Body {
			if block.Kind == BlockTable {
				tables++
			}
		}
		images += len(doc.Resources)
		after, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("local source sample %d changed during parse", i)
		}
	}
	if tables == 0 || images == 0 || withProperties == 0 {
		t.Fatal("local corpus did not exercise expected structures")
	}
}

func TestTextProjectionEmptyParagraphs(t *testing.T) {
	doc := &Document{Body: []Block{
		{Kind: BlockParagraph, Paragraph: &Paragraph{Inlines: []Inline{{Kind: InlineRun, Run: &TextRun{Text: "A"}}}}},
		{Kind: BlockParagraph, Paragraph: &Paragraph{}},
		{Kind: BlockParagraph, Paragraph: &Paragraph{Inlines: []Inline{{Kind: InlineRun, Run: &TextRun{Text: "B"}}}}},
	}}
	if doc.Text() != "A\n\nB" {
		t.Fatal("empty paragraph boundary was not preserved")
	}
	if strings.Contains(doc.Text(), "\r") {
		t.Fatal("unexpected carriage return in projection")
	}
}
