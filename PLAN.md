# UDF library plan

## Goal

Build a dependable Go library for reading UYAP UDF documents into a well-defined, format-independent document model. Add exporters as independent packages so callers can choose output formats and applications can add their own.

The project should aim to be the best practical Go package for UDF by prioritizing format coverage, robust parsing, clear APIs, confidentiality, and documented fidelity limits. “Best” should be demonstrated through fixtures and comparisons, not assumed from a single reference implementation.

## Backlog at a glance

Execute the checkbox tasks below in order: P0 source proof (T01–T05), P0 parser/model (T06–T12), P0 exporters (T13–T16), P1 CLI/visual conversion (T17–T22), P2 release/expansion (T23–T25, T28–T29), with T26–T27 applied throughout. Start at **T01**. The local corpus has been inventoried at the ZIP-manifest level; its XML semantics are not yet verified.

## Format references

The `gokselb/udfpdf` project is a useful independent reference for UDF structures and conversion behavior. Its README describes `.udf` as a ZIP package containing `content.xml`, optional document properties, and signature data, and documents PDF, DOCX, HTML, Markdown, and text output. Use it to compare observed behavior and identify format cases; do not copy implementation code without reviewing its license and attribution requirements.

Treat UDF format details as hypotheses until confirmed against representative, legally accessible documents and UYAP output. Do not include confidential document contents in repository fixtures.

The reference project's public README describes an XML v1.8 content format with page format, paragraphs, tables, styles, optional document properties, and signature data. This is a useful checklist, not a format specification; verify each detail against local files and UYAP Editor output. Record implementation observations as anonymized structural notes rather than copying its implementation.

## Local corpus handling and parser discovery

- Keep user-provided documents in the ignored `.local-fixtures/` directory only. Never commit, publish, or upload these legal documents or extracted contents. Keep every original `.udf` byte-for-byte intact.
- Prefer a small, diverse local corpus over many duplicate downloads. Record hashes and archive structure to identify duplicates; do not put sensitive filenames, text, metadata, or signature bytes in tracked notes.
- Inspect ZIP central-directory entries first. Read XML entries from the archive stream, without extracting to disk by default. Do not log document text or personal metadata.
- Build an anonymized format inventory: required/optional entries, XML version/signatures, namespace and element variations, singleton-versus-repeated nodes, styles, tables, page settings, offsets, whitespace/control elements, and unknown fields. Distinguish observed facts from reference-derived hypotheses.
- Handle malformed ZIP/XML, duplicate or path-like entry names, encrypted entries, oversized compressed/uncompressed entries, excessive compression ratios, and resource exhaustion with bounded reads and clear errors. Never trust declared ZIP sizes without enforcing limits while reading.
- Keep `sign.sgn` opaque. Parsing never modifies the caller's source archive; the model records signature presence only, and an exported derivative does not carry or validate that signature.
- Only create shareable test fixtures after sensitive content and identifying metadata have been removed and the resulting fixture is independently checked. Until then, tests can use synthetic XML/archive fixtures and local-only samples.

Initial local archive survey (45 unique samples copied into the ignored corpus): all inspected files are ZIP archives. Their entry manifests include `content.xml` and `sign.sgn`; most also include `documentproperties.xml`, while three observed samples omit it. This confirms the parser must support missing properties. XML element-level variation remains to be inventoried locally without logging document text or identifying values.

## Proposed package architecture

```text
udf/                  public parsing API and document types
udf/internal/...      archive/XML implementation details
udf/export/text/      plain text exporter
udf/export/udf/       unsigned UDF archive exporter
udf/export/docx/      DOCX exporter (docxgo/v2 backend)
udf/export/html/      HTML exporter
udf/export/pdf/       PDF exporter
udf/export/markdown/  Markdown exporter
cmd/udf/              minimal inspection/export CLI, built on the public API
```

Keep the public model independent of PDF or any other output library. The parser owns UDF-specific interpretation; exporters consume the normalized model. Avoid premature abstractions: define the document model around UDF structures and expose stable extension points only where a real use case needs them.

The root `udf` package owns the model and the small exporter contract. Built-in and third-party exporters are ordinary Go packages that import `udf`; no plugin registry, format switch, or dependency on this repository's exporter list is required. External packages can implement formats this project does not ship.

Keep the root parser/model and text/HTML exporters standard-library-only. The DOCX package imports docxgo/v2, whose current module has no third-party Go runtime dependencies; PDF backend boundaries remain subject to T21.

## UDF input and parsing

