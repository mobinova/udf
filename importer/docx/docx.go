// Package docx imports WordprocessingML documents into the normalized UDF
// document model.
package docximporter

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"path"
	"strconv"
	"strings"

	word "github.com/mmonterroca/docxgo/v2"
	"github.com/mmonterroca/docxgo/v2/domain"
	udf "github.com/mobinova/udf"
)

const (
	defaultMaxInputBytes = 64 << 20
	defaultMaxEntryBytes = 64 << 20
	defaultMaxExpanded   = 256 << 20
	defaultMaxEntries    = 1024
	defaultMaxImageBytes = 32 << 20
	maxImportDiagnostics = 512
)

// Options bounds the ZIP package and image resources read from a DOCX file.
// Non-positive limits use the defaults.
type Options struct {
	MaxInputBytes     int64
	MaxEntryBytes     int64
	MaxTotalExpanded  int64
	MaxImageBytes     int64
	MaxArchiveEntries int
}

// Importer reads DOCX documents using docxgo and maps representable content
// into the shared model. It does not fetch linked resources.
type Importer struct {
	Options Options
}

var _ udf.Importer = Importer{}

type importState struct {
	ctx                  context.Context
	options              Options
	doc                  *udf.Document
	resources            map[[32]byte]string
	diagnostics          int
	paragraphMetadata    []paragraphMetadata
	paragraphIndex       int
	useParagraphMetadata bool
}

type paragraphMetadata struct {
	styleName   string
	tabs        []udf.TabStop
	invalidTabs int
}

func (i Importer) Import(ctx context.Context, src io.Reader) (*udf.Document, error) {
	if src == nil {
		return nil, fmt.Errorf("DOCX import: nil reader")
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	limits := normalizedLimits(i.Options)
	data, err := io.ReadAll(io.LimitReader(src, limits.maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("DOCX import: read input: %w", udf.ErrInputRead)
	}
	if int64(len(data)) > limits.maxInputBytes {
		return nil, udf.ErrLimitExceeded
	}
	if err := preflightArchive(data, limits); err != nil {
		return nil, err
	}
	paragraphMetadata, err := readParagraphMetadata(data, limits.maxEntryBytes)
	if err != nil {
		return nil, err
	}
	parsed, err := word.OpenDocumentFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("DOCX import: parse package: %w", udf.ErrInvalidArchive)
	}
	state := &importState{
		ctx:               ctx,
		options:           i.Options,
		doc:               &udf.Document{FormatID: "docx", Properties: udf.DocumentProperties{Values: make(map[string]string)}},
		resources:         make(map[[32]byte]string),
		paragraphMetadata: paragraphMetadata,
	}
	state.importMetadata(parsed)
	state.importSections(parsed)
	if countDocxParagraphs(parsed.Blocks()) != len(paragraphMetadata) {
		state.add("docx.paragraph_metadata_alignment_unavailable", udf.DiagnosticNormalization, "body", "Some DOCX paragraphs are outside the normalized block tree; source style and tab metadata are omitted to avoid misalignment")
		state.paragraphMetadata = nil
	}
	state.useParagraphMetadata = true
	for _, block := range parsed.Blocks() {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		state.doc.Body = append(state.doc.Body, state.importBlock(block, "body")...)
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := udf.Validate(state.doc); err != nil {
		return nil, fmt.Errorf("DOCX import produced invalid model: %w", err)
	}
	return state.doc, nil
}

func Import(ctx context.Context, src io.Reader) (*udf.Document, error) {
	return (Importer{}).Import(ctx, src)
}

func ImportWithOptions(ctx context.Context, src io.Reader, options Options) (*udf.Document, error) {
	return (Importer{Options: options}).Import(ctx, src)
}

type docxLimits struct {
	maxInputBytes, maxEntryBytes, maxExpanded, maxImageBytes int64
	maxEntries                                               int
}

func normalizedLimits(options Options) docxLimits {
	set64 := func(value, fallback int64) int64 {
		if value > 0 {
			return value
		}
		return fallback
	}
	set := func(value, fallback int) int {
		if value > 0 {
			return value
		}
		return fallback
	}
	return docxLimits{
		maxInputBytes: set64(options.MaxInputBytes, defaultMaxInputBytes),
		maxEntryBytes: set64(options.MaxEntryBytes, defaultMaxEntryBytes),
		maxExpanded:   set64(options.MaxTotalExpanded, defaultMaxExpanded),
		maxImageBytes: set64(options.MaxImageBytes, defaultMaxImageBytes),
		maxEntries:    set(options.MaxArchiveEntries, defaultMaxEntries),
	}
}

func preflightArchive(data []byte, limits docxLimits) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("DOCX import: invalid ZIP package: %w", udf.ErrInvalidArchive)
	}
	if len(archive.File) > limits.maxEntries {
		return udf.ErrLimitExceeded
	}
	seen := make(map[string]bool, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		clean := path.Clean(name)
		if path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || seen[clean] {
			return fmt.Errorf("DOCX import: unsafe or duplicate ZIP entry: %w", udf.ErrInvalidArchive)
		}
		seen[clean] = true
		if entry.UncompressedSize64 > uint64(limits.maxEntryBytes) || entry.UncompressedSize64 > uint64(limits.maxExpanded) {
			return udf.ErrLimitExceeded
		}
		size := int64(entry.UncompressedSize64)
		if total > limits.maxExpanded-size {
			return udf.ErrLimitExceeded
		}
		total += size
		if strings.HasPrefix(clean, "word/media/") && size > limits.maxImageBytes {
			return udf.ErrLimitExceeded
		}
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("DOCX import: open ZIP entry: %w", udf.ErrInvalidArchive)
		}
		read, readErr := io.Copy(io.Discard, io.LimitReader(reader, limits.maxEntryBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("DOCX import: read ZIP entry: %w", udf.ErrInvalidArchive)
		}
		if read > limits.maxEntryBytes {
			return udf.ErrLimitExceeded
		}
	}
	return nil
}

