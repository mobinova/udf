# Repository guidance

## Confidential documents

- Treat UDF files and their extracted text as confidential legal documents. Process them locally; do not upload them to third-party services.
- Never commit source `.udf` documents, extracted document text, private fixture-derived snapshots, or rendered pages.
- Do not commit generated `.udf`, `.md`, or `.docx` exports made from a real document. The example's default `/out/` directory is ignored.
- Keep local corpus material under `.local-fixtures/`, which is Git-ignored. Use synthetic, non-sensitive fixtures for tracked tests and examples.
- Parsing and exporting must leave the caller's source archive unchanged. An exported DOCX or UDF is a derivative and does not carry or verify the source electronic signature.

## Implementation

- Refer to `AGENT_USAGE.md` for installed CLI commands and agent-facing import/export guidance.
- Keep UDF ZIP/XML interpretation in the parser and normalized model. Exporters consume the public model and report compromises through `ExportReport` diagnostics.
- Importers are explicitly selected by the caller and convert one source format into the shared model. Do not add filename-based format guessing or a global importer registry without a concrete need.
- Never fetch Markdown images, DOCX links, or other source references during import. Only retain resources embedded in the input package.
- Do not silently omit visible content. Reject unsupported visible content by default; any explicit lossy mode must report stable diagnostic codes and locations.
- Preserve source text and ordering. Do not normalize legal names, numbers, addresses, or spelling as part of conversion.
- Keep limits and input validation on untrusted ZIP, XML, text, and embedded resources.
- Keep examples self-contained, synthetic where possible, and runnable with the public API.
- Keep CLI inspection structural by default. Commands that emit document text must be explicit; JSON inspection and reports must not include source text or property values.
- Prevent CLI exports and reports from overwriting the input UDF. Route diagnostic reports separately from document bytes.
- Keep release builds reproducible and cross-platform. Never embed credentials or private UDF fixtures in release artifacts.

## Validation and Git

- For Go changes, run `gofmt`, `go test ./...`, `go vet ./...`, and `go test -race ./...` when the environment supports them.
- Add regression tests for new parser behavior, model invariants, and exporter mappings. Tests must not depend on the private corpus.
- Never create a Git commit, push, publish, or change repository visibility unless the user explicitly asks.
- Do not force-push or rewrite shared history.