- Read the ZIP archive directly; never extract entries to disk by default.
- Require `content.xml`; treat `documentproperties.xml` and `sign.sgn` as optional entries.
- Use one random-access archive path internally. `ParseFile` uses file random access, `ParseBytes` avoids an extra full copy, `ParseReaderAt` accepts existing random-access storage, and `Parse(io.Reader)` buffers only to the compressed-input limit. Temporary-file spooling is not implicit.
- Define documented budgets for compressed input bytes, ZIP entry count and directory work, per-entry and aggregate expanded bytes actually consumed, XML nesting/token/attribute/text limits, normalized runs/blocks/table dimensions/style traversal, decoded resource bytes and image dimensions/pixel counts, unknown XML retention, diagnostic count/message length, and PDF page count/layout work. Choose defaults from corpus measurements. Compression ratios supplement absolute limits; they are not the sole defense.
- Account for work done by the ZIP reader before application-level entry checks. Bound input before archive construction and make memory guarantees precise.
- Consume every ZIP entry, including opaque signature data, through its legitimate end under the byte budgets and propagate decompression/checksum errors. A valid XML root followed by corrupt or truncated ZIP data must not parse successfully.
- Reject malformed archives/XML with stable stage errors. Detect duplicate and path-like entry names and encrypted entries. Never trust declared sizes without enforcing limits during reads.
- Parse the content XML, including CDATA and the UYAP `<properties>`, `<elements>`, and `<styles>` structures.
- Preserve text, whitespace, tabs, newlines, ordering, and metadata as faithfully as the format permits.
- Keep signature bytes opaque and out of the normalized model. Parsing never modifies the caller's source archive; exporters do not carry or validate its signature.
- Use token-aware decoding or custom unmarshalling at relevant boundaries so unknown elements/attributes are observable; ordinary `xml.Unmarshal` into fixed structs can silently discard them. Distinguish supported constructs, understood-and-ignored metadata, and unknown constructs that may affect content/layout. Keep unknown position; do not collect arbitrary descendant text as recovery. Raw XML retention is opt-in and bounded.
- Sanitize and bound input-controlled XML names, ZIP names, decoder errors, and locations before placing them in errors or diagnostics. Never include source text or property values in logs.
- A context-aware parser, if added, can check cancellation between operations; it cannot promise to interrupt an arbitrary blocking caller-provided reader.

## Proposed public API and document model

The parser returns one normalized `*udf.Document`. It represents the readable document content and parsed properties; it is not a mutable UDF editor and does not claim to reproduce UYAP's exact layout or carry the source signature.

```go
package udf

func Parse(r io.Reader) (*Document, error)
func ParseFile(path string) (*Document, error)
func ParseBytes(data []byte) (*Document, error)
func ParseReaderAt(r io.ReaderAt, size int64) (*Document, error)
func ParseWithOptions(r io.Reader, opts ParseOptions) (*Document, error)
func ParseFileWithOptions(path string, opts ParseOptions) (*Document, error)
func ParseBytesWithOptions(data []byte, opts ParseOptions) (*Document, error)
func ParseReaderAtWithOptions(r io.ReaderAt, size int64, opts ParseOptions) (*Document, error)
func Validate(doc *Document) error

type Opt[T any] struct {
    Val T
    Set bool
}

type Diagnostic struct {
    Code     string
    Severity DiagnosticSeverity
    Class    DiagnosticClass
    Location string
    Message  string
}

type ExportReport struct {
    Diagnostics []Diagnostic
}

type Document struct {
    Properties   DocumentProperties
    Page         PageLayout
    Styles       []Style
    Resources    []Resource
    Headers      []HeaderFooter
    Body         []Block
    Footers      []HeaderFooter
    Signature    SignatureInfo
    Diagnostics  []Diagnostic
}

func (d *Document) Text() string
func (d *Document) Walk(fn func(Block) error) error
func (p *Paragraph) Text() string
```

Diagnostic classes separate source interpretation from conversion compromises. Keep a small documented enum and stable codes; free-form messages are not protocol. Examples include parser codes `source.unknown_element` and `style.unresolved_reference`, and export codes `font.substituted`, `content.unsupported_field`, `table.unsupported_row_span`, and `attachment.omitted`.

Every input form has default and options-bearing entry points. `ParseReaderAtWithOptions` is the foundational random-access path; `ParseReaderAt` supplies defaults, `ParseFileWithOptions` opens/closes its file and delegates, and `ParseBytesWithOptions` delegates without another archive copy. `ParseWithOptions` buffers a generic reader only up to the compressed-input limit, then delegates. Caller-owned readers/storage remain caller-owned. `ParseWithOptions` and all sibling options APIs accept the same `ParseOptions` so callers do not lose limits or policy when changing input type. The returned model owns or copies every resource it needs and remains usable after parsing finishes. Parsing errors identify a fixed stage and safe archive-entry role where possible, never document text or property values.

`Opt[T]` is a small presence wrapper for source/override inspection values when they must be exposed: `Set == false` means absent; `Set == true` with `Val == 0` or `false` means explicitly set. Effective paragraph/run/page properties are flat, fully resolved values and do not use `Opt[T]`. Prefer this wrapper over allocating a pointer for every optional scalar.

