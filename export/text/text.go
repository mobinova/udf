// Package text exports a UDF document's deterministic body-text projection.
package text

import (
	"context"
	"fmt"
	"io"

	"github.com/mobinova/udf"
)

type Exporter struct{}

func (Exporter) Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	if err := udf.Validate(doc); err != nil {
		return udf.ExportReport{}, err
	}
	if dst == nil {
		return udf.ExportReport{}, fmt.Errorf("udf text: nil writer")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return udf.ExportReport{}, ctx.Err()
		default:
		}
	}
	_, err := io.WriteString(dst, doc.Text())
	return udf.ExportReport{}, err
}

func Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	return (Exporter{}).Export(ctx, doc, dst)
}
