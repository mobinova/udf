package text

import (
	"bytes"
	"context"
	"testing"

	"github.com/mobinova/udf"
)

func TestExporterWritesTextProjection(t *testing.T) {
	doc := &udf.Document{Body: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{Inlines: []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: "plain"}}}}}}}
	var out bytes.Buffer
	if _, err := (Exporter{}).Export(context.Background(), doc, &out); err != nil {
		t.Fatal("text export failed")
	}
	if out.String() != "plain" {
		t.Fatal("text exporter changed the body projection")
	}
}