`Document` fields are:

- `Properties`: optional UYAP/document properties, separate from visible body text. Keep known fields typed (for example title, author, timestamps, serial and verification values only after their meanings are confirmed); preserve additional property name/value pairs in a clearly format-specific collection.
- `Page`: page width, height, margins, orientation, and header/footer distances, normalized to points while retaining the source unit when known.
- `Styles`: source style IDs/names for inspection plus one authoritative effective property view for rendering. The parser resolves source defaults, inheritance, and direct overrides; exporters never apply UDF-specific precedence.
- `Resources`: document-level decoded inline resources (initially images), deduplicated by SHA-256 of decoded bytes and referenced by stable document-local IDs from inline image nodes. Each resource records media type when known, digest, and bytes owned by the model. Keep bytes subject to per-resource/aggregate limits; hash equality only deduplicates identical bytes and does not imply semantic equivalence.
- `Headers` and `Footers`: ordered content where present. The first release has one document-wide page layout; unsupported sections or header/footer variants produce diagnostics. Do not flatten them into body text by default.
- `Body`: source-ordered blocks. Initial block kinds are paragraph, table, explicit page break, and unsupported/unknown. A block carries exactly one corresponding payload; parser output guarantees that invariant.
- `Signature`: presence and archive-entry facts only. Signature bytes remain opaque and are not exposed as validated, transferred, or preserved by an export.
- `Diagnostics`: parse-time unsupported constructs, unresolved styles, uncertain variants, and normalization/recovery notes. Use stable codes and structural locations; do not put source text or unbounded input-controlled names in diagnostics.

Content types:

- `Block` is a tagged struct with `Kind` (`BlockParagraph`, `BlockTable`, `BlockPageBreak`, or `BlockUnknown`) and one matching payload where needed (`Paragraph`, `Table`, or `Unknown`).
- `Paragraph` has paragraph properties and ordered inline content. Paragraph properties cover alignment, indentation, before/after spacing, line spacing, tabs, and style reference.
- `Inline` is a tagged struct preserving order and distinguishing text runs, tabs, explicit line breaks, fields, images/objects, and unknown items. Image nodes refer to `Resources` by ID and preserve placement dimensions. Mutually exclusive payloads are pointers (or compact arena indexes), so a slice of `Inline` does not pay for the largest union variant in every element. Text runs carry text plus run formatting: font family/size, bold, italic, underline, strike-through, foreground/background color, and style reference. Include only fields observed in UDF or needed to preserve a source construct.
- Effective paragraph/run properties are authoritative for rendering; retain source style IDs separately for inspection. Represent absent values distinctly from explicit false and zero.
- `Table` has ordered rows and cells. Cells contain ordered blocks to support paragraphs and, if observed, nested tables. Normalization assigns zero-based `GridRow`, `GridCol`, `RowSpan`, and `ColSpan`; exporters consume this resolved grid rather than independently solving occupancy. `Validate` checks collisions, impossible spans, and ragged/invalid occupancy. Preserve widths as tagged values (`UnitPt`, `UnitPercent`, `UnitAuto`) and do not resolve percentages against page width before the output container is known. Retain borders and cell alignment.
- `Block` and `Inline` are tagged structs whose mutually exclusive payloads are pointers or compact arena indexes, not large inline unions. `Validate` checks the discriminator/payload combination.
- `HeaderFooter` contains ordered blocks and its observed scope/variant. Unknown blocks keep their position so later exporters cannot silently reorder surrounding content.
- `SignatureInfo` reports whether `sign.sgn` exists and, if exposed, its size as reported by ZIP directory metadata. This is not proof the entry was fully inspected; signature bytes remain opaque.
- Shared values use explicit enums for alignment/orientation and numeric values with documented units. `TabStop` records position, alignment (left/right/decimal/other confirmed variants), and leader style, in addition to literal tab characters in text. Distinguish absent values from explicit zero values where the source format does.

Text behavior:

