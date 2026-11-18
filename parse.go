package udf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"archive/zip"
)

var (
	ErrInvalidArchive = errors.New("udf: invalid archive")
	ErrLimitExceeded  = errors.New("udf: resource limit exceeded")
	ErrInvalidXML     = errors.New("udf: invalid XML")
	ErrInvalidOffset  = errors.New("udf: invalid text offset")
	ErrUnsupported    = errors.New("udf: unsupported document structure")
	ErrInvalidModel   = errors.New("udf: invalid document model")
	ErrInputRead      = errors.New("udf: input read failed")
)

type parseLimits struct {
	compressed, entry, expanded, text, attrs, resources, resource, unknown, gridCells int64
	entries, depth, nodes, diagnostics                                                int
}

func limits(opts ParseOptions) parseLimits {
	set64 := func(v, d int64) int64 {
		if v > 0 {
			return v
		}
		return d
	}
	set := func(v, d int) int {
		if v > 0 {
			return v
		}
		return d
	}
	return parseLimits{
		compressed:  set64(opts.MaxCompressedBytes, 64<<20),
		entry:       set64(opts.MaxEntryBytes, 16<<20),
		expanded:    set64(opts.MaxTotalExpanded, 64<<20),
		entries:     set(opts.MaxArchiveEntries, 256),
		depth:       set(opts.MaxXMLDepth, 64),
		nodes:       set(opts.MaxXMLNodes, 200000),
		text:        set64(opts.MaxXMLTextBytes, 16<<20),
		attrs:       set64(opts.MaxXMLAttributeBytes, 16<<20),
		resources:   set64(opts.MaxResourcesBytes, 16<<20),
		resource:    set64(opts.MaxResourceBytes, 8<<20),
		unknown:     set64(opts.MaxUnknownBytes, 1<<20),
		gridCells:   set64(opts.MaxTableGridCells, 1<<20),
		diagnostics: set(opts.MaxDiagnostics, 512),
	}
}

func Parse(r io.Reader) (*Document, error) { return ParseWithOptions(r, ParseOptions{}) }

func ParseWithOptions(r io.Reader, opts ParseOptions) (*Document, error) {
	if r == nil {
		return nil, fmt.Errorf("udf: nil reader")
	}
	lim := limits(opts)
	data, err := io.ReadAll(io.LimitReader(r, lim.compressed+1))
	if err != nil {
		return nil, fmt.Errorf("udf: read archive: %w", ErrInputRead)
	}
	if int64(len(data)) > lim.compressed {
		return nil, ErrLimitExceeded
	}
	return ParseReaderAtWithOptions(bytes.NewReader(data), int64(len(data)), opts)
}

func ParseBytes(data []byte) (*Document, error) { return ParseBytesWithOptions(data, ParseOptions{}) }

func ParseBytesWithOptions(data []byte, opts ParseOptions) (*Document, error) {
	return ParseReaderAtWithOptions(bytes.NewReader(data), int64(len(data)), opts)
}

func ParseFile(name string) (*Document, error) { return ParseFileWithOptions(name, ParseOptions{}) }

func ParseFileWithOptions(name string, opts ParseOptions) (*Document, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("udf: open input: %w", ErrInputRead)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("udf: stat input: %w", ErrInputRead)
	}
	return ParseReaderAtWithOptions(f, info.Size(), opts)
}

func ParseReaderAt(r io.ReaderAt, size int64) (*Document, error) {
	return ParseReaderAtWithOptions(r, size, ParseOptions{})
}

func ParseReaderAtWithOptions(r io.ReaderAt, size int64, opts ParseOptions) (*Document, error) {
	if r == nil || size < 0 {
		return nil, fmt.Errorf("udf: invalid random-access input")
	}
	lim := limits(opts)
	if size > lim.compressed {
		return nil, ErrLimitExceeded
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("udf: %w", ErrInvalidArchive)
	}
	if len(zr.File) > lim.entries {
		return nil, ErrLimitExceeded
	}

	seen := make(map[string]bool, len(zr.File))
	var content, properties []byte
	var signature SignatureInfo
	var expanded int64
	for _, file := range zr.File {
		name := file.Name
		if unsafeEntryName(name) || seen[name] {
			return nil, ErrInvalidArchive
		}
		seen[name] = true
		if file.Flags&1 != 0 {
			return nil, ErrInvalidArchive
		}
		if file.UncompressedSize64 > uint64(lim.entry) || file.UncompressedSize64 > uint64(lim.expanded) {
			return nil, ErrLimitExceeded
		}
		if file.Name == "sign.sgn" {
			signature.Present = true
			signature.Size = file.UncompressedSize64
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("udf: open archive entry: %w", ErrInvalidArchive)
		}
		remaining := lim.expanded - expanded
		entryLimit := lim.entry
		if remaining < entryLimit {
			entryLimit = remaining
		}
		data, readErr := readEntry(reader, entryLimit)
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("udf: close archive entry: %w", ErrInvalidArchive)
		}
		expanded += int64(len(data))
		switch name {
		case "content.xml":
			content = data
		case "documentproperties.xml":
			properties = data
		}
	}
	if content == nil {
		return nil, fmt.Errorf("udf: missing content.xml: %w", ErrInvalidArchive)
	}
	limitsXML := xmlLimits{depth: lim.depth, nodes: lim.nodes, textBytes: lim.text, attrBytes: lim.attrs}
	root, err := decodeTree(content, limitsXML)
	if err != nil {
		return nil, err
	}
	doc, err := buildDocument(root, opts, lim)
	if err != nil {
		return nil, err
	}
	doc.Signature = signature
	if len(properties) > 0 {
		if err := parseDocumentProperties(properties, doc); err != nil {
			return nil, err
		}
	}
	if err := Validate(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func readEntry(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, ErrLimitExceeded
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("udf: read archive entry: %w", ErrInvalidArchive)
	}
	if int64(len(b)) > limit {
		return nil, ErrLimitExceeded
	}
	return b, nil
}

func unsafeEntryName(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return true
	}
	clean := path.Clean(name)
	return clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "/")
}

func parseDocumentProperties(data []byte, doc *Document) error {
	root, err := decodeTree(data, xmlLimits{depth: 16, nodes: 10000, textBytes: 1 << 20, attrBytes: 1 << 20})
	if err != nil {
		return err
	}
	if root.Name.Local != "properties" {
		return fmt.Errorf("udf: invalid document properties: %w", ErrInvalidXML)
	}
	if doc.Properties.Values == nil {
		doc.Properties.Values = make(map[string]string)
	}
	for _, child := range root.Children {
		if child.Name.Local != "entry" {
			continue
		}
		key := child.Attr["key"]
		if key != "" {
			doc.Properties.Values[key] = child.Text.String()
		}
	}
	return nil
}

// safeInputError intentionally maps parser input errors to stable public errors.
func safeInputError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("udf: %s: %w", stage, err)
}
