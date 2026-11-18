package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	word "github.com/mmonterroca/docxgo/v2"
	"github.com/mobinova/udf"
)

func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 20, G: 40, B: 60, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal("encode PNG fixture")
	}
	return b.Bytes()
}

func TestExporterWritesReadableDOCX(t *testing.T) {
	imageData := makePNG(t)
	resource := udf.Resource{ID: "img1", Data: imageData, MediaType: "image/png", Width: 2, Height: 2}
	doc := &udf.Document{
		Page:      udf.PageLayout{MediaSizeName: "1", Width: 595.28, Height: 841.89, LeftMargin: 36, RightMargin: 36, TopMargin: 36, BottomMargin: 36, Orientation: udf.Portrait},
		Resources: []udf.Resource{resource},
		Body: []udf.Block{
			{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{StyleName: "hvl-default", Properties: udf.ParagraphStyle{Alignment: udf.AlignCenter, LineSpacing: 0.3, Tabs: []udf.TabStop{{Position: 72, Alignment: "right", Leader: "dot"}}}, Inlines: []udf.Inline{
				{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Notice", Style: udf.TextStyle{Bold: true, FontFamily: "Arial", FontSize: 14}}},
				{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}},
				{Kind: udf.InlineRun, Run: &udf.TextRun{Text: " text"}},
			}}},
			{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineImage, Image: &udf.Image{ResourceID: resource.ID, Width: 72, Height: 72}}}}},
			{Kind: udf.BlockTable, Table: &udf.Table{ColumnCount: 2, Rows: []udf.TableRow{{Type: "headerRow", Cells: []udf.TableCell{
				{GridRow: 0, GridCol: 0, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Name"}}}}}}},
				{GridRow: 0, GridCol: 1, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Value"}}}}}}},
			}}}}},
			{Kind: udf.BlockPageBreak, PageBreak: &udf.PageBreak{}},
		},
	}
	var output bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &output)
	if err != nil {
		t.Fatalf("export DOCX: %v", err)
	}
	if !containsDiagnostic(report.Diagnostics, "style.font_default_inferred") || !containsDiagnostic(report.Diagnostics, "style.font_size_default_inferred") {
		t.Fatal("inferred missing font defaults were not reported")
	}
	parsed, err := word.OpenDocumentFromBytes(output.Bytes())
	if err != nil {
		t.Fatalf("open generated DOCX: %v", err)
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("generated DOCX validation failed: %v", err)
	}
	blocks := parsed.Blocks()
	if len(blocks) != 4 {
		t.Fatal("DOCX top-level block count changed")
	}
	if got := blocks[0].Paragraph.Runs()[0]; !got.Bold() || got.Text() != "Notice" {
		t.Fatal("run formatting did not round-trip through docxgo")
	}
	if len(blocks[1].Paragraph.Images()) != 1 {
		t.Fatal("inline image was not embedded")
	}
	if blocks[2].Table == nil || blocks[2].Table.RowCount() != 1 || blocks[2].Table.ColumnCount() != 2 {
		t.Fatal("table structure was not written")
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatalf("read generated package: %v", err)
	}
	var documentXML string
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal("open document XML")
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal("read document XML")
		}
		documentXML = string(data)
	}
	if !strings.Contains(documentXML, `<w:tab w:val="right" w:pos="1440" w:leader="dot"/>`) {
		t.Fatal("tab stop was not written in OOXML")
	}
	if !strings.Contains(documentXML, `w:line="312"`) {
		t.Fatal("UDF 0.3 line spacing was not converted to 1.3 line spacing")
	}
	if !strings.Contains(documentXML, "&#x9;") {
		t.Fatal("inline tab character was not preserved in its text run")
	}
	if strings.Index(documentXML, "<w:tabs>") > strings.Index(documentXML, "<w:spacing") {
		t.Fatal("tab stops were not placed in WordprocessingML property order")
	}
	if !strings.Contains(documentXML, `<w:docGrid w:linePitch="360"/>`) {
		t.Fatal("UYAP document text grid was not emitted")
	}
	if !strings.Contains(documentXML, `w:w="10466"`) {
		t.Fatal("auto-width table did not expand to the available page width")
	}
	if strings.Contains(documentXML, "<w:pStyle") {
		t.Fatal("UDF default resolver was incorrectly mapped to a Word paragraph style")
	}
	if strings.Count(documentXML, `w:ascii="Arial"`) < 2 {
		t.Fatal("runs without explicit font did not inherit the inferred document font")
	}
}

func TestLocalCorpusProducesOpenableDOCXWithoutChangingSources(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", ".local-fixtures", "*.udf"))
	if err != nil {
		t.Fatal("find private local UDF corpus")
	}
	if len(files) == 0 {
		t.Skip("private local UDF corpus is not present")
	}
	exporter := Exporter{Options: Options{AllowLossy: true}}
	for i, path := range files {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read local sample %d", i)
		}
		doc, err := udf.ParseBytes(original)
		if err != nil {
			t.Fatalf("parse local sample %d: %v", i, err)
		}
		var output bytes.Buffer
		if _, err := exporter.Export(context.Background(), doc, &output); err != nil {
			t.Fatalf("export local sample %d: %v", i, err)
		}
		generated, err := word.OpenDocumentFromBytes(output.Bytes())
		if err != nil {
			t.Fatalf("open generated DOCX sample %d", i)
		}
		if err := generated.Validate(); err != nil {
			t.Fatalf("validate generated DOCX sample %d", i)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatalf("local UDF sample %d changed during export", i)
		}
	}
}

func TestSignedUDFSignatureIsReportedNotTransferred(t *testing.T) {
	doc := &udf.Document{Signature: udf.SignatureInfo{Present: true}, Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "visible text"}}}}}}}
	var output bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &output)
	if err != nil {
		t.Fatalf("DOCX derivative should be allowed: %v", err)
	}
	if !containsDiagnostic(report.Diagnostics, "signature.not_transferred") {
		t.Fatal("source signature loss was not reported")
	}
}

func TestUnverifiedSwingIndentFieldsAreNotWritten(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Properties: udf.ParagraphStyle{Hanging: 182, FirstLineIndent: 35.4}}}}}
	var output bytes.Buffer
	report, err := ExportWithOptions(context.Background(), doc, &output, Options{AllowLossy: true})
	if err != nil {
		t.Fatalf("export document: %v", err)
	}
	if !containsDiagnostic(report.Diagnostics, "paragraph.indentation_omitted") {
		t.Fatal("unverified paragraph indent was not reported")
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatalf("read generated package: %v", err)
	}
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal("open document XML")
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal("read document XML")
		}
		if !strings.Contains(string(data), `<w:ind w:left="0" w:right="0" w:firstLine="0"/>`) || strings.Contains(string(data), `w:hanging="3640"`) {
			t.Fatal("safe zero paragraph indent was not written or unverified indent leaked into DOCX")
		}
	}
}

func containsDiagnostic(items []udf.Diagnostic, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}