- `Document.Text()` is a deterministic body-text projection, not a byte-preserving XML reconstruction. It excludes properties, headers, footers, signatures, and separately supplied files.
- Paragraph boundaries contribute exactly one `\n`: paragraphs `A`, empty, `B` project to `A\n\nB`. No synthetic final newline is appended; a final empty paragraph therefore contributes its boundary newline. Source text within a paragraph preserves decoded whitespace, tabs, and explicit line breaks. XML line-ending normalization means original CRLF/CR bytes are not preserved.
- An explicit page-break block projects to form feed (`\f`) in `Text()`; visual exporters may use a format-specific page-break representation and report any approximation.
- Structural paragraph separators appear exactly once: if verified source ranges include a terminator used as a structural boundary, normalization assigns it to the boundary rather than also retaining it as inline text. Controlled fixtures must establish this rule for actual UDF variants.
- Tables project in row-major order: cells are separated by `\t`, rows by `\n`, and multiple paragraphs within one cell by `\n`. Merged cells are represented once according to their structural cell, not duplicated to fill the visual grid. Cached visible field values are used when present. Unknown non-text inline objects are omitted from the string projection but remain represented/diagnosed; unsupported text-bearing content must not disappear silently.
- `Paragraph.Text()` returns its inline textual projection, preserving text, tabs, and explicit line breaks. Non-text items are omitted from the string projection but remain in the structured model and diagnostics.
- Exporters may offer options to include headers/footers or represent tables differently; those choices do not change the core `Text()` contract.
- `Walk` visits body blocks only, in preorder. Nested table-cell blocks are visited row-major. It stops at the first visitor error and does not traverse separate related files.

Offset and ordering rules are a P0 proof gate. Keep UTF-8 byte offsets, Unicode code-point offsets, and UTF-16 code-unit offsets as separate hypotheses. `A😀İB` places `B` at 7, 3, and 4 respectively. Turkish characters alone do not distinguish code points from UTF-16 units. Use verified document structure to determine ownership and reading order; use verified offsets only to select text from the source buffer. Do not globally sort XML structures by offset. Do not trim, normalize Unicode, or clamp invalid ranges. Conflicting text-ownership, negative, or out-of-bounds spans fail with a specific parse error unless fixtures establish valid overlap semantics; explicit best-effort recovery must emit a diagnostic. Distinguish XML source positions, text-buffer offsets, and normalized-model locations.

Before offset code is implemented, create synthetic documents with UYAP Editor and record editor version and proven behavior: Turkish and non-BMP characters, combining sequences, style boundaries around those characters, empty paragraphs, tabs, explicit breaks, trailing paragraph terminators, inline image/object placeholders, and tables followed by paragraphs. Establish offset unit, target text buffer, terminator/object-placeholder accounting, structure-vs-buffer order, and invalid/overlapping range policy. Do not infer an offset unit by choosing whichever interpretation keeps ranges in bounds.

Normalization guarantees: the parser resolves UDF-specific text-buffer references, style inheritance/defaults, and direct formatting precedence once. The model exposes one authoritative effective style/property view for exporters; source style IDs may remain for inspection but exporters do not reimplement UDF precedence. Values preserve presence separately from explicit false/zero. Defaults must be confirmed source defaults, explicit caller choices, or documented fallback diagnostics. Use distinct types/fields for absolute spacing and line-spacing multipliers. The first release supports one document-wide page layout; layout changes, sections, or unsupported header/footer variants are diagnosed. Preserve explicit page breaks as model blocks. Fields keep cached visible values; understood pagination fields may be regenerated only under an explicit exporter option, and unknown fields use cached values with a diagnostic. Never invent dates or missing values.

Public model structs are mutable, so built-in exporters must call shared `Validate` and reject invalid tags/payload combinations, nil required payloads, impossible spans, and invalid dimensions. Concurrent exports are supported only when callers do not mutate the document during export. `Validate` checks model invariants, not source authenticity or UDF signature validity.

The API outline is a concrete target, not a promise that every UDF construct is already understood. Before freezing field names, inventory XML structures in the local corpus and distinguish absent, empty, and defaulted values. Keep archive/XML structs under `internal/`.

Attachment handling:

- Keep inline images/objects in the content model separate from external legal attachments or files submitted alongside a UDF.
- Defer recursive `Attachment{Document *Document}` composition. The current corpus has not established embedded file attachments or UDF relationship metadata, and recursive composition adds cycle, recursion-budget, export, and ownership questions.
- Preserve proven source-contained references as non-recursive metadata with an explicit origin. Any future caller-associated sidecar API must distinguish caller-supplied files from source-contained references. A single-file parse never guesses sibling relationships, follows paths, or fetches URLs.
- The local archive survey found no extra file entries beyond `content.xml`, `sign.sgn`, and optional `documentproperties.xml`. An independent format note describes inline base64 images in `content.xml`; treat that as a hypothesis until verified against controlled local samples and UYAP Editor. Add general attachment composition only with a concrete workflow and fixture. If introduced, disallow cycles and define aggregate limits.

## Exporter extension contract

An exporter converts the shared `Document` model into one caller-provided output stream. The core package does not pick file names, create files, close caller-owned writers, register exporters, or import format libraries.

```go
package udf

type Exporter interface {
    Export(ctx context.Context, doc *Document, dst io.Writer) (ExportReport, error)
}
```

Contract rules:

