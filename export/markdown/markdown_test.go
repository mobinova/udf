package markdown

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mobinova/udf"
)

func TestExporterFormatsRunsAndTables(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{
		{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{
			{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Hello "}},
			{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "world", Style: udf.TextStyle{Bold: true}}},
		}}},
		{Kind: udf.BlockTable, Table: &udf.Table{ColumnCount: 2, Rows: []udf.TableRow{{Type: "headerRow", Cells: []udf.TableCell{
			{GridRow: 0, GridCol: 0, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "A|B"}}}}}}},
			{GridRow: 0, GridCol: 1, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Cell"}}}}}}},
		}}}}},
	}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("markdown export failed")
	}
	expected := "Hello **world**\n\n| A\\|B | Cell |\n| --- | --- |\n"
	if out.String() != expected {
		t.Fatal("markdown output did not match the format contract")
	}
	if len(report.Diagnostics) != 0 {
		t.Fatal("supported markdown features produced diagnostics")
	}
}

func TestExporterUsesPureMarkdownAndEscapesHTMLLikeSourceText(t *testing.T) {
	family := `Arial';background:url(javascript:alert(1))`
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Properties: udf.ParagraphStyle{Alignment: udf.AlignCenter, SpaceAbove: 4}, Inlines: []udf.Inline{
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: `<script>alert("x")</script>`, Style: udf.TextStyle{FontFamily: family, FontSize: 12, Bold: true, Underline: true, Foreground: udf.Opt[int32]{Val: -65536, Set: true}}}},
		{Kind: udf.InlineField, Field: &udf.Field{Name: "page", Type: "cached", Text: "7"}},
		{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}},
		{Kind: udf.InlineBreak, Break: &udf.LineBreak{Text: "\n"}},
	}}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("rich Markdown export failed")
	}
	result := out.String()
	if strings.Contains(result, "<script>") || strings.Contains(result, "<span") || strings.Contains(result, "<u>") {
		t.Fatal("export included raw HTML")
	}
	if !strings.Contains(result, `\<script\>`) || !strings.Contains(result, "**") {
		t.Fatal("source text or representable emphasis was not preserved")
	}
	if !strings.Contains(result, "7 ") {
		t.Fatal("field or tab text was not preserved")
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("unrepresentable style projection was not reported")
	}
}

func TestExporterRendersTableSpansAsPureMarkdown(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockTable, Table: &udf.Table{
		ColumnCount:  2,
		ColumnWidths: []udf.ColumnWidth{{Value: 90, Unit: udf.UnitPoint}, {Value: 50, Unit: udf.UnitPercent}},
		Rows: []udf.TableRow{
			{Type: "headerRow", Cells: []udf.TableCell{
				{GridRow: 0, GridCol: 0, RowSpan: 2, ColSpan: 1, VerticalAlign: "top", Border: udf.Border{Kind: "borderCell", Style: "borderStyle-solid", Width: 1, Spec: 15}, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Merged"}}}}}}},
				{GridRow: 0, GridCol: 1, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Top"}}}}}}},
			}},
			{Type: "dataRow", Cells: []udf.TableCell{{GridRow: 1, GridCol: 1, RowSpan: 1, ColSpan: 1, Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Bottom"}}}}}}}}},
		},
	}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("spanned table export failed")
	}
	for _, required := range []string{"| Merged | Top |", "|  | Bottom |", "---", "Merged", "Bottom"} {
		if !strings.Contains(out.String(), required) {
			t.Fatalf("table output is missing a required construct: %s", required)
		}
	}
	if strings.Contains(out.String(), "<table") || strings.Contains(out.String(), "rowspan=") || strings.Contains(out.String(), "width:") {
		t.Fatal("table export included HTML markup")
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("table spans/width approximations were not reported")
	}
}

func TestExporterEmbedsImagesAndSupportsHeaderFooterOption(t *testing.T) {
	data := []byte("fake-png-bytes")
	resource := udf.Resource{ID: "resource-1", Data: data, MediaType: "image/png", Width: 10, Height: 20}
	doc := &udf.Document{Resources: []udf.Resource{resource}, Headers: []udf.HeaderFooter{{Kind: "header", Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Header"}}}}}}}}, Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineImage, Image: &udf.Image{ResourceID: resource.ID, Width: 10, Height: 20}}}}}}}
	var out bytes.Buffer
	report, err := (Exporter{Options: Options{IncludeHeadersFooters: true}}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("image/header export failed")
	}
	if !strings.Contains(out.String(), "> **Header**") || !strings.Contains(out.String(), "data:image/png;base64,") || !strings.Contains(out.String(), "![Embedded image]") {
		t.Fatal("header or embedded image was not preserved")
	}
	if strings.Contains(out.String(), "<header") || strings.Contains(out.String(), "<img") || strings.Contains(out.String(), `width="10"`) {
		t.Fatal("header/image export included raw HTML")
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("image/header rendering compromise was not reported")
	}
}

func TestExporterGroupsListsAndKeepsNumberingSequence(t *testing.T) {
	props := udf.ParagraphStyle{Numbered: true, NumberType: "NUMBER_TYPE_NUMBER_DOT", ListID: "items"}
	doc := &udf.Document{Body: []udf.Block{
		{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Properties: props, Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "First"}}}}},
		{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Properties: props, Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Second"}}}}},
	}}
	var out bytes.Buffer
	if _, err := (Exporter{}).Export(context.Background(), doc, &out); err != nil {
		t.Fatal("list export failed")
	}
	if out.String() != "1. First\n1. Second\n" {
		t.Fatal("ordered list items were not emitted as one Markdown list")
	}
}

func TestExporterCoalescesFormattingAndOmitsEmptyMarkers(t *testing.T) {
	bold := udf.TextStyle{Bold: true}
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "VEKİLİ", Style: bold}},
		{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "", Style: bold}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: ":", Style: bold}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: " Av. Ali Ömür"}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: " ", Style: bold}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "GÜLTEKİN"}},
	}}}}}
	var out bytes.Buffer
	_, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("formatted paragraph export failed")
	}
	if out.String() != "**VEKİLİ:** Av. Ali Ömür GÜLTEKİN\n" {
		t.Fatalf("format boundaries changed: %q", out.String())
	}
	if strings.Contains(out.String(), "****") || strings.Contains(out.String(), "******") {
		t.Fatal("empty formatting spans emitted visible Markdown delimiters")
	}
}

