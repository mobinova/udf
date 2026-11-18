package docximporter

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	word "github.com/mmonterroca/docxgo/v2"
	"github.com/mmonterroca/docxgo/v2/domain"
	udf "github.com/mobinova/udf"
)

func TestImporterMapsDocumentStructureAndFormatting(t *testing.T) {
	imageData := tinyPNG(t)
	source := word.NewDocument()
	section, err := source.DefaultSection()
	if err != nil {
		t.Fatal("get document section")
	}
	if err := section.SetPageSize(domain.PageSize{Width: 11906, Height: 16838}); err != nil {
		t.Fatal("set page size")
	}
	if err := section.SetMargins(domain.Margins{Top: 720, Right: 720, Bottom: 720, Left: 720}); err != nil {
		t.Fatal("set margins")
	}
	paragraph, err := source.AddParagraph()
	if err != nil {
		t.Fatal("add paragraph")
	}
	if err := paragraph.SetAlignment(domain.AlignmentCenter); err != nil {
		t.Fatal("center heading")
	}
	run, err := paragraph.AddRun()
	if err != nil {
		t.Fatal("add heading run")
	}
	if err := run.SetText("Synthetic title"); err != nil {
		t.Fatal("set heading text")
	}
	if err := run.SetBold(true); err != nil {
		t.Fatal("bold heading")
	}

	paragraph, err = source.AddParagraph()
	if err != nil {
		t.Fatal("add image paragraph")
	}
	if _, err := paragraph.AddImageFromBytesWithSize(imageData, domain.ImageFormatPNG, domain.NewImageSizeInches(0.25, 0.25)); err != nil {
		t.Fatal("add image")
	}

	table, err := source.AddTable(1, 2)
	if err != nil {
		t.Fatal("add table")
	}
	for col, text := range []string{"Name", "Value"} {
		cell, err := table.Row(0)
		if err != nil {
			t.Fatal("get table row")
		}
		target, err := cell.Cell(col)
		if err != nil {
			t.Fatal("get table cell")
		}
		cellParagraph, err := target.AddParagraph()
		if err != nil {
			t.Fatal("add table-cell paragraph")
		}
		cellRun, err := cellParagraph.AddRun()
		if err != nil {
			t.Fatal("add table-cell run")
		}
		if err := cellRun.SetText(text); err != nil {
			t.Fatal("set table-cell text")
		}
	}

	var data bytes.Buffer
	if _, err := source.WriteTo(&data); err != nil {
		t.Fatalf("write DOCX fixture: %v", err)
	}
	imported, err := (Importer{}).Import(context.Background(), bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatalf("import DOCX: %v", err)
	}
	if err := udf.Validate(imported); err != nil {
		t.Fatalf("importer returned invalid model: %v", err)
	}
	if imported.FormatID != "docx" || len(imported.Body) != 3 || len(imported.Resources) != 1 {
		t.Fatalf("unexpected document structure: format=%s blocks=%d resources=%d", imported.FormatID, len(imported.Body), len(imported.Resources))
	}
	if imported.Page.Width <= 0 || imported.Page.Height <= 0 || imported.Page.LeftMargin != 36 {
		t.Fatal("DOCX page layout was not normalized from twips to points")
	}
	if imported.Body[0].Paragraph == nil {
		t.Fatal("paragraph was not imported")
	}
	gotBold := len(imported.Body[0].Paragraph.Inlines) > 0 && imported.Body[0].Paragraph.Inlines[0].Run != nil && imported.Body[0].Paragraph.Inlines[0].Run.Style.Bold
	if imported.Body[0].Paragraph.Properties.Alignment != udf.AlignCenter || !gotBold {
		t.Fatal("paragraph alignment or run formatting was not imported")
	}
	if imported.Body[1].Paragraph == nil || imported.Body[1].Paragraph.Inlines[0].Kind != udf.InlineImage {
		t.Fatal("embedded DOCX image was not represented as an inline resource")
	}
	if imported.Body[2].Table == nil || imported.Body[2].Table.ColumnCount != 2 || len(imported.Body[2].Table.Rows) != 1 {
		t.Fatal("DOCX table structure was not imported")
	}
}

func TestImporterBoundsAndRejectsInvalidPackages(t *testing.T) {
	data := tinyDOCX(t)
	if _, err := (Importer{Options: Options{MaxInputBytes: 1}}).Import(context.Background(), bytes.NewReader(data)); err != udf.ErrLimitExceeded {
		t.Fatalf("expected compressed input limit, got %v", err)
	}
	if _, err := (Importer{}).Import(context.Background(), bytes.NewReader([]byte("not a DOCX"))); err == nil {
		t.Fatal("invalid DOCX package was accepted")
	}
}

func TestImporterHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Importer{}).Import(ctx, bytes.NewReader(tinyDOCX(t))); err == nil {
		t.Fatal("cancelled import unexpectedly succeeded")
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 20, G: 40, B: 60, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal("encode PNG fixture")
	}
	return data.Bytes()
}

func tinyDOCX(t *testing.T) []byte {
	t.Helper()
	doc := word.NewDocument()
	paragraph, err := doc.AddParagraph()
	if err != nil {
		t.Fatal("add paragraph")
	}
	run, err := paragraph.AddRun()
	if err != nil {
		t.Fatal("add run")
	}
	if err := run.SetText("fixture"); err != nil {
		t.Fatal("set text")
	}
	var data bytes.Buffer
	if _, err := doc.WriteTo(&data); err != nil {
		t.Fatal("write DOCX fixture")
	}
	return data.Bytes()
}

func TestImporterEnforcesArchiveEntryLimit(t *testing.T) {
	data := tinyDOCX(t)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal("read DOCX package directory")
	}
	if len(reader.File) == 0 {
		t.Fatal("generated DOCX has no package entries")
	}
	if _, err := (Importer{Options: Options{MaxArchiveEntries: 1}}).Import(context.Background(), bytes.NewReader(data)); err != udf.ErrLimitExceeded {
		t.Fatalf("expected archive entry limit, got %v", err)
	}
}