func readParagraphMetadata(data []byte, maxEntryBytes int64) ([]paragraphMetadata, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("DOCX import: invalid ZIP package: %w", udf.ErrInvalidArchive)
	}
	var documentXML []byte
	for _, entry := range archive.File {
		if entry.Name != "word/document.xml" {
			continue
		}
		if entry.UncompressedSize64 > uint64(maxEntryBytes) {
			return nil, udf.ErrLimitExceeded
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("DOCX import: open document XML: %w", udf.ErrInvalidArchive)
		}
		documentXML, err = io.ReadAll(io.LimitReader(reader, maxEntryBytes+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			return nil, fmt.Errorf("DOCX import: read document XML: %w", udf.ErrInvalidArchive)
		}
		if int64(len(documentXML)) > maxEntryBytes {
			return nil, udf.ErrLimitExceeded
		}
		break
	}
	if len(documentXML) == 0 {
		return nil, fmt.Errorf("DOCX import: missing word/document.xml: %w", udf.ErrInvalidArchive)
	}
	decoder := xml.NewDecoder(bytes.NewReader(documentXML))
	decoder.Strict = true
	var paragraphs []paragraphMetadata
	var current *paragraphMetadata
	inParagraphProperties := false
	inTabs := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("DOCX import: malformed document XML: %w", udf.ErrInvalidXML)
		}
		switch node := token.(type) {
		case xml.StartElement:
			switch node.Name.Local {
			case "p":
				paragraphs = append(paragraphs, paragraphMetadata{})
				current = &paragraphs[len(paragraphs)-1]
				inParagraphProperties = false
				inTabs = false
			case "pPr":
				if current != nil {
					inParagraphProperties = true
				}
			case "pStyle":
				if current != nil && inParagraphProperties {
					current.styleName = xmlAttribute(node.Attr, "val")
				}
			case "tabs":
				if current != nil && inParagraphProperties {
					inTabs = true
				}
			case "tab":
				if current != nil && inTabs {
					stop, ok := parseTabStop(node.Attr)
					if ok {
						current.tabs = append(current.tabs, stop)
					} else {
						current.invalidTabs++
					}
				}
			}
		case xml.EndElement:
			switch node.Name.Local {
			case "p":
				current = nil
				inParagraphProperties = false
				inTabs = false
			case "pPr":
				inParagraphProperties = false
			case "tabs":
				inTabs = false
			}
		}
	}
	return paragraphs, nil
}