- Any package may implement `udf.Exporter`; callers can pass any implementation directly. The parser does not need to know the output format.
- The exporter reads the normalized model only. It must not parse UDF ZIP/XML itself, mutate the `Document`, or interpret opaque signature data as a transferable signature.
- The caller owns `dst` and closes it. Exporters write to it and return write/conversion errors. They should honor context cancellation during long conversions.
- Format-specific options and dependencies belong to the exporter package. Use constructors or package-local option types; do not add PDF, DOCX, or other format settings to the core `Document` or `udf.Exporter` interface.
- `ExportReport.Diagnostics` describes this conversion only. It is separate from parse-time `Document.Diagnostics`, so exporters can run concurrently without mutating shared state. Diagnostics use stable codes, severity, class, structural location, and bounded human-readable messages. Code and location are machine-readable; message wording is not.
- Diagnostic codes identify fixed conditions (examples: `font.substituted`, `content.unsupported_field`, `table.unsupported_row_span`, `attachment.omitted`); structural locations use model paths such as `body[4].table.rows[2].cells[1]`. Messages must not include document text or property values.
- Intentional projection (such as plain text omitting bold styling) is normal. Visual approximations (such as font substitution or approximated table layout) must be reported. Unexpected loss of readable content fails by default; a package-specific explicit fallback option may allow it only with a diagnostic.
- Exporters document mappings for text, tables, page layout, styles, headers/footers, inline resources, metadata, and unsupported blocks. Single-stream writes may be partial on error unless the exporter guarantees atomic output. The CLI must write to a temporary destination and replace the requested output only on success. Exported output is a derivative; no exporter claims to preserve or transfer the source UDF signature.
- Single-stream formats use `io.Writer` (including ZIP-based formats such as DOCX). A format that inherently needs a directory or several sidecar files may provide an additional package-specific API; it still consumes the same model and must make ownership of created files clear.

Example of a third-party exporter:

```go
type MyFormat struct{}

func (MyFormat) Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
    // Convert doc.Body, doc.Page, doc.Styles, and doc.Properties.
    return udf.ExportReport{}, nil
}

var exporter udf.Exporter = MyFormat{}
report, err := exporter.Export(ctx, doc, dst)
```

Built-in exporters follow the same contract under separate packages, for example `udf/export/text`, `udf/export/markdown`, `udf/export/docx`, `udf/export/html`, and `udf/export/pdf`. Each can also expose package-specific convenience functions and options without changing the shared contract. Text is the diagnostic view; DOCX and Markdown are implemented for the current model; HTML is the planned visual inspection surface; PDF follows model validation.

For PDF, evaluate `github.com/gpdf-dev/gpdf` first. Treat it as a candidate, not a settled dependency: confirm explicit font selection, Turkish and non-BMP glyph coverage, combining marks, text wrapping, page sizing, paragraph alignment, pagination, and rendering quality using locally held fixtures. The reviewed source path appears to fall back through WinAnsi with `?` for unrepresentable text, and the reviewed table layout path references column spans but not row spans; confirm these observations against the pinned candidate version and treat both as adoption risks, not established runtime results. PDF fallback fonts must be deliberately configured; no automatic font downloads. Use the same resolved font for measurement and rendering, report substitutions/missing glyphs, and decide whether a redistributable bundled default or caller-provided fonts are required. Test all four regular/bold/italic/bold-italic variants with `İ ı I i Ş ş Ğ ğ Ç ç Ö ö Ü ü ₺`, plus a non-BMP character and combining sequence.

Treat merged tables as an adoption gate: test horizontal/vertical spans, merges beside ordinary cells and near page boundaries, repeated headers, multiple paragraphs in cells, and rows that exceed a page. Also test mixed-style justified paragraphs, indentation, tabs, long unbreakable text, and style changes at wrap boundaries. Compare extracted text/content separately from rasterized page geometry; exact PDF bytes are not the primary golden. Pin the backend version and font assets after qualification and rerun the pack on upgrades. A feature not faithfully supported must fail or return a reported fallback.

Third-party exporter authors should be able to rely on the documented public model and its text/order semantics, not `internal` packages or undocumented XML behavior. Keep additions to the core model format-neutral and backward-compatible where practical. If the model cannot represent a common construct, extend the model before teaching every exporter to independently decode UDF details.

## Minimal inspection CLI

Build a thin `cmd/udf` early, using only the public library API:

- `udf inspect document.udf --json` reports structural counts, format information, parse diagnostic codes, and signature presence by default. It does not print document text or personal metadata unless the caller explicitly requests those fields.
- `udf text document.udf` explicitly writes the deterministic body-text projection.
- `udf export document.udf --format html --output document.html` selects an exporter without adding parsing logic to the CLI.
- Keep stdout clean when it carries text or exported bytes. Send progress/diagnostics to stderr or a requested report destination.
- For file outputs, write to a temporary file in the destination directory and replace the destination only after successful conversion. Preserve any prior output on failure.
- HTML is the first visual inspection exporter. Default output is self-contained, offline, and passive: escape source text using contextual HTML escaping, do not inject raw unknown XML as HTML/CSS, do not load remote assets/fonts, and define explicit behavior for links and unsupported objects.

