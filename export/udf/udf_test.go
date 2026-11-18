package udf

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	model "github.com/mobinova/udf"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal("encode image fixture")
	}
	return data.Bytes()
}

func TestSignedSourceRequiresExplicitUnsignedExport(t *testing.T) {
	doc := &model.Document{Signature: model.SignatureInfo{Present: true}}
	var output bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &output)
	if err != model.ErrUnsupported || output.Len() != 0 {
		t.Fatal("signed source was exported without unsigned opt-in")
	}
	if len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != "signature.not_transferred" {
		t.Fatal("signature loss was not reported")
	}
}

func TestExportUDFSemanticRoundTrip(t *testing.T) {
	imgBytes := tinyPNG(t)
	resource := model.Resource{ID: "logo", Data: imgBytes, MediaType: "image/png", Width: 1, Height: 1}
	doc := &model.Document{
		FormatID:   "1.8",
		WebID:      "fixture-web-id",
		Properties: model.DocumentProperties{Values: map[string]string{"uyapsicil": "fixture-serial", verificationCodeKey: "stale-code"}},
		Signature:  model.SignatureInfo{Present: true, Size: 12},
		Page:       model.PageLayout{MediaSizeName: "1", Orientation: model.Portrait, Width: 595.28, Height: 841.89, LeftMargin: 36, RightMargin: 36, TopMargin: 36, BottomMargin: 36},
		Resources:  []model.Resource{resource},
		Body: []model.Block{
			{Kind: model.BlockParagraph, Paragraph: &model.Paragraph{Properties: model.ParagraphStyle{Alignment: model.AlignCenter}, Inlines: []model.Inline{
				{Kind: model.InlineRun, Run: &model.TextRun{Text: "İHBAR EDEN: ", Style: model.TextStyle{Bold: true}}},
				{Kind: model.InlineField, Field: &model.Field{Name: "cached", Type: "value", Text: "Av. Ali"}},
				{Kind: model.InlineTab, Tab: &model.Tab{Text: "\t"}},
				{Kind: model.InlineRun, Run: &model.TextRun{Text: "Başvuru"}},
			}}},
			{Kind: model.BlockTable, Table: &model.Table{ColumnCount: 2, ColumnWidths: []model.ColumnWidth{{Value: 100, Unit: model.UnitPoint}, {Value: 120, Unit: model.UnitPoint}}, Rows: []model.TableRow{{Type: "headerRow", Cells: []model.TableCell{
				{GridRow: 0, GridCol: 0, RowSpan: 1, ColSpan: 1, Blocks: []model.Block{{Kind: model.BlockParagraph, Paragraph: &model.Paragraph{Inlines: []model.Inline{{Kind: model.InlineRun, Run: &model.TextRun{Text: "A"}}}}}}},
				{GridRow: 0, GridCol: 1, RowSpan: 1, ColSpan: 1, Blocks: []model.Block{{Kind: model.BlockParagraph, Paragraph: &model.Paragraph{Inlines: []model.Inline{{Kind: model.InlineRun, Run: &model.TextRun{Text: "B"}}}}}}},
			}}}}},
			{Kind: model.BlockParagraph, Paragraph: &model.Paragraph{Inlines: []model.Inline{{Kind: model.InlineImage, Image: &model.Image{ResourceID: resource.ID, Width: 24, Height: 24}}}}},
		},
	}
	if err := model.Validate(doc); err != nil {
		t.Fatalf("fixture model invalid: %v", err)
	}
	wantText := doc.Text()
	var output bytes.Buffer
	report, err := (Exporter{Options: Options{AllowUnsigned: true, AllowLossy: true}}).Export(context.Background(), doc, &output)
	if err != nil {
		t.Fatalf("export unsigned UDF fixture: %v", err)
	}
	if !hasDiagnostic(report.Diagnostics, "signature.not_transferred") || !hasDiagnostic(report.Diagnostics, "metadata.verification_code_omitted") {
		t.Fatal("signature and verification-code loss were not reported")
	}

	zr, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal("export was not a ZIP archive")
	}
	for _, entry := range zr.File {
		if entry.Name == "sign.sgn" {
			t.Fatal("unsigned exporter unexpectedly emitted a signature")
		}
	}

	parsed, err := model.ParseBytes(output.Bytes())
	if err != nil {
		t.Fatalf("parse exported UDF: %v", err)
	}
	if parsed.Signature.Present {
		t.Fatal("exported derivative unexpectedly has a signature")
	}
	if parsed.Text() != wantText {
		t.Fatal("exported UDF changed the model text projection")
	}
	if parsed.Properties.Values["uyapsicil"] != "fixture-serial" {
		t.Fatal("document properties did not round-trip")
	}
	if _, exists := parsed.Properties.Values[verificationCodeKey]; exists {
		t.Fatal("stale source verification code was copied")
	}
	if parsed.WebID != doc.WebID {
		t.Fatal("document web identifier did not round-trip")
	}
	if len(parsed.Resources) != 1 || len(parsed.Body) != len(doc.Body) {
		t.Fatal("table or image resources did not round-trip")
	}
}

func hasDiagnostic(items []model.Diagnostic, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestLocalCorpusUnsignedSemanticRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", ".local-fixtures", "*.udf"))
	if err != nil {
		t.Fatal("find private local corpus")
	}
	if len(files) == 0 {
		t.Skip("private local UDF corpus is not present")
	}
	options := Options{AllowUnsigned: true, AllowLossy: true}
	for i, path := range files {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read local sample %d", i)
		}
		doc, err := model.ParseBytes(original)
		if err != nil {
			t.Fatalf("parse local sample %d: %v", i, err)
		}
		var out bytes.Buffer
		_, err = (Exporter{Options: options}).Export(context.Background(), doc, &out)
		if err != nil {
			t.Fatalf("export local sample %d: %v", i, err)
		}
		roundTrip, err := model.ParseBytes(out.Bytes())
		if err != nil {
			t.Fatalf("parse exported local sample %d: %v", i, err)
		}
		if doc.Text() != roundTrip.Text() {
			t.Fatalf("local sample %d changed its text projection", i)
		}
		if doc.FormatID != roundTrip.FormatID || doc.WebID != roundTrip.WebID || len(doc.Headers) != len(roundTrip.Headers) || len(doc.Footers) != len(roundTrip.Footers) || len(doc.Styles) != len(roundTrip.Styles) || len(doc.Resources) != len(roundTrip.Resources) {
			t.Fatalf("local sample %d changed normalized document metadata or resource counts", i)
		}
		if countBlocks(doc.Body, model.BlockTable) != countBlocks(roundTrip.Body, model.BlockTable) || countBlocks(doc.Body, model.BlockParagraph) != countBlocks(roundTrip.Body, model.BlockParagraph) {
			t.Fatalf("local sample %d changed normalized body structure counts", i)
		}
		if roundTrip.Signature.Present {
			t.Fatalf("local sample %d emitted a source signature", i)
		}
		if _, exists := roundTrip.Properties.Values[verificationCodeKey]; exists {
			t.Fatalf("local sample %d copied a stale verification code", i)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatalf("local source sample %d changed during round-trip", i)
		}
	}
}

func countBlocks(blocks []model.Block, kind model.BlockKind) int {
	count := 0
	for _, block := range blocks {
		if block.Kind == kind {
			count++
		}
		if block.Table != nil {
			for _, row := range block.Table.Rows {
				for _, cell := range row.Cells {
					count += countBlocks(cell.Blocks, kind)
				}
			}
		}
	}
	return count
}
