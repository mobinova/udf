# Using UDF from an AI agent

This tool-agnostic guide is for Codex, Claude Code, Antigravity, Gemini, and other agents that can run local commands. Agent-specific project instructions belong in files such as `AGENTS.md`, `CLAUDE.md`, or the tool's own rules file; link to this guide rather than duplicating its command details.

## Install the CLI

### Any OS with Go

```sh
go install github.com/mobinova/udf/cmd/udf@latest
udf version
```

Make sure Go's `bin` directory is on `PATH`. On Windows, the default is usually `%USERPROFILE%\go\bin`; on macOS and Linux it is usually `$HOME/go/bin`.

### macOS and Linux with Homebrew

```sh
brew tap mobinova/udf
brew trust mobinova/udf
brew install mobinova/udf/udf
```

Homebrew asks users to trust third-party taps before loading their formula. The tap follows stable releases automatically.

### Prebuilt release archive

Download the matching archive from <https://github.com/mobinova/udf/releases/latest>:

- Windows: ZIP containing `udf.exe`
- macOS: `darwin_arm64` for Apple Silicon or `darwin_amd64` for Intel
- Linux: `linux_amd64` or `linux_arm64`

Verify the archive against that release's `checksums.txt`, extract the executable, and put it on `PATH`.

## Choose the input format explicitly

The CLI does not guess the format from a filename. Pass `--input-format udf`, `markdown`, or `docx` when the input is not UDF. UDF is the default.

```sh
udf inspect --json ./document.udf
udf inspect --json --input-format docx ./document.docx
udf inspect --json --input-format markdown ./notes.md
```

`inspect --json` is the recommended first command for an agent. It reports format, structural counts, signature presence, and diagnostic codes. It does **not** include body text or document property values.

## Read and convert content

Body text is emitted only when explicitly requested:

```sh
udf text ./document.udf
udf text --input-format docx ./document.docx
```

Convert between supported formats with an explicit input and output format:

```sh
udf export --input-format udf --format markdown \
  --allow-lossy --output ./out.md --report ./out.report.json ./document.udf

udf export --input-format markdown --format docx \
  --allow-lossy --output ./out.docx --report ./out.report.json ./notes.md

udf export --input-format docx --format markdown \
  --allow-lossy --output ./out.md --report ./out.report.json ./document.docx
```

Supported output formats are `text`, `markdown`, `docx`, and `udf`. Without `--allow-lossy`, Markdown and DOCX exports reject unsupported visible content instead of silently omitting it. Inspect the JSON report for stable diagnostic codes and locations.

For UDF output, use `--allow-unsigned` only when an unsigned derivative is acceptable:

```sh
udf export --format udf --allow-unsigned --allow-lossy \
  --output ./unsigned-copy.udf --report ./udf.report.json ./signed-source.udf
```

The output is a new unsigned UDF archive. The importer does not verify signatures, and no exporter carries a source signature into the output.

## Agent handling rules

- Keep `.udf` input local. Do not upload the file, extracted text, images, or reports to a remote service.
- Begin with `udf inspect --json`; use `udf text` only when the task requires the content.
- Write conversions to a new output path. The CLI refuses to overwrite the input and commits file output only after a successful export.
- Keep reports separate from document data. `--output -` sends document bytes to stdout; diagnostic output goes to stderr unless `--report` is specified.
- Review reported compromises before describing an export as faithful. Markdown and DOCX cannot preserve every page-layout feature.
- Preserve the original UDF. An exported DOCX or UDF is a convenience derivative and does not retain or validate its electronic signature.
- Importers do not fetch Markdown image URLs or DOCX external links. DOCX images embedded in the package are imported locally.

For parser limits, public APIs, and format support details, see [README.md](README.md). Repository contribution and testing rules are in [AGENTS.md](AGENTS.md).
