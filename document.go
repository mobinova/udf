// Package udf reads UYAP UDF archives into a format-neutral document model.
package udf

import (
	"context"
	"io"
	"strings"
)

// Opt retains whether a source value was present. Effective model values are
// resolved and do not use Opt.
type Opt[T any] struct {
	Val T
	Set bool
}

type DiagnosticSeverity string
type DiagnosticClass string

const (
	SeverityInfo    DiagnosticSeverity = "info"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"

	DiagnosticSourceInterpretation    DiagnosticClass = "source_interpretation"
	DiagnosticNormalization           DiagnosticClass = "normalization_recovery"
	DiagnosticVisualApproximation     DiagnosticClass = "visual_approximation"
	DiagnosticContentLoss             DiagnosticClass = "content_loss"
	DiagnosticSignatureNotTransferred DiagnosticClass = "signature_not_transferred"
)

// Diagnostic codes are stable identifiers. Message is intended for people and
// must not contain source text, document property values, or unbounded input.
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

type ParseOptions struct {
	MaxCompressedBytes   int64
	MaxEntryBytes        int64
	MaxTotalExpanded     int64
	MaxArchiveEntries    int
	MaxXMLDepth          int
	MaxXMLNodes          int
	MaxXMLTextBytes      int64
	MaxXMLAttributeBytes int64
	MaxResourcesBytes    int64
	MaxResourceBytes     int64
	MaxUnknownBytes      int64
	MaxTableGridCells    int64
	MaxDiagnostics       int
	BestEffort           bool
	RetainUnknownXML     bool
}

type Document struct {
	FormatID    string
	WebID       string
	Properties  DocumentProperties
	Page        PageLayout
	Styles      []Style
	Resources   []Resource
	Headers     []HeaderFooter
	Body        []Block
	Footers     []HeaderFooter
	Signature   SignatureInfo
	Diagnostics []Diagnostic
}

type DocumentProperties struct {
	Values map[string]string
}

type PageLayout struct {
	Width, Height                  float64 // points; zero when source format is unknown
	MediaSizeName                  string
	LeftMargin, RightMargin        float64
	TopMargin, BottomMargin        float64
	HeaderDistance, FooterDistance float64
	Orientation                    Orientation
}

type Orientation string

const (
	Portrait  Orientation = "portrait"
	Landscape Orientation = "landscape"
)

type Style struct {
	Name          string
	Description   string
	Text          TextStyle
	TextOverrides TextStyleOverrides
	Paragraph     ParagraphStyle
}

type TextStyleOverrides struct {
	FontFamily Opt[string]
	FontSize   Opt[float64]
	Bold       Opt[bool]
	Italic     Opt[bool]
	Underline  Opt[bool]
	Strike     Opt[bool]
	Foreground Opt[int32]
	Background Opt[int32]
}

type TextStyle struct {
	FontFamily string
	FontSize   float64
	Bold       bool
	Italic     bool
	Underline  bool
	Strike     bool
	Foreground Opt[int32]
	Background Opt[int32]
}

type ParagraphStyle struct {
	Alignment       Alignment
	LeftIndent      float64
	RightIndent     float64
	FirstLineIndent float64
	Hanging         float64
	SpaceAbove      float64
	SpaceBelow      float64
	LineSpacing     float64 // UDF additional spacing factor; 0.3 maps to 1.3 (312/240) line spacing.
	Tabs            []TabStop
	Numbered        bool
	Bulleted        bool
	NumberType      string
	BulletType      string
	ListID          string
	ListLevel       int
}

type Alignment string

const (
	AlignUnspecified Alignment = ""
	AlignLeft        Alignment = "left"
	AlignCenter      Alignment = "center"
	AlignRight       Alignment = "right"
	AlignJustify     Alignment = "justify"
)

type TabStop struct {
	Position  float64
	Alignment string
	Leader    string
}

type BlockKind string

const (
	BlockParagraph BlockKind = "paragraph"
	BlockTable     BlockKind = "table"
	BlockPageBreak BlockKind = "page_break"
	BlockUnknown   BlockKind = "unknown"
)

type Block struct {
	Kind      BlockKind
	Paragraph *Paragraph
	Table     *Table
	PageBreak *PageBreak
	Unknown   *UnknownBlock
}

type PageBreak struct{}

type UnknownBlock struct {
	Element string
	Text    string
	RawXML  []byte
}

type Paragraph struct {
	StyleName  string
	Properties ParagraphStyle
	Inlines    []Inline
}

type InlineKind string