## Concrete task list

Work top to bottom. A task is complete only when its deliverable and done condition are met. Keep implementation and verification local; repository instructions prohibit running code/tests without an explicit request.

### P0 — prove source semantics and text correctness

- [ ] **T01 — Build the controlled fixture pack.** Create synthetic documents in UYAP Editor with a Turkish string, `A😀İB`, a combining sequence, formatting boundaries around each, empty paragraphs at document end and cell end, tabs and explicit left/right/decimal tab stops with leaders, explicit line breaks, trailing paragraph terminators, an inline image/object, a table followed by paragraphs, and a cached field. Record the UYAP Editor version and which behavior each sample proves. Use no real legal matter data.
  - **Done when:** fixtures cover every listed case and their expected observations are recorded without private document data.
- [ ] **T02 — Resolve offset and ordering rules.** Test UTF-8 byte, Unicode code-point, and UTF-16 code-unit hypotheses. Java/Swing indexing makes UTF-16 a strong hypothesis, not an assumption. Identify the target text buffer; determine whether paragraph/cell terminators and object placeholders consume positions; compare XML structure order with text-buffer order.
  - **Done when:** evidence distinguishes all three units with non-BMP text, verifies terminator/object accounting, and parser rules use structure for ownership/order and spans only for text selection. If UTF-16 is confirmed, build one bounded UTF-16-code-unit-to-UTF-8-byte index map per text buffer and validate that ranges land on legal boundaries.
- [ ] **T03 — Specify malformed-span behavior.** Add fixtures for out-of-range, negative, conflicting/overlapping text ownership, and valid style-range overlap if the format permits it.
  - **Done when:** strict parsing returns stable errors for invalid ranges; any best-effort recovery is opt-in and produces a diagnostic; no range is silently trimmed, normalized, or clamped.
- [ ] **T04 — Lock down `Text()`.** Add exact goldens for paragraphs `A`, empty, `B` → `A\n\nB`; final empty paragraphs; source paragraph terminators; multiple paragraphs in table cells; merged cells; fields with cached values; tabs; explicit breaks; and unsupported inline objects.
  - **Done when:** projection has one documented final-newline policy, never duplicates structural boundaries, and matches goldens around non-BMP/combining characters and formatting boundaries.
- [ ] **T05 — Maintain the evidence matrix.** Track each feature/variant, evidence state (`hypothesized`, `observed`, `fixture-verified`), parser/exporter support, fixture, and known fallback.
  - **Done when:** no feature is described as supported solely because it appears in a reference implementation or one unverified sample.

### P0 — implement the bounded parser and normalized model

- [ ] **T06 — Build a private structural profiler and set resource budgets.** Before production parser code, add a local developer command that scans only `.local-fixtures/*.udf` and emits aggregate, zero-PII JSON: unique XML element paths, attribute names, whitelisted closed-enum values, nesting/entry/content-size distributions. Never emit text, arbitrary property/attribute values, filenames, hashes, signatures, or per-document records. Use profile results to choose limits for compressed input, ZIP entries/directory work, expanded bytes, XML depth/tokens/attributes/text, blocks/runs/table dimensions/style traversal, decoded image bytes/dimensions/pixels, unknown XML retention, diagnostics, and later PDF pages/layout work.
  - **Done when:** profile output is aggregate-only and reviewed for privacy; every budget has a documented default, override path, and boundary fixture; absolute limits protect against oversized and highly compressed inputs.
- [ ] **T07 — Implement symmetric ZIP input adapters.** Make `ParseReaderAtWithOptions` the foundational parser. Implement default and options-bearing forms for `ParseReaderAt`, `ParseFile`, `ParseBytes`, and `Parse(io.Reader)`. File/byte/random-access APIs must not route through the generic reader buffer; generic readers buffer only up to the compressed-input limit. Keep caller-owned readers/storage open and avoid implicit temp-file spooling. The module currently targets Go 1.26.0, the oldest Go release line supported by the Go project as of this plan update; revisit the baseline as the support window advances.
  - **Done when:** every input form accepts the same `ParseOptions`; all helpers delegate to the same random-access core; returned documents remain usable after parsing; input bytes and caller-owned resources are unchanged.
- [ ] **T08 — Enforce archive integrity and limits.** Require `content.xml`; allow missing `documentproperties.xml`/`sign.sgn`; reject duplicate/path-like names, encryption, malformed archives, and limit violations. Consume every entry, including opaque signature bytes, through EOF so CRC/decompression failures surface.
  - **Done when:** truncated or corrupted entries cannot yield a successful parse, and each error is bounded and identifies a safe stage/entry role without document content.
