# udf

A local-first Go library for reading UYAP `.udf` documents and exporting their contents to common document formats.

## Direction

The project is a UDF library with a format-independent document model and independent exporters. Plain text is the diagnostic exporter; Markdown uses pure Markdown syntax and explicit loss reporting; DOCX is the first WordprocessingML exporter. The UDF exporter writes an unsigned derivative from the model. HTML is the visual inspection format, and PDF follows model validation and use cases.

```text
.udf archive → UDF parser → normalized document model → text / Markdown / DOCX / HTML / PDF exporters
                                      └→ unsigned UDF derivative
```

The parser and model are the core product. Exporters should depend on the model, not on UDF archive details, so they can be developed and replaced independently.

## Safety and scope

- Read UDF archives locally; do not upload documents or extracted contents to third-party services.
- Never modify the caller's original `.udf` bytes. The parsed model records signature presence only; an exported file is a convenience copy and does not carry or validate the source document's electronic signature.
- Keep signature bytes opaque unless signature inspection is explicitly implemented and documented.
- The UDF writer creates a new unsigned archive from the normalized model. It does not edit the source archive or transfer/validate its electronic signature. Exact UYAP rendering fidelity is out of scope.

## Status

The implementation includes bounded UDF parsing, plain-text and Markdown exporters, an unsigned UDF writer, and a DOCX exporter. Markdown covers the current model's rich text, lists, fields, breaks, tables/spans, inline images, and optional headers/footers using Markdown syntax only. Features Markdown cannot preserve produce diagnostics; unsupported visible content fails by default, and `markdown.Options{AllowLossy: true}` opts into reported placeholders. The UDF exporter refuses signed input by default; the `udfexport.Options{AllowUnsigned: true}` option explicitly permits an unsigned derivative, and unsupported source details require `AllowLossy`. It is a semantic model round-trip, not a byte-identical copy. The DOCX exporter uses pure-Go `docxgo/v2`; it maps the current model, including inline tabs, tab stops, verified UDF line spacing, and full-width tables when UDF provides no column widths. It applies the 360-twip document grid observed in the supplied Word examples. Unverified Swing paragraph-indent fields are reported and omitted because converting them directly caused extreme indentation in the comparison documents. When run-level font defaults are absent, it infers a fallback from the most common explicit source font and size and reports that choice. Other formatting it cannot represent is reported. Source UDF signatures are never transferred. HTML, PDF, and general multi-file attachment composition are not implemented. Offset handling currently uses the UTF-16 interpretation suggested by Java/Swing; the local corpus contains no supplementary characters, so this remains to be confirmed with a controlled UYAP Editor fixture. See [`PLAN.md`](PLAN.md) for the prioritized task backlog and remaining proof gates.

DOCX dependency note: `docxgo/v2` is currently MIT-licensed and its module has no third-party Go runtime dependencies. Its maintainers document AGPL-era repository history and a provenance audit for current v2; review that audit when upgrading or changing license assumptions ([project](https://github.com/mmonterroca/docxgo), [provenance audit](https://github.com/mmonterroca/docxgo/blob/master/docs/PROVENANCE_AUDIT.md)).

The module minimum is Go 1.26.0, chosen as the oldest Go release line currently receiving official support.

## Planned Go API

The proposed API returns a structured `udf.Document` with explicit text, ordering, and normalization guarantees. The complete model and semantics are specified in [`PLAN.md`](PLAN.md).

```go
doc, err := udf.Parse(reader)
if err != nil {
	return err
}

wholeBodyText := doc.Text()
pageWidth := doc.Page.Width
for _, block := range doc.Body {
	// Inspect paragraphs and tables in source order.
}

var exporter udf.Exporter = markdown.Exporter{}
report, err := exporter.Export(ctx, doc, output)
if err != nil {
	return err
}
for _, diagnostic := range report.Diagnostics {
	// Handle this export's reported approximations or omissions.
}
```

The planned entry points provide default and options-bearing forms for `io.Reader`, file paths, byte slices, and `io.ReaderAt`. `ParseReaderAtWithOptions` is the random-access foundation; other helpers delegate to it without forcing files/byte slices through the generic-reader buffer. `Document.Text()` is a deterministic body-text projection; structured properties, effective styles, page layout, headers/footers, normalized table grids, inline resources, and signature presence are available separately. General multi-file attachment composition is deferred until UDF relationship semantics are demonstrated. Parsing once should let callers choose exporters without depending on the CLI.

## Exporter extensions

Exporters are separate packages that consume `*udf.Document`. The root package will define a small `udf.Exporter` interface using `context.Context` and `io.Writer`, returning an `ExportReport` and an error. Any third-party package can implement it without registering with this library. Format options and dependencies stay inside each exporter package. Parse diagnostics describe source interpretation; export diagnostics describe compromises made by one conversion.

```go
type MyExporter struct{}

func (MyExporter) Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	// Convert the shared document model to your format.
	return udf.ExportReport{}, nil
}

var exporter udf.Exporter = MyExporter{}
report, err := exporter.Export(ctx, doc, output)
```

An exporter owns the conversion and documents unsupported model features. The caller owns the output writer. The exporter reads the normalized model and does not need to know how UDF ZIP/XML parsing works. See [`PLAN.md`](PLAN.md) for the full contract and fidelity rules.