func xmlAttribute(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func parseTabStop(attrs []xml.Attr) (udf.TabStop, bool) {
	position, err := strconv.ParseFloat(xmlAttribute(attrs, "pos"), 64)
	if err != nil || !finite(position) || position < 0 {
		return udf.TabStop{}, false
	}
	stop := udf.TabStop{Position: position / 20, Alignment: xmlAttribute(attrs, "val")}
	switch xmlAttribute(attrs, "leader") {
	case "dot":
		stop.Leader = "dot"
	case "hyphen":
		stop.Leader = "dash"
	case "underscore", "heavy", "middleDot":
		stop.Leader = xmlAttribute(attrs, "leader")
	}
	switch stop.Alignment {
	case "left", "center", "right", "decimal":
		return stop, true
	default:
		return udf.TabStop{}, false
	}
}

func (s *importState) importMetadata(doc domain.Document) {
	metadata := doc.Metadata()
	if metadata == nil {
		return
	}
	for key, value := range map[string]string{
		"title": metadata.Title, "subject": metadata.Subject, "author": metadata.Creator,
		"keywords": strings.Join(metadata.Keywords, ", "), "description": metadata.Description,
		"created": metadata.Created, "modified": metadata.Modified,
	} {
		if value != "" {
			s.doc.Properties.Values[key] = value
		}
	}
}

func (s *importState) importSections(doc domain.Document) {
	sections := doc.Sections()
	if len(sections) == 0 {
		s.add("layout.section_missing", udf.DiagnosticSourceInterpretation, "sections", "DOCX section layout is unavailable")
		return
	}
	if len(sections) > 1 {
		s.add("layout.sections_flattened", udf.DiagnosticVisualApproximation, "sections", "Only the first DOCX section layout is represented by the shared model")
	}
	section := sections[0]
	pageSize := section.PageSize()
	s.doc.Page.Width = twipsToPoints(pageSize.Width)
	s.doc.Page.Height = twipsToPoints(pageSize.Height)
	margins := section.Margins()
	s.doc.Page.LeftMargin = twipsToPoints(margins.Left)
	s.doc.Page.RightMargin = twipsToPoints(margins.Right)
	s.doc.Page.TopMargin = twipsToPoints(margins.Top)
	s.doc.Page.BottomMargin = twipsToPoints(margins.Bottom)
	s.doc.Page.HeaderDistance = twipsToPoints(margins.Header)
	s.doc.Page.FooterDistance = twipsToPoints(margins.Footer)
	if section.Orientation() == domain.OrientationLandscape {
		s.doc.Page.Orientation = udf.Landscape
	} else {
		s.doc.Page.Orientation = udf.Portrait
	}
	if section.Columns() > 1 {
		s.add("layout.columns_not_supported", udf.DiagnosticVisualApproximation, "page", "Multi-column section layout is not represented")
	}

	for si, current := range sections {
		headerSource, ok := current.(interface {
			HeadersAll() map[domain.HeaderType]domain.Header
		})
		if ok {
			for _, variant := range []domain.HeaderType{domain.HeaderDefault, domain.HeaderFirst, domain.HeaderEven} {
				if header := headerSource.HeadersAll()[variant]; header != nil && len(header.Blocks()) > 0 {
					startPage := 1
					if variant == domain.HeaderFirst {
						startPage = 1
					}
					sectionName := fmt.Sprintf("headers[%d]", si)
					if variant != domain.HeaderDefault || si > 0 {
						s.add("layout.header_scope_flattened", udf.DiagnosticVisualApproximation, sectionName, "DOCX header variant or section scope is flattened")
					}
					s.doc.Headers = append(s.doc.Headers, udf.HeaderFooter{Kind: "header", StartPage: startPage, Blocks: s.importDocxBlocks(header.Blocks(), sectionName)})
				}
			}
		}
		footerSource, ok := current.(interface {
			FootersAll() map[domain.FooterType]domain.Footer
		})
		if ok {
			for _, variant := range []domain.FooterType{domain.FooterDefault, domain.FooterFirst, domain.FooterEven} {
				if footer := footerSource.FootersAll()[variant]; footer != nil && len(footer.Blocks()) > 0 {
					if variant != domain.FooterDefault || si > 0 {
						s.add("layout.footer_scope_flattened", udf.DiagnosticVisualApproximation, fmt.Sprintf("footers[%d]", si), "DOCX footer variant or section scope is flattened")
					}
					s.doc.Footers = append(s.doc.Footers, udf.HeaderFooter{Kind: "footer", Blocks: s.importDocxBlocks(footer.Blocks(), fmt.Sprintf("footers[%d]", si))})
				}
			}
		}
	}
}

func (s *importState) importDocxBlocks(blocks []domain.Block, location string) []udf.Block {
	var result []udf.Block
	for index, block := range blocks {
		result = append(result, s.importBlock(block, fmt.Sprintf("%s[%d]", location, index))...)
	}
	return result
}

func (s *importState) importBlock(block domain.Block, location string) []udf.Block {
	if block.Paragraph != nil {
		return []udf.Block{{Kind: udf.BlockParagraph, Paragraph: s.importParagraph(block.Paragraph, location)}}
	}
	if block.Table != nil {
		return []udf.Block{{Kind: udf.BlockTable, Table: s.importTable(block.Table, location)}}
	}
	if block.SectionBreak != nil {
		s.add("layout.section_break_flattened", udf.DiagnosticVisualApproximation, location, "Section break and following layout are not represented by the shared model")
		return []udf.Block{{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "docx_section_break"}}}
	}
	s.add("source.unknown_docx_block", udf.DiagnosticSourceInterpretation, location, "DOCX block type is not exposed by the reader library")
	return []udf.Block{{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "docx_unknown_block"}}}
}

