// Package udfimporter adapts the UDF parser to the shared importer interface.
package udfimporter

import (
	"context"
	"fmt"
	"io"

	udf "github.com/mobinova/udf"
)

// Importer reads a UDF archive into the normalized document model.
// ParseOptions configure the parser's archive and XML resource limits.
type Importer struct {
	Options udf.ParseOptions
}

var _ udf.Importer = Importer{}

func (i Importer) Import(ctx context.Context, src io.Reader) (*udf.Document, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	doc, err := udf.ParseWithOptions(src, i.Options)
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("UDF import cancelled: %w", ctx.Err())
		default:
		}
	}
	return doc, nil
}