const (
	InlineRun     InlineKind = "run"
	InlineTab     InlineKind = "tab"
	InlineBreak   InlineKind = "break"
	InlineField   InlineKind = "field"
	InlineImage   InlineKind = "image"
	InlineUnknown InlineKind = "unknown"
)

type Inline struct {
	Kind    InlineKind
	Run     *TextRun
	Tab     *Tab
	Break   *LineBreak
	Field   *Field
	Image   *Image
	Unknown *UnknownInline
}

type TextRun struct {
	Text  string
	Style TextStyle
}

type Tab struct{ Text string }
type LineBreak struct{ Text string }

type Field struct {
	Name, Type, Group string
	Text              string
	Style             TextStyle
}

type Image struct {
	ResourceID string
	Width      float64
	Height     float64
}

type UnknownInline struct {
	Element string
	Text    string
	RawXML  []byte
}

type Resource struct {
	ID        string
	SHA256    [32]byte
	MediaType string
	Data      []byte
	Width     int
	Height    int
}

type Table struct {
	Name         string
	ColumnCount  int
	ColumnWidths []ColumnWidth
	Rows         []TableRow
}

type WidthUnit string

const (
	UnitPoint   WidthUnit = "pt"
	UnitPercent WidthUnit = "percent"
	UnitAuto    WidthUnit = "auto"
)

type ColumnWidth struct {
	Value float64
	Unit  WidthUnit
}

type TableRow struct {
	Name   string
	Type   string
	Height float64
	Cells  []TableCell
}

type TableCell struct {
	GridRow, GridCol int
	RowSpan, ColSpan int
	VerticalAlign    string
	Background       Opt[int32]
	Border           Border
	Blocks           []Block
}

type Border struct {
	Kind  string
	Style string
	Width float64
	Color Opt[int32]
	Spec  uint32
}

type HeaderFooter struct {
	Kind      string
	StartPage int
	Blocks    []Block
}

type SignatureInfo struct {
	Present bool
	Size    uint64 // ZIP directory size, not a validation result
}

// Exporter converts a Document to one caller-owned output stream.
type Exporter interface {
	Export(ctx context.Context, doc *Document, dst io.Writer) (ExportReport, error)
}

// Text returns a deterministic body-text projection. It inserts one newline
// between blocks and preserves paragraph-internal text and explicit breaks.
func (d *Document) Text() string {
	if d == nil {
		return ""
	}
	parts := make([]string, 0, len(d.Body))
	for _, block := range d.Body {
		parts = append(parts, blockText(block))
	}
	return strings.Join(parts, "\n")
}

func blockText(block Block) string {
	switch block.Kind {
	case BlockParagraph:
		if block.Paragraph != nil {
			return block.Paragraph.Text()
		}
	case BlockTable:
		if block.Table != nil {
			return block.Table.Text()
		}
	case BlockPageBreak:
		return "\f"
	case BlockUnknown:
		if block.Unknown != nil {
			return block.Unknown.Text
		}
	}
	return ""
}

func (p *Paragraph) Text() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, inline := range p.Inlines {
		switch inline.Kind {
		case InlineRun:
			if inline.Run != nil {
				b.WriteString(inline.Run.Text)
			}
		case InlineTab:
			if inline.Tab != nil {
				b.WriteString(inline.Tab.Text)
			}
		case InlineBreak:
			if inline.Break != nil {
				b.WriteString(inline.Break.Text)
			}
		case InlineField:
			if inline.Field != nil {
				b.WriteString(inline.Field.Text)
			}
		case InlineUnknown:
			if inline.Unknown != nil {
				b.WriteString(inline.Unknown.Text)
			}
		}
	}
	return b.String()
}

func (t *Table) Text() string {
	if t == nil {
		return ""
	}
	rows := make([]string, 0, len(t.Rows))
	for _, row := range t.Rows {
		cells := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			blocks := make([]string, 0, len(cell.Blocks))
			for _, block := range cell.Blocks {
				blocks = append(blocks, blockText(block))
			}
			cells = append(cells, strings.Join(blocks, "\n"))
		}
		rows = append(rows, strings.Join(cells, "\t"))
	}
	return strings.Join(rows, "\n")
}

func (d *Document) Walk(fn func(Block) error) error {
	if d == nil || fn == nil {
		return nil
	}
	for _, block := range d.Body {
		if err := walkBlock(block, fn); err != nil {
			return err
		}
	}
	return nil
}

func walkBlock(block Block, fn func(Block) error) error {
	if err := fn(block); err != nil {
		return err
	}
	if block.Kind == BlockTable && block.Table != nil {
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				for _, nested := range cell.Blocks {
					if err := walkBlock(nested, fn); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