func (s *importState) importParagraph(source domain.Paragraph, location string) *udf.Paragraph {
	paragraph := &udf.Paragraph{}
	if style := source.Style(); style != nil {
		paragraph.StyleName = style.ID()
		if paragraph.StyleName == "" {
			paragraph.StyleName = style.Name()
		}
	}
	if s.useParagraphMetadata && s.paragraphIndex < len(s.paragraphMetadata) {
		metadata := s.paragraphMetadata[s.paragraphIndex]
		s.paragraphIndex++
		if paragraph.StyleName == "" {
			paragraph.StyleName = metadata.styleName
		}
		paragraph.Properties.Tabs = append(paragraph.Properties.Tabs, metadata.tabs...)
		if metadata.invalidTabs > 0 {
			s.add("paragraph.tab_stop_approximated", udf.DiagnosticVisualApproximation, location, "One or more DOCX tab stops have an unsupported alignment or position")
		}
	}
	props := &paragraph.Properties
	switch source.Alignment() {
	case domain.AlignmentCenter:
		props.Alignment = udf.AlignCenter
	case domain.AlignmentRight:
		props.Alignment = udf.AlignRight
	case domain.AlignmentJustify:
		props.Alignment = udf.AlignJustify
	default:
		props.Alignment = udf.AlignLeft
	}
	indent := source.Indent()
	props.LeftIndent = twipsToPoints(indent.Left)
	props.RightIndent = twipsToPoints(indent.Right)
	props.FirstLineIndent = twipsToPoints(indent.FirstLine)
	props.Hanging = twipsToPoints(indent.Hanging)
	props.SpaceAbove = twipsToPoints(source.SpacingBefore())
	props.SpaceBelow = twipsToPoints(source.SpacingAfter())
	lineSpacing := source.LineSpacing()
	if lineSpacing.Value > 0 {
		props.LineSpacing = float64(lineSpacing.Value)/240 - 1
		if lineSpacing.Rule != domain.LineSpacingAuto {
			s.add("layout.line_spacing_approximated", udf.DiagnosticVisualApproximation, location, "Exact or minimum DOCX line spacing is approximated as a multiplier")
		}
	}
	if numbering, ok := source.Numbering(); ok {
		props.Numbered = true
		props.NumberType = "NUMBER_TYPE_NUMBER_DOT"
		props.ListID = fmt.Sprintf("docx-num-%d", numbering.ID)
		props.ListLevel = numbering.Level
		s.add("list.numbering_style_unresolved", udf.DiagnosticVisualApproximation, location, "DOCX numbering reference is preserved as a generic ordered list")
	}
	for runIndex, run := range source.Runs() {
		if run == nil {
			continue
		}
		style := textStyle(run)
		text := run.Text()
		fields := run.Fields()
		if len(fields) > 0 {
			for _, field := range fields {
				fieldText := field.Result()
				if fieldText == "" {
					fieldText = text
				}
				group := ""
				if property, ok := field.(interface{ GetProperty(string) (string, bool) }); ok {
					group, _ = property.GetProperty("url")
				}
				paragraph.Inlines = append(paragraph.Inlines, udf.Inline{Kind: udf.InlineField, Field: &udf.Field{
					Name: field.Code(), Type: fmt.Sprint(field.Type()), Group: group, Text: fieldText, Style: style,
				}})
				if group != "" {
					s.add("link.target_not_represented", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.runs[%d]", location, runIndex), "Hyperlink target is not represented by the shared model")
				}
				if field.Result() == "" {
					s.add("field.cached_value_missing", udf.DiagnosticSourceInterpretation, fmt.Sprintf("%s.runs[%d]", location, runIndex), "DOCX field has no cached visible result")
				}
			}
		} else if text != "" {
			paragraph.Inlines = appendText(paragraph.Inlines, text, style)
		}
		if img := run.Image(); img != nil {
			resourceID, width, height := s.importImage(img, fmt.Sprintf("%s.runs[%d]", location, runIndex))
			if resourceID != "" {
				paragraph.Inlines = append(paragraph.Inlines, udf.Inline{Kind: udf.InlineImage, Image: &udf.Image{ResourceID: resourceID, Width: width, Height: height}})
			}
		}
		for _, lineBreak := range run.Breaks() {
			if lineBreak == domain.BreakTypePage {
				s.add("layout.inline_page_break_approximated", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.runs[%d]", location, runIndex), "Page break inside a run is represented as a line break")
			}
			paragraph.Inlines = append(paragraph.Inlines, udf.Inline{Kind: udf.InlineBreak, Break: &udf.LineBreak{Text: "\n"}})
		}
	}
	return paragraph
}

func countDocxParagraphs(blocks []domain.Block) int {
	count := 0
	for _, block := range blocks {
		if block.Paragraph != nil {
			count++
		}
		if block.Table != nil {
			for _, row := range block.Table.Rows() {
				for _, cell := range row.Cells() {
					count += len(cell.Paragraphs())
					for _, nested := range cell.Tables() {
						count += countDocxParagraphs([]domain.Block{{Table: nested}})
					}
				}
			}
		}
	}
	return count
}

func textStyle(run domain.Run) udf.TextStyle {
	font := run.Font()
	style := udf.TextStyle{
		FontFamily: font.Name,
		FontSize:   float64(run.Size()) / 2,
		Bold:       run.Bold(),
		Italic:     run.Italic(),
		Strike:     run.Strike(),
		Underline:  run.Underline() != domain.UnderlineNone,
	}
	color := run.Color()
	if color != domain.ColorBlack {
		style.Foreground = udf.Opt[int32]{Set: true, Val: int32(color.R)<<16 | int32(color.G)<<8 | int32(color.B)}
	}
	if run.Highlight() != domain.HighlightNone {
		style.Background = udf.Opt[int32]{Set: true, Val: int32(highlightColor(run.Highlight()))}
	}
	return style
}

func (s *importState) importImage(source domain.Image, location string) (string, float64, float64) {
	data := source.Data()
	if len(data) == 0 {
		s.add("resource.image_data_missing", udf.DiagnosticContentLoss, location, "DOCX image has no embedded resource bytes")
		return "", 0, 0
	}
	limits := normalizedLimits(s.options)
	if int64(len(data)) > limits.maxImageBytes {
		s.add("resource.image_too_large", udf.DiagnosticContentLoss, location, "DOCX image exceeds the configured image limit")
		return "", 0, 0
	}
	hash := sha256.Sum256(data)
	if id, ok := s.resources[hash]; ok {
		size := source.Size()
		return id, emuToPoints(size.WidthEMU), emuToPoints(size.HeightEMU)
	}
	mediaType := mediaTypeForImage(source.Format())
	if mediaType == "" {
		s.add("resource.image_format_unknown", udf.DiagnosticContentLoss, location, "DOCX image format is not recognized")
		return "", 0, 0
	}
	size := source.Size()
	width, height := emuToPoints(size.WidthEMU), emuToPoints(size.HeightEMU)
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		if size.WidthPx <= 0 {
			size.WidthPx = config.Width
		}
		if size.HeightPx <= 0 {
			size.HeightPx = config.Height
		}
	}
	id := fmt.Sprintf("docx-image-%x", hash[:8])
	s.resources[hash] = id
	s.doc.Resources = append(s.doc.Resources, udf.Resource{ID: id, SHA256: hash, MediaType: mediaType, Data: append([]byte(nil), data...), Width: size.WidthPx, Height: size.HeightPx})
	return id, width, height
}

func (s *importState) importTable(source domain.Table, location string) *udf.Table {
	table := &udf.Table{Name: source.Style().Name, ColumnCount: source.ColumnCount()}
	if table.ColumnCount < 0 {
		table.ColumnCount = 0
	}
	if table.ColumnCount > 0 {
		widths := make([]float64, table.ColumnCount)
		for _, row := range source.Rows() {
			col := 0
			for _, cell := range row.Cells() {
				span := cell.GridSpan()
				if span < 1 {
					span = 1
				}
				width := twipsToPoints(cell.Width()) / float64(span)
				for offset := 0; offset < span && col+offset < len(widths); offset++ {
					if widths[col+offset] == 0 && width > 0 {
						widths[col+offset] = width
					}
				}
				col += span
			}
		}
		for _, width := range widths {
			if width > 0 {
				table.ColumnWidths = append(table.ColumnWidths, udf.ColumnWidth{Value: width, Unit: udf.UnitPoint})
			} else {
				table.ColumnWidths = append(table.ColumnWidths, udf.ColumnWidth{Unit: udf.UnitAuto})
			}
		}
		tableWidth := source.Width()
		if tableWidth.Value > 0 && (tableWidth.Type == domain.WidthDXA || tableWidth.Type == domain.WidthPct) {
			value := twipsToPoints(tableWidth.Value)
			unit := udf.UnitPoint
			if tableWidth.Type == domain.WidthPct {
				value = float64(tableWidth.Value) / 50
				unit = udf.UnitPercent
			}
			if tableWidth.Type == domain.WidthDXA {
				allAuto := true
				for _, width := range widths {
					if width > 0 {
						allAuto = false
						break
					}
				}
				if allAuto {
					for col := range table.ColumnWidths {
						table.ColumnWidths[col] = udf.ColumnWidth{Value: value / float64(table.ColumnCount), Unit: udf.UnitPoint}
					}
				}
			} else if unit == udf.UnitPercent {
				for col := range table.ColumnWidths {
					if table.ColumnWidths[col].Unit == udf.UnitAuto {
						table.ColumnWidths[col] = udf.ColumnWidth{Value: value / float64(table.ColumnCount), Unit: udf.UnitPercent}
					}
				}
			}
		}
	}
	rows := source.Rows()
	active := make(map[int]mergeOrigin)
	for ri, row := range rows {
		modelRow := udf.TableRow{Type: "dataRow", Height: twipsToPoints(row.Height())}
		if row.RepeatAsHeader() {
			modelRow.Type = "headerRow"
		}
		nextActive := make(map[int]mergeOrigin)
		updated := make(map[mergeOrigin]bool)
		col := 0
		for ci, cell := range row.Cells() {
			span := cell.GridSpan()
			if span < 1 {
				span = 1
			}
			if cell.VMerge() == domain.VMergeContinue {
				origin, ok := active[col]
				if ok {
					if !updated[origin] {
						table.Rows[origin.row].Cells[origin.cell].RowSpan++
						updated[origin] = true
					}
					for offset := 0; offset < span; offset++ {
						nextActive[col+offset] = origin
					}
				} else {
					s.add("table.vertical_merge_orphan", udf.DiagnosticNormalization, fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci), "Vertical merge continuation has no matching start cell")
				}
				col += span
				continue
			}
			cellLocation := fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci)
			modelCell := udf.TableCell{
				GridRow: ri, GridCol: col, RowSpan: 1, ColSpan: span,
				VerticalAlign: verticalAlignment(cell.VerticalAlignment()),
				Blocks:        s.importCellBlocks(cell, cellLocation),
			}
			modelCell.Border = borderFromTable(cell.Borders())
			if shading := cell.Shading(); shading != domain.ColorBlack {
				modelCell.Background = udf.Opt[int32]{Set: true, Val: int32(shading.R)<<16 | int32(shading.G)<<8 | int32(shading.B)}
			}
			modelRow.Cells = append(modelRow.Cells, modelCell)
			if cell.VMerge() == domain.VMergeRestart {
				origin := mergeOrigin{row: ri, cell: len(modelRow.Cells) - 1}
				for offset := 0; offset < span; offset++ {
					nextActive[col+offset] = origin
				}
			}
			col += span
		}
		if row.CantSplit() {
			s.add("table.row_keep_together_omitted", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.rows[%d]", location, ri), "DOCX row keep-together behavior is not represented")
		}
		active = nextActive
		table.Rows = append(table.Rows, modelRow)
	}
	return table
}

