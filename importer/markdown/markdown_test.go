package markdown

import (
	"context"
	"strings"
	"testing"

	udf "github.com/mobinova/udf"
)

func TestImporterMapsMarkdownBlocksAndFormatting(t *testing.T) {
	source := `# A heading

A **bold** and *italic* and ~~struck~~ phrase with ` + "`code`" + `.

- first
- second

| Name | Value |
| ---- | ----- |
| A    | B     |
`
	doc, err := (Importer{}).Import(context.Background(), strings.NewReader(source))
	if err != nil {
		t.Fatalf("import Markdown: %v", err)
	}
	if doc.FormatID != "markdown" || len(doc.Body) != 5 {
		t.Fatalf("unexpected imported structure: format=%q blocks=%d", doc.FormatID, len(doc.Body))
	}
	if doc.Body[0].Paragraph == nil || doc.Body[0].Paragraph.StyleName != "Heading 1" {
		t.Fatal("Markdown heading style was not retained")
	}
	if got := doc.Text(); !strings.Contains(got, "A heading\nA bold and italic and struck phrase with code.") || !strings.Contains(got, "first\nsecond") || !strings.Contains(got, "A\tB") {
		t.Fatalf("unexpected text projection: %q", got)
	}
	if doc.Body[1].Paragraph.Inlines[1].Run == nil || !doc.Body[1].Paragraph.Inlines[1].Run.Style.Bold {
		t.Fatal("strong emphasis was not mapped to bold")
	}
	if err := udf.Validate(doc); err != nil {
		t.Fatalf("importer returned invalid model: %v", err)
	}
}

func TestImporterDoesNotFetchLinksOrImages(t *testing.T) {
	source := "[visible link](https://example.invalid/path) ![alt text](https://example.invalid/image.png)"
	doc, err := (Importer{}).Import(context.Background(), strings.NewReader(source))
	if err != nil {
		t.Fatalf("import Markdown with links and images: %v", err)
	}
	if doc.Text() != "visible link alt text" {
		t.Fatalf("visible labels changed: %q", doc.Text())
	}
	if len(doc.Resources) != 0 {
		t.Fatal("Markdown importer fetched an image resource")
	}
	if !hasDiagnostic(doc.Diagnostics, "markdown.link_target_omitted") || !hasDiagnostic(doc.Diagnostics, "markdown.image_not_embedded") {
		t.Fatal("link and image limitations were not reported")
	}
}

func TestImporterEnforcesLimitsAndCancellation(t *testing.T) {
	if _, err := (Importer{Options: Options{MaxInputBytes: 2}}).Import(context.Background(), strings.NewReader("# too long")); err != udf.ErrLimitExceeded {
		t.Fatalf("expected input limit error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Importer{}).Import(ctx, strings.NewReader("hello")); err == nil {
		t.Fatal("cancelled import unexpectedly succeeded")
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
