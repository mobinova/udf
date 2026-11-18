# UDF for Go

`github.com/mobinova/udf` is a local-first Go library for reading UYAP `.udf` documents into a normalized document model and exporting that model to text, Markdown, DOCX, or a new unsigned UDF archive.

[![Go CI](https://github.com/mobinova/udf/actions/workflows/ci.yml/badge.svg)](https://github.com/mobinova/udf/actions/workflows/ci.yml)

The parser and exporters run locally. The library does not send documents or extracted content to a service.

## Supported formats

| Operation | Package | Status |
| --- | --- | --- |
| Read UDF archives into a normalized model | `github.com/mobinova/udf` | Implemented with bounded ZIP/XML parsing |
| Plain text projection | `github.com/mobinova/udf/export/text` | Implemented; body text only |
| Markdown | `github.com/mobinova/udf/export/markdown` | Implemented; unsupported visible content is reported or rejected |
| Word DOCX | `github.com/mobinova/udf/export/docx` | Implemented for the documented model subset; compromises are reported |
| Write a UDF derivative | `github.com/mobinova/udf/export/udf` | Implemented; output is unsigned and not byte-identical |
| HTML and PDF | — | Not implemented |

This is not a claim of complete UDF compatibility or exact visual reproduction. The format inventory and remaining proof gates are tracked in [PLAN.md](PLAN.md).

## Requirements and installation

The module requires Go 1.26 or newer.

```sh
go get github.com/mobinova/udf
```

The DOCX exporter uses the pure-Go [`docxgo/v2`](https://github.com/mmonterroca/docxgo) backend. The parser, text, Markdown, and UDF exporter packages use the Go standard library.

## Quick start

Parse a local file, inspect its body text, then export Markdown:

```go
package main

import (
	"context"
	"log"
	"os"

	udf "github.com/mobinova/udf"
	"github.com/mobinova/udf/export/markdown"
)

func main() {
	doc, err := udf.ParseFile("input.udf")
	if err != nil {
		log.Fatal(err)
	}

	// Text is a deterministic body-text projection.
	log.Printf("body has %d characters", len([]rune(doc.Text())))

	out, err := os.Create("output.md")
	if err != nil {
		log.Fatal(err)
	}

	report, err := markdown.ExportWithOptions(
		context.Background(), doc, out,
		markdown.Options{AllowLossy: true},
	)
	closeErr := out.Close()
	if err != nil {
		log.Fatal(err)
	}
	if closeErr != nil {
		log.Fatal(closeErr)
	}
	for _, diagnostic := range report.Diagnostics {
		log.Printf("%s at %s: %s", diagnostic.Code, diagnostic.Location, diagnostic.Message)
	}
}
```

The runnable [conversion example](examples/convert) writes Markdown and DOCX outputs:

```sh
go run ./examples/convert -input ./document.udf -outdir ./out
```

The [custom exporter example](examples/custom-exporter) shows how to implement the public exporter interface without parser internals:

```sh
go run ./examples/custom-exporter ./document.udf > document.json
```

## Parsing options

The parser offers `Parse`, `ParseFile`, `ParseBytes`, and `ParseReaderAt`, with `WithOptions` variants for each input form. `ParseFile` and `ParseReaderAt` use random-access ZIP parsing; the generic `Parse(io.Reader)` buffers up to the configured compressed-input limit.

Default limits bound compressed and expanded archive data, entry counts, XML depth and node counts, retained text, embedded resources, tables, and diagnostics. Override them with `ParseOptions` when a trusted workload needs different limits.

```go
doc, err := udf.ParseFileWithOptions("large.udf", udf.ParseOptions{
	MaxCompressedBytes: 128 << 20,
	MaxTotalExpanded:   128 << 20,
})
```

The parser records signature presence and archive facts only. It does not verify the signature. The original archive remains the caller's responsibility.

## Exporters and conversion reports

Built-in exporters accept `context.Context`, a `*udf.Document`, and an `io.Writer`. They return an `udf.ExportReport` and an error. The caller owns the writer; an export may leave partial output if the writer fails.

- Markdown emits Markdown syntax and reports features it approximates. `markdown.Options{AllowLossy: true}` explicitly permits placeholders for unsupported visible content. `IncludeHeadersFooters` includes each header and footer once; page repetition cannot be represented in Markdown.
- DOCX exports the supported document subset. `docx.Options{AllowLossy: true}` permits reported omissions for unsupported visible content. UDF signatures are not copied.
- The UDF exporter writes a new semantic derivative. Signed source documents require `udf.Options{AllowUnsigned: true}`; source signatures and stale verification codes are not copied. The output is not a byte-for-byte reconstruction.
- Text export writes `Document.Text()` and does not preserve formatting.

Diagnostics have stable codes and structural locations. Treat diagnostic codes as machine-readable; messages are for people. A successful export can still report approximations, so inspect the report when fidelity matters.

Third-party exporters can implement `udf.Exporter` directly. The model and exporter interface do not require a registry or imports from internal packages.

## Known limits

- Only the UDF structures represented by the current public model can be exported. Unknown or unsupported content may require an explicit lossy option.
- DOCX export reports inferred font defaults, approximated layout, unsupported styles, and other compromises. Its output should be reviewed in the target word processor.
- Markdown is a text-oriented projection; it cannot represent page layout or all Word formatting.
- Related multi-file attachments, HTML, and PDF export are not implemented.
- UTF-16 text-buffer offsets are implemented based on the Java/Swing evidence available so far. A controlled UYAP Editor fixture with supplementary Unicode characters is still needed to verify the convention.
- Neither parsing nor export verifies or transfers an electronic signature. Preserve the source `.udf` when its signature matters.

## Development

Tests use synthetic archives and never require private legal documents. Optional local-corpus checks read `.local-fixtures/*.udf`, which is ignored by Git; do not add private UDF files, extracted text, or rendered pages to the repository.

```sh
gofmt -w $(find . -type f -name '*.go' -not -path './.git/*')
go test ./...
go vet ./...
go test -race ./...
```

The GitHub Actions workflow runs formatting checks, tests, vet, and race tests on pushes and pull requests.

## License

This repository does not yet declare a project license. Public visibility alone does not grant permission to reuse or redistribute the code.