type mergeOrigin struct{ row, cell int }

func (s *importState) importCellBlocks(cell domain.TableCell, location string) []udf.Block {
	var blocks []udf.Block
	paragraphs := cell.Paragraphs()
	tables := cell.Tables()
	if len(paragraphs) > 0 && len(tables) > 0 {
		s.add("table.nested_block_order_approximated", udf.DiagnosticVisualApproximation, location, "Paragraphs and nested tables are separated by the DOCX reader API")
	}
	for i, paragraph := range paragraphs {
		blocks = append(blocks, udf.Block{Kind: udf.BlockParagraph, Paragraph: s.importParagraph(paragraph, fmt.Sprintf("%s.paragraphs[%d]", location, i))})
	}
	for i, table := range tables {
		blocks = append(blocks, udf.Block{Kind: udf.BlockTable, Table: s.importTable(table, fmt.Sprintf("%s.tables[%d]", location, i))})
	}
	return blocks
}

func (s *importState) add(code string, class udf.DiagnosticClass, location, message string) {
	if s.diagnostics >= maxImportDiagnostics {
		if len(s.doc.Diagnostics) > 0 && s.doc.Diagnostics[len(s.doc.Diagnostics)-1].Code == "diagnostics.truncated" {
			return
		}
		s.doc.Diagnostics[len(s.doc.Diagnostics)-1] = udf.Diagnostic{Code: "diagnostics.truncated", Severity: udf.SeverityWarning, Class: udf.DiagnosticNormalization, Location: "document", Message: "Additional import diagnostics were omitted by the limit"}
		return
	}
	s.doc.Diagnostics = append(s.doc.Diagnostics, udf.Diagnostic{Code: code, Severity: udf.SeverityWarning, Class: class, Location: location, Message: message})
	s.diagnostics++
}