- [ ] **T09 — Decode XML without silent loss.** Parse CDATA and supported properties/elements/styles with token-aware decoding or custom unmarshalling. Distinguish supported, understood-and-ignored, and unknown content; preserve unknown structural position; do not infer visible text from arbitrary descendants. Make raw XML retention opt-in and bounded.
  - **Done when:** injected unknown text-bearing elements/attributes are represented or produce predictable diagnostics; diagnostics sanitize input-controlled names and never contain source text or properties.
- [ ] **T10 — Normalize model semantics and inline resources once.** Resolve source text references, style defaults/inheritance/direct overrides, absent-vs-zero/false semantics, effective table grid coordinates/spans, page dimensions/units, explicit page breaks, cached fields, and first-release single-layout scope. Use `Opt[T]` only for exposed raw/override values; keep effective properties flat and resolved. Decode embedded images into a document-level `Resources` pool with SHA-256 deduplication; inline image nodes refer to resource IDs. Model table widths as point, percent, or auto without premature percentage resolution.
  - **Done when:** HTML/PDF exporters do not need UDF-specific defaults, inheritance, field, offset, or table occupancy logic; repeated identical image bytes occupy one resource; size/pixel budgets apply; unsupported sections/layout changes produce diagnostics; no source defaults are guessed.
- [ ] **T11 — Implement model validation and traversal.** Add `Validate` for tagged-block payloads, dimensions, spans, and other public invariants. Define `Walk` as body-only preorder, table cells row-major, stop at first visitor error, and do not traverse related files.
  - **Done when:** invalid caller-built models fail clearly; parser-produced models validate; traversal order has exact fixtures.
- [ ] **T12 — Separate parse diagnostics.** Define stable codes, severity, class, structural location, and bounded human-readable message. Keep parse diagnostics on `Document` and immutable during export.
  - **Done when:** unsupported elements, unresolved styles, uncertain variants, and recovery decisions have stable codes and do not leak document text/metadata.

### P0 — establish exporter contracts and loss reporting

- [x] **T13 — Finalize `udf.Exporter`.** Use `Export(ctx, doc, dst) (ExportReport, error)`. Caller owns the writer; exporter does not mutate the model; format options/dependencies remain package-local; errors may leave partial output unless atomicity is promised.
  - **Done when:** package docs specify context behavior, writer ownership, concurrent-read requirements, error behavior, and report semantics.
- [ ] **T14 — Define conversion diagnostics and strictness.** Use stable codes and model paths. Treat intentional projection as normal, report visual approximation, and fail by default on unexpected readable-content loss; explicit fallback options must report the loss.
  - **Done when:** a successful export that substitutes a font or approximates a table reports it, and an unsupported visible text/object construct cannot disappear with a clean report.
- [x] **T15 — Implement the text exporter.** Consume only the public model and `Text()` contract; document how tables, headers/footers, fields, and unsupported items map.
  - **Done when:** text export matches model projection goldens and returns appropriate diagnostics without treating unrepresentable bold/italic as content loss.
- [ ] **T16 — Prove third-party extensibility.** Add a minimal example exporter in a package outside the root package, importing only public APIs.
  - **Done when:** it compile-checks against the public model/interface without importing `internal` packages or registering with the library.

### P1 — inspection and visual conversion

- [ ] **T17 — Build the minimal CLI.** Add `udf inspect document.udf --json`, `udf text document.udf`, and `udf export ... --format html --output ...`. Inspect defaults to structural counts, format info, diagnostic codes, and signature presence; text/metadata require explicit requests.
  - **Done when:** stdout stays clean for data, progress/report output goes to stderr or an explicit report destination, and file exports use a temporary destination replaced only after success.
- [ ] **T18 — Implement safe HTML first.** Build self-contained, offline, passive HTML from the normalized model with contextual escaping. Define link and unsupported-object handling; never inject unknown XML or load remote assets/fonts.
  - **Done when:** escaping/security fixtures pass and every unsupported visible construct is represented or reported.
- [ ] **T19 — Decide heavy-exporter module boundaries.** Keep the root parser/model and text/HTML exporters standard-library-only. Before adding PDF dependencies, compare dependency trees and binary sizes for same-module optional subpackages versus nested Go modules; include `go.work` development ergonomics and release/versioning costs.
  - **Done when:** choose and document the smallest-maintenance structure that keeps core-only consumers free of PDF/font dependencies; confirm with dependency inspection and a core-only build before release.
- [ ] **T20 — Run an early PDF proof of concept.** Use it to identify missing model fields while the model can still change; keep backend-specific layout code inside the chosen `udf/export/pdf` package/module.
  - **Done when:** prototype findings are recorded as model gaps or backend limitations, without claiming PDF support from a successful write alone.