func TestExporterSeparatesBoldLabelFromNormalValue(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "VEKİLİ:", Style: udf.TextStyle{Bold: true}}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Av. Ali Ömür"}},
	}}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("field label export failed")
	}
	if out.String() != "**VEKİLİ:** Av. Ali Ömür\n" {
		t.Fatal("label/value emphasis boundary was not represented safely")
	}
	if !hasDiagnostic(report.Diagnostics, "text.label_separator_inserted") {
		t.Fatal("inserted label/value separator was not reported")
	}
}

func TestExporterDoesNotEscapeOrdinaryDatesAndHyphens(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "11.09.2019 EK-1 (J-11)\n1. next\n---"}}}}}}}
	var out bytes.Buffer
	if _, err := (Exporter{}).Export(context.Background(), doc, &out); err != nil {
		t.Fatal("punctuation export failed")
	}
	if out.String() != "11.09.2019 EK-1 (J-11)\n1\\. next\n\\---\n" {
		t.Fatalf("ordinary punctuation was unnecessarily escaped: %q", out.String())
	}
}

func TestExporterRemovesLeadingLayoutTabsWithoutCodeBlocks(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{
		{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}},
		{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}},
		{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "Indented legal paragraph"}},
	}}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != nil {
		t.Fatal("indented paragraph export failed")
	}
	if out.String() != "Indented legal paragraph\n" {
		t.Fatal("layout tabs became Markdown code indentation")
	}
	if !hasDiagnostic(report.Diagnostics, "paragraph.leading_indentation_omitted") {
		t.Fatal("removed layout indentation was not reported")
	}
}

func hasDiagnostic(items []udf.Diagnostic, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestExporterPreservesEmptyAndWhitespaceParagraphs(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{
		{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{}},
		{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "  leading and trailing  "}}}}},
	}}
	var out bytes.Buffer
	if _, err := (Exporter{}).Export(context.Background(), doc, &out); err != nil {
		t.Fatal("whitespace paragraph export failed")
	}
	if strings.Contains(out.String(), "<p") || strings.Contains(out.String(), "<span") || !strings.Contains(out.String(), "  leading and trailing  ") {
		t.Fatal("empty or significant edge whitespace was not represented")
	}
}

func TestExporterIncludesLossyFallbackOnlyWhenRequested(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "unknown"}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), doc, &out)
	if err != udf.ErrUnsupported || out.Len() != 0 || len(report.Diagnostics) == 0 {
		t.Fatal("strict Markdown export did not reject content loss before writing")
	}
	report, err = (Exporter{Options: Options{AllowLossy: true}}).Export(context.Background(), doc, &out)
	if err != nil || !strings.Contains(out.String(), "Unsupported content omitted") || len(report.Diagnostics) == 0 {
		t.Fatal("explicit lossy fallback was not applied and reported")
	}
}

func TestLocalCorpusMarkdownExport(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".local-fixtures", "*.udf"))
	if err != nil {
		t.Fatal("find private local corpus")
	}
	if len(files) == 0 {
		t.Skip("private local UDF corpus is not present")
	}
	exporter := Exporter{Options: Options{IncludeHeadersFooters: true, AllowLossy: true}}
	for i, name := range files {
		doc, err := udf.ParseFile(name)
		if err != nil {
			t.Fatalf("parse local sample %d: %v", i, err)
		}
		if _, err := exporter.Export(context.Background(), doc, io.Discard); err != nil {
			t.Fatalf("export local sample %d: %v", i, err)
		}
	}
}

func TestExporterReportsApproximationAndRejectsUnprojectableContent(t *testing.T) {
	resource := udf.Resource{ID: "image-1", Data: []byte("image bytes"), MediaType: "image/png"}
	imageDoc := &udf.Document{Resources: []udf.Resource{resource}, Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineImage, Image: &udf.Image{ResourceID: resource.ID}}}}}}}
	var out bytes.Buffer
	report, err := (Exporter{}).Export(context.Background(), imageDoc, &out)
	if err != nil {
		t.Fatal("reported image placeholder should be exportable")
	}
	if len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != "resource.data_uri_renderer_support" {
		t.Fatal("image data URI limitation was not reported")
	}

	unknownDoc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "unknown"}}}}
	report, err = (Exporter{}).Export(context.Background(), unknownDoc, &out)
	if err != udf.ErrUnsupported {
		t.Fatal("unprojectable content should fail by default")
	}
	if len(report.Diagnostics) == 0 || report.Diagnostics[0].Class != udf.DiagnosticContentLoss {
		t.Fatal("content loss was not reported")
	}
}