func appendText(inlines []udf.Inline, text string, style udf.TextStyle) []udf.Inline {
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		inlines = append(inlines, udf.Inline{Kind: udf.InlineRun, Run: &udf.TextRun{Text: run.String(), Style: style}})
		run.Reset()
	}
	for _, char := range text {
		switch char {
		case '\t':
			flush()
			inlines = append(inlines, udf.Inline{Kind: udf.InlineTab, Tab: &udf.Tab{Text: "\t"}})
		case '\n':
			flush()
			inlines = append(inlines, udf.Inline{Kind: udf.InlineBreak, Break: &udf.LineBreak{Text: "\n"}})
		case '\r':
		default:
			run.WriteRune(char)
		}
	}
	flush()
	return inlines
}

func borderFromTable(source domain.TableBorders) udf.Border {
	spec := uint32(0)
	var candidate domain.BorderStyle
	for bit, border := range []domain.BorderStyle{source.Top, source.Right, source.Bottom, source.Left} {
		if border.Style != domain.BorderNone {
			spec |= 1 << bit
			if candidate.Style == domain.BorderNone {
				candidate = border
			}
		}
	}
	if spec == 0 {
		return udf.Border{}
	}
	style := "borderStyle-solid"
	switch candidate.Style {
	case domain.BorderDotted:
		style = "borderStyle-dotted"
	case domain.BorderDashed:
		style = "borderStyle-dashed"
	case domain.BorderDouble:
		style = "borderStyle-double"
	}
	color := udf.Opt[int32]{Set: true, Val: int32(candidate.Color.R)<<16 | int32(candidate.Color.G)<<8 | int32(candidate.Color.B)}
	return udf.Border{Kind: "borderSingle", Style: style, Width: float64(candidate.Width) / 8, Color: color, Spec: spec}
}