- [ ] **T21 — Qualify the PDF backend.** Evaluate `gpdf` with explicit fonts and pinned assets. Test regular/bold/italic/bold-italic for `İ ı I i Ş ş Ğ ğ Ç ç Ö ö Ü ü ₺`, non-BMP text, combining marks, mixed styles, justification, indentation, tabs, long unbreakable text, and style changes at wraps. Test horizontal/vertical merges, repeated table headers, multiple paragraphs per cell, long rows, pagination, clipping, and headers/footers.
  - **Done when:** extracted text and rasterized page geometry pass separate gates; missing glyphs/substitution/layout compromises are diagnosed; backend/font versions are pinned. Exact PDF bytes are not the main golden.
- [x] **T22 — Implement Markdown for the current document model.** Map paragraph/run formatting, lists, fields, breaks, tables/spans, inline images, and optionally headers/footers using Markdown syntax only. Report features the format cannot preserve and provide an explicit `AllowLossy` option.
  - **Done when:** synthetic and local-corpus exports pass; visual approximations have stable diagnostics; unprojectable content fails before writing by default; explicit lossy fallback is tested.

### P2 — release and later format coverage

- [ ] **T23 — Prepare the v0 support and compatibility docs.** Include feature matrix, tested UDF variants, limits, diagnostics, exporter authoring example, and fidelity/signature statements.
  - **Done when:** every claimed supported feature points to evidence and exporters can be implemented without reading parser internals.
- [x] **T24 — Add DOCX.** Use `docxgo/v2` as a pure-Go backend. Map the verified paragraph, run, table, image, page, list, and header/footer subset; return diagnostics for approximations and fail on unsupported visible content by default.
  - **Done when:** synthetic fixtures and all available local UDFs generate DOCX packages that docxgo can reopen and validate; original UDF bytes and signatures remain untouched/not transferred.
- [ ] **T25 — Revisit attachment composition and sections.** Keep inline images/objects distinct. Add multi-file attachments, related UDF composition, sections, or richer layout only when a concrete workflow and source fixture establishes the relationship semantics.
  - **Done when:** origin, ordering, ownership, cycles, recursion/aggregate limits, and exporter behavior are specified before API commitment.
- [x] **T28 — Add an unsigned UDF exporter.** Serialize the normalized model to a new UDF ZIP package. Refuse signed input unless the caller explicitly allows an unsigned derivative; omit stale verification codes and report unsupported source details.
  - **Done when:** synthetic fixtures and all available local samples parse → export → parse with identical `Document.Text()` projections, originals remain byte-identical, and generated archives contain no copied source signature.
- [x] **T29 — Review the DOCX backend dependency and license.** Pin `docxgo/v2` and document its pure-Go/no-third-party-runtime-dependency status plus its maintainers' provenance determination for the current MIT release and historical AGPL-era repository history.
  - **Done when:** the dependency version and its current license/provenance references are recorded; upgrades re-review the project's provenance audit.

### Cross-cutting validation tasks

- [ ] **T26 — Fuzz isolated layers.** Create separate fuzz targets for archive handling, XML interpretation, offset translation, and normalization. Use synthetic or independently reviewed publishable seeds only; keep private outputs in a private location and reproduce failures synthetically.
  - **Done when:** invariants cover no panic/nontermination, valid model on success, deterministic text, no source/model mutation, and no silent text duplication/omission; no private legal document can enter `testdata/fuzz` or shared logs.
- [ ] **T27 — Add regression gates per task.** Every completed task above adds its own synthetic fixtures and acceptance checks; do not defer validation to a final pass.
  - **Done when:** feature-matrix status changes to `fixture-verified` only after the task's acceptance evidence exists.

The ignored `.local-fixtures/` corpus currently contains 45 unique samples. They are for local compatibility investigation only, not tracked fixtures or fuzz seeds. The initial archive survey found two manifest shapes: 42 samples have `content.xml`, `documentproperties.xml`, and `sign.sgn`; three omit `documentproperties.xml`. XML-level coverage is not yet inventoried.

## Risks and open decisions

- UDF is a UYAP-specific format; undocumented fields and edge cases may occur in real documents.
- The reference converter may not handle all UDF structures or formatting and is not an authoritative fidelity benchmark.
- Different output formats have different capabilities; one model cannot guarantee identical visual reproduction in each exporter.
- PDF library selection depends on actual document comparisons, font licensing, and the required layout behavior.
- DOCX output quality depends on backend behavior; rerun the DOCX round-trip/validation fixtures on docxgo upgrades, and re-review its documented license provenance before changing versions.
- Decide how unknown XML fields and optional verification metadata should be represented in the stable public API.
- Choose the Go module path and finalize repository/package naming after confirming the remote repository identity.