func mediaTypeForImage(format domain.ImageFormat) string {
	switch format {
	case domain.ImageFormatPNG:
		return "image/png"
	case domain.ImageFormatJPEG, domain.ImageFormatJPG:
		return "image/jpeg"
	case domain.ImageFormatGIF:
		return "image/gif"
	case domain.ImageFormatBMP:
		return "image/bmp"
	case domain.ImageFormatTIFF, domain.ImageFormatTIF:
		return "image/tiff"
	case domain.ImageFormatWEBP:
		return "image/webp"
	case domain.ImageFormatSVG:
		return "image/svg+xml"
	default:
		return ""
	}
}

func verticalAlignment(align domain.VerticalAlignment) string {
	switch align {
	case domain.VerticalAlignCenter:
		return "vcenter"
	case domain.VerticalAlignBottom:
		return "bottom"
	default:
		return "top"
	}
}

func twipsToPoints(value int) float64 { return float64(value) / 20 }
func emuToPoints(value int) float64   { return float64(value) / 12700 }
func finite(value float64) bool       { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func highlightColor(value domain.HighlightColor) int32 {
	colors := map[domain.HighlightColor]int32{
		domain.HighlightYellow: 0xFFFF00, domain.HighlightGreen: 0x00FF00,
		domain.HighlightCyan: 0x00FFFF, domain.HighlightMagenta: 0xFF00FF,
		domain.HighlightBlue: 0x0000FF, domain.HighlightRed: 0xFF0000,
		domain.HighlightDarkBlue: 0x000080, domain.HighlightDarkCyan: 0x008080,
		domain.HighlightDarkGreen: 0x008000, domain.HighlightDarkMagenta: 0x800080,
		domain.HighlightDarkRed: 0x800000, domain.HighlightDarkYellow: 0x808000,
		domain.HighlightDarkGray: 0x808080, domain.HighlightLightGray: 0xC0C0C0,
	}
	return colors[value]
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
