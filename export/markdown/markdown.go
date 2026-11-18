// Package markdown exports the normalized UDF model using Markdown syntax only.
package markdown

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mobinova/udf"
)

// Options controls body projection and explicitly lossy fallbacks.
type Options struct {
	// IncludeHeadersFooters adds each source header/footer once as a Markdown
	// blockquote. Page repetition cannot be represented by Markdown.
	IncludeHeadersFooters bool
	// AllowLossy writes placeholders for unsupported visible constructs. The
	// default returns udf.ErrUnsupported before writing any bytes.
	AllowLossy bool
}

type Exporter struct{ Options Options }

func (e Exporter) Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	if err := udf.Validate(doc); err != nil {
		return udf.ExportReport{}, err
	}
	if dst == nil {
		return udf.ExportReport{}, fmt.Errorf("udf markdown: nil writer")
	}
	s := &renderState{ctx: ctx, options: e.Options, resources: make(map[string]udf.Resource, len(doc.Resources))}
	for _, resource := range doc.Resources {
		s.resources[resource.ID] = resource
	}
	var sections []string
	if e.Options.IncludeHeadersFooters {
		for i, header := range doc.Headers {
			if err := contextError(ctx); err != nil {
				return s.report(), err
			}
			sections = append(sections, s.renderHeaderFooter(header, fmt.Sprintf("headers[%d]", i)))
		}
	}
	if doc.Page.Width > 0 || doc.Page.Height > 0 || doc.Page.LeftMargin != 0 || doc.Page.RightMargin != 0 || doc.Page.TopMargin != 0 || doc.Page.BottomMargin != 0 || doc.Page.Orientation != "" || doc.Page.HeaderDistance != 0 || doc.Page.FooterDistance != 0 {
		s.add("layout.page_properties_omitted", udf.DiagnosticVisualApproximation, "page", "Markdown does not represent page dimensions, orientation, or margins")
	}
	sections = append(sections, s.renderBlocks(doc.Body, "body")...)
	if e.Options.IncludeHeadersFooters {
		for i, footer := range doc.Footers {
			if err := contextError(ctx); err != nil {
				return s.report(), err
			}
			sections = append(sections, s.renderHeaderFooter(footer, fmt.Sprintf("footers[%d]", i)))
		}
	}
	if s.cancelErr != nil {
		return s.report(), s.cancelErr
	}
	if s.hasContentLoss && !e.Options.AllowLossy {
		return s.report(), udf.ErrUnsupported
	}
	output := strings.Join(sections, "\n\n")
	if output != "" {
		output += "\n"
	}
	_, err := io.WriteString(dst, output)
	return s.report(), err
}

func Export(ctx context.Context, doc *udf.Document, dst io.Writer) (udf.ExportReport, error) {
	return (Exporter{}).Export(ctx, doc, dst)
}

func ExportWithOptions(ctx context.Context, doc *udf.Document, dst io.Writer, options Options) (udf.ExportReport, error) {
	return (Exporter{Options: options}).Export(ctx, doc, dst)
}

type renderState struct {
	ctx            context.Context
	options        Options
	resources      map[string]udf.Resource
	diagnostics    []udf.Diagnostic
	hasContentLoss bool
	cancelErr      error
}

func (s *renderState) report() udf.ExportReport {
	return udf.ExportReport{Diagnostics: append([]udf.Diagnostic(nil), s.diagnostics...)}
}

func (s *renderState) add(code string, class udf.DiagnosticClass, location, message string) {
	if class == udf.DiagnosticContentLoss {
		s.hasContentLoss = true
	}
	d := udf.Diagnostic{Code: code, Severity: udf.SeverityWarning, Class: class, Location: location, Message: message}
	if class == udf.DiagnosticContentLoss {
		d.Severity = udf.SeverityError
	}
	const maxDiagnostics = 512
	if len(s.diagnostics) < maxDiagnostics {
		s.diagnostics = append(s.diagnostics, d)
		return
	}
	s.diagnostics[maxDiagnostics-1] = udf.Diagnostic{Code: "diagnostics.truncated", Severity: udf.SeverityWarning, Class: udf.DiagnosticNormalization, Location: "document", Message: "Additional export diagnostics were omitted by the limit"}
}

func (s *renderState) cancelled() bool {
	if s.cancelErr != nil {
		return true
	}
	if err := contextError(s.ctx); err != nil {
		s.cancelErr = err
		return true
	}
	return false
}

func (s *renderState) renderHeaderFooter(section udf.HeaderFooter, location string) string {
	tag := "Header"
	if section.Kind == "footer" {
		tag = "Footer"
	}
	s.add("header_footer.scope_approximated", udf.DiagnosticVisualApproximation, location, "Header/footer is included once; page repetition is not represented")
	if section.StartPage > 1 {
		s.add("header_footer.start_page_omitted", udf.DiagnosticVisualApproximation, location, "Header/footer start-page behavior is not represented")
	}
	lines := []string{"**" + tag + "**"}
	for i, block := range section.Blocks {
		lines = append(lines, s.renderBlock(block, fmt.Sprintf("%s.blocks[%d]", location, i)))
	}
	var quoted []string
	for _, line := range lines {
		for _, sub := range strings.Split(line, "\n") {
			quoted = append(quoted, "> "+sub)
		}
	}
	return strings.Join(quoted, "\n")
}

func (s *renderState) renderBlocks(blocks []udf.Block, location string) []string {
	var out []string
	for i := 0; i < len(blocks); {
		if s.cancelled() {
			break
		}
		block := blocks[i]
		loc := fmt.Sprintf("%s[%d]", location, i)
		if block.Kind == udf.BlockParagraph && block.Paragraph != nil && isList(block.Paragraph) {
			end := i + 1
			for end < len(blocks) && blocks[end].Kind == udf.BlockParagraph && blocks[end].Paragraph != nil && sameList(block.Paragraph.Properties, blocks[end].Paragraph.Properties) {
				end++
			}
			out = append(out, s.renderList(blocks[i:end], loc))
			i = end
			continue
		}
		out = append(out, s.renderBlock(block, loc))
		i++
	}
	return out
}

func isList(p *udf.Paragraph) bool { return p.Properties.Numbered || p.Properties.Bulleted }
func sameList(a, b udf.ParagraphStyle) bool {
	return isList(&udf.Paragraph{Properties: a}) && a.Numbered == b.Numbered && a.Bulleted == b.Bulleted && a.ListID == b.ListID && a.ListLevel == b.ListLevel && a.NumberType == b.NumberType && a.BulletType == b.BulletType
}

func (s *renderState) renderList(blocks []udf.Block, location string) string {
	first := blocks[0].Paragraph.Properties
	if first.Numbered && first.Bulleted {
		s.add("list.marker_conflict", udf.DiagnosticNormalization, location, "Both numbered and bulleted flags are set; numbering takes precedence")
	}
	if first.Numbered && first.NumberType != "" && first.NumberType != "NUMBER_TYPE_NUMBER_DOT" {
		s.add("list.numbering_approximated", udf.DiagnosticVisualApproximation, location, "List sequence uses standard Markdown numbering; source marker punctuation may differ")
	}
	if first.Bulleted && first.BulletType != "" && first.BulletType != "BULLET_TYPE_ELLIPSE" {
		s.add("list.bullet_approximated", udf.DiagnosticVisualApproximation, location, "Custom bullet is represented by a standard Markdown bullet")
	}
	indent := first.ListLevel
	if indent < 0 {
		indent = 0
	}
	if indent > 1 {
		indent = 1
		s.add("list.depth_limited", udf.DiagnosticVisualApproximation, location, "List depth was capped to avoid accidental Markdown code indentation")
	}
	marker := "- "
	if first.Numbered {
		marker = "1. "
	}
	lines := make([]string, 0, len(blocks))
	for i, block := range blocks {
		if s.cancelled() {
			break
		}
		p := block.Paragraph
		s.noteParagraphProjection(p, fmt.Sprintf("%s[%d]", location, i))
		lines = append(lines, strings.Repeat("  ", indent)+marker+s.renderInlines(p.Inlines, fmt.Sprintf("%s[%d]", location, i), hasLayoutIndent(p.Properties)))
	}
	return strings.Join(lines, "\n")
}

func (s *renderState) renderBlock(block udf.Block, location string) string {
	switch block.Kind {
	case udf.BlockParagraph:
		if block.Paragraph == nil {
			return ""
		}
		return s.renderParagraph(block.Paragraph, location)
	case udf.BlockTable:
		if block.Table == nil {
			return ""
		}
		return s.renderTable(block.Table, location)
	case udf.BlockPageBreak:
		s.add("layout.page_break_approximated", udf.DiagnosticVisualApproximation, location, "Markdown uses a horizontal rule as a page-break placeholder")
		return "---"
	case udf.BlockUnknown:
		if block.Unknown == nil {
			return ""
		}
		if block.Unknown.Text == "" {
			s.add("content.unknown_block_omitted", udf.DiagnosticContentLoss, location, "Unknown block has no readable text projection")
			if s.options.AllowLossy {
				return "[Unsupported content omitted]"
			}
			return ""
		}
		s.add("content.unknown_block_structure", udf.DiagnosticVisualApproximation, location, "Unknown block structure was projected as readable text")
		return markdownEscape(block.Unknown.Text)
	default:
		s.add("content.unknown_block_kind", udf.DiagnosticContentLoss, location, "Unknown block kind cannot be represented")
		if s.options.AllowLossy {
			return "[Unsupported content omitted]"
		}
		return ""
	}
}

func (s *renderState) renderParagraph(p *udf.Paragraph, location string) string {
	s.noteParagraphProjection(p, location)
	if len(p.Inlines) == 0 {
		s.add("paragraph.empty_projection", udf.DiagnosticVisualApproximation, location, "Empty paragraph is represented as a blank Markdown block")
		return ""
	}
	text := s.renderInlines(p.Inlines, location, hasLayoutIndent(p.Properties))
	if p.Properties.Numbered && p.Properties.Bulleted {
		s.add("list.marker_conflict", udf.DiagnosticNormalization, location, "Both list flags are set; numbering takes precedence")
	}
	if p.Properties.Numbered {
		if p.Properties.NumberType != "" && p.Properties.NumberType != "NUMBER_TYPE_NUMBER_DOT" {
			s.add("list.numbering_approximated", udf.DiagnosticVisualApproximation, location, "Source numbering style is represented with standard Markdown numbering")
		}
		return "1. " + text
	}
	if p.Properties.Bulleted {
		if p.Properties.BulletType != "" && p.Properties.BulletType != "BULLET_TYPE_ELLIPSE" {
			s.add("list.bullet_approximated", udf.DiagnosticVisualApproximation, location, "Source bullet style is represented with a standard Markdown bullet")
		}
		return "- " + text
	}
	return text
}

func (s *renderState) noteParagraphProjection(p *udf.Paragraph, location string) {
	props := p.Properties
	if (props.Alignment != "" && props.Alignment != udf.AlignLeft) || props.LeftIndent != 0 || props.RightIndent != 0 || props.FirstLineIndent != 0 || props.Hanging != 0 || props.SpaceAbove != 0 || props.SpaceBelow != 0 {
		s.add("paragraph.layout_omitted", udf.DiagnosticVisualApproximation, location, "Alignment, indentation, or spacing is not represented in Markdown")
	}
	if props.LineSpacing != 0 {
		s.add("paragraph.line_spacing_omitted", udf.DiagnosticVisualApproximation, location, "Line spacing is not represented in Markdown")
	}
	if len(props.Tabs) > 0 {
		s.add("paragraph.tab_stops_omitted", udf.DiagnosticVisualApproximation, location, "Tab stop positions and leaders are not represented in Markdown")
	}
	if strings.TrimSpace(p.Text()) != p.Text() {
		s.add("paragraph.edge_whitespace", udf.DiagnosticVisualApproximation, location, "Markdown viewers may collapse leading or trailing whitespace")
	}
}

func (s *renderState) renderInlines(inlines []udf.Inline, location string, stripLayoutIndent bool) string {
	var out strings.Builder
	var chunks []styledChunk
	var leading strings.Builder
	paragraphStart := true
	lastWasTab := false
	skipWhitespaceBeforePunctuation := false
	appendChunk := func(text string, style udf.TextStyle, index int) {
		if text == "" {
			return
		}
		if strings.TrimSpace(text) != "" && (style.Underline || style.FontFamily != "" || style.FontSize != 0 || style.Foreground.Set || style.Background.Set) {
			s.add("style.markdown_omitted", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.inlines[%d]", location, index), "Underline, font, or color styling is not represented in pure Markdown")
		}
		if len(chunks) > 0 && sameEmphasis(chunks[len(chunks)-1].style, style) {
			chunks[len(chunks)-1].text += text
		} else {
			chunks = append(chunks, styledChunk{text: text, style: style, index: index})
		}
	}
	flush := func() {
		for i := 0; i < len(chunks); {
			j := i + 1
			text := chunks[i].text
			for j < len(chunks) && sameEmphasis(chunks[i].style, chunks[j].style) {
				text += chunks[j].text
				j++
			}
			out.WriteString(s.renderStyledText(text, chunks[i].style, fmt.Sprintf("%s.inlines[%d]", location, chunks[i].index)))
			i = j
		}
		chunks = chunks[:0]
	}
	flushLeading := func(index int) {
		text := leading.String()
		leading.Reset()
		if text == "" {
			return
		}
		spaces := len(text) - len(strings.TrimLeft(text, " "))
		if strings.ContainsRune(text, '\t') || stripLayoutIndent || spaces >= 4 {
			s.add("paragraph.leading_indentation_omitted", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.inlines[%d]", location, index), "Leading layout indentation was removed to prevent an accidental Markdown code block")
			return
		}
		appendChunk(text, udf.TextStyle{}, index)
	}
	addText := func(text string, style udf.TextStyle, index int) {
		if skipWhitespaceBeforePunctuation {
			text = strings.TrimLeftFunc(text, unicode.IsSpace)
			if text != "" {
				skipWhitespaceBeforePunctuation = false
			}
		}
		if paragraphStart {
			prefixEnd := 0
			for prefixEnd < len(text) {
				r, size := utf8.DecodeRuneInString(text[prefixEnd:])
				if !unicode.IsSpace(r) {
					break
				}
				prefixEnd += size
			}
			if prefixEnd > 0 {
				leading.WriteString(text[:prefixEnd])
				text = text[prefixEnd:]
			}
			if text == "" {
				return
			}
			flushLeading(index)
			paragraphStart = false
		}
		if strings.ContainsRune(text, '\t') {
			var normalized strings.Builder
			previousTab := false
			for _, r := range text {
				if r == '\t' {
					if !previousTab {
						normalized.WriteByte(' ')
					}
					previousTab = true
					continue
				}
				normalized.WriteRune(r)
				previousTab = false
			}
			text = normalized.String()
			s.add("text.tab_approximated", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.inlines[%d]", location, index), "Inline tabs were converted to spaces")
		}
		if len(chunks) > 0 && !style.Bold && !unicode.IsSpace(firstRune(text)) && needsLabelSeparator(chunks) {
			appendChunk(" ", udf.TextStyle{}, index)
			s.add("text.label_separator_inserted", udf.DiagnosticNormalization, fmt.Sprintf("%s.inlines[%d]", location, index), "A space was added after a bold field label for readability")
		}
		appendChunk(text, style, index)
	}
	for i, in := range inlines {
		if s.cancelled() {
			break
		}
		loc := fmt.Sprintf("%s.inlines[%d]", location, i)
		switch in.Kind {
		case udf.InlineRun:
			if in.Run != nil {
				addText(in.Run.Text, in.Run.Style, i)
			}
		case udf.InlineField:
			if in.Field != nil {
				addText(in.Field.Text, in.Field.Style, i)
			}
		case udf.InlineTab:
			if paragraphStart {
				leading.WriteString("\t")
				continue
			}
			if next := nextNonWhitespace(inlines[i+1:]); strings.ContainsRune(":;,!?", next) {
				skipWhitespaceBeforePunctuation = true
				s.add("text.alignment_tab_omitted", udf.DiagnosticVisualApproximation, loc, "A tab before punctuation was treated as label alignment")
				continue
			}
			if !lastWasTab {
				appendChunk(" ", udf.TextStyle{}, i)
			}
			lastWasTab = true
			s.add("text.tab_approximated", udf.DiagnosticVisualApproximation, loc, "Inline tab stops were projected as one space")
		case udf.InlineBreak:
			flushLeading(i)
			flush()
			if in.Break != nil {
				out.WriteString("  \n")
			}
			paragraphStart = true
			lastWasTab = false
		default:
			flushLeading(i)
			flush()
			out.WriteString(s.renderInline(in, loc))
			paragraphStart = false
			lastWasTab = false
		}
		if in.Kind != udf.InlineTab && in.Kind != udf.InlineRun && in.Kind != udf.InlineField {
			continue
		}
		if in.Kind == udf.InlineRun && in.Run != nil && strings.TrimSpace(in.Run.Text) != "" {
			lastWasTab = false
		}
		if in.Kind == udf.InlineField && in.Field != nil && strings.TrimSpace(in.Field.Text) != "" {
			lastWasTab = false
		}
	}
	flushLeading(len(inlines))
	flush()
	return out.String()
}

func hasLayoutIndent(p udf.ParagraphStyle) bool {
	return len(p.Tabs) > 0 || p.LeftIndent != 0 || p.FirstLineIndent != 0 || p.Hanging != 0
}

type styledChunk struct {
	text  string
	style udf.TextStyle
	index int
}

func sameEmphasis(a, b udf.TextStyle) bool {
	return a.Bold == b.Bold && a.Italic == b.Italic && a.Strike == b.Strike
}

func firstRune(value string) rune { r, _ := utf8.DecodeRuneInString(value); return r }

func needsLabelSeparator(chunks []styledChunk) bool {
	trailingSpace := false
	for i := len(chunks) - 1; i >= 0; i-- {
		text := chunks[i].text
		for j := len(text); j > 0; {
			r, size := utf8.DecodeLastRuneInString(text[:j])
			j -= size
			if unicode.IsSpace(r) {
				trailingSpace = true
				continue
			}
			if r != ':' || trailingSpace {
				return false
			}
			for k := 0; k < i; k++ {
				if chunks[k].style.Bold {
					return true
				}
			}
			return chunks[i].style.Bold
		}
	}
	return false
}

func nextNonWhitespace(inlines []udf.Inline) rune {
	for _, in := range inlines {
		var value string
		switch in.Kind {
		case udf.InlineRun:
			if in.Run != nil {
				value = in.Run.Text
			}
		case udf.InlineField:
			if in.Field != nil {
				value = in.Field.Text
			}
		case udf.InlineUnknown:
			if in.Unknown != nil {
				value = in.Unknown.Text
			}
		case udf.InlineBreak:
			return '\n'
		case udf.InlineImage:
			return 'x'
		default:
			continue
		}
		for _, r := range value {
			if !unicode.IsSpace(r) {
				return r
			}
		}
	}
	return 0
}

func (s *renderState) renderStyledText(text string, style udf.TextStyle, location string) string {
	if text == "" {
		return ""
	}
	if strings.TrimSpace(text) == "" {
		return markdownEscape(text)
	}
	_ = location
	start := 0
	for start < len(text) {
		r, size := utf8.DecodeRuneInString(text[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	end := len(text)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	prefix, suffix := markdownEscape(text[:start]), markdownEscape(text[end:])
	core := markdownEscape(text[start:end])
	if style.Bold && style.Italic {
		core = "***" + core + "***"
	} else if style.Bold {
		core = "**" + core + "**"
	} else if style.Italic {
		core = "*" + core + "*"
	}
	if style.Strike {
		core = "~~" + core + "~~"
	}
	return prefix + core + suffix
}

func (s *renderState) renderInline(in udf.Inline, location string) string {
	switch in.Kind {
	case udf.InlineRun:
		if in.Run == nil {
			return ""
		}
		return s.renderStyledText(in.Run.Text, in.Run.Style, location)
	case udf.InlineField:
		if in.Field == nil {
			return ""
		}
		return s.renderStyledText(in.Field.Text, in.Field.Style, location)
	case udf.InlineTab:
		if in.Tab == nil {
			return ""
		}
		s.add("text.tab_projection", udf.DiagnosticVisualApproximation, location, "Literal tab is preserved; viewer tab-stop rendering may differ")
		return in.Tab.Text
	case udf.InlineBreak:
		if in.Break == nil {
			return ""
		}
		return "  \n"
	case udf.InlineImage:
		if in.Image == nil {
			return ""
		}
		resource, ok := s.resources[in.Image.ResourceID]
		if !ok || !safeImageType(resource.MediaType) {
			s.add("resource.image_omitted", udf.DiagnosticContentLoss, location, "Image resource is unavailable or has unsupported media type")
			if s.options.AllowLossy {
				return "[Image omitted]"
			}
			return ""
		}
		uri := "data:" + resource.MediaType + ";base64," + base64.StdEncoding.EncodeToString(resource.Data)
		s.add("resource.data_uri_renderer_support", udf.DiagnosticVisualApproximation, location, "Image is embedded as a data URI; some Markdown viewers block data URIs")
		if in.Image.Width > 0 || in.Image.Height > 0 {
			s.add("resource.dimensions_omitted", udf.DiagnosticVisualApproximation, location, "Markdown image dimensions are not represented")
		}
		return "![Embedded image](" + uri + ")"
	case udf.InlineUnknown:
		if in.Unknown == nil {
			return ""
		}
		if in.Unknown.Text == "" {
			s.add("content.unknown_inline_omitted", udf.DiagnosticContentLoss, location, "Unknown inline item has no readable text projection")
			if s.options.AllowLossy {
				return "[Unsupported content omitted]"
			}
			return ""
		}
		s.add("content.unknown_inline_structure", udf.DiagnosticVisualApproximation, location, "Unknown inline structure is represented by readable text")
		return markdownEscape(in.Unknown.Text)
	default:
		s.add("content.unknown_inline_kind", udf.DiagnosticContentLoss, location, "Unknown inline kind cannot be represented")
		if s.options.AllowLossy {
			return "[Unsupported content omitted]"
		}
		return ""
	}
}

func (s *renderState) renderTable(t *udf.Table, location string) string {
	if t.ColumnCount > 10000 {
		s.add("table.too_wide", udf.DiagnosticContentLoss, location, "Table exceeds Markdown export width limit")
		if s.options.AllowLossy {
			return "[Table omitted]"
		}
		return ""
	}
	cols := t.ColumnCount
	if cols < 1 {
		cols = 1
	}
	rows := make([][]string, len(t.Rows))
	for ri, row := range t.Rows {
		if s.cancelled() {
			break
		}
		rows[ri] = make([]string, cols)
		for ci, cell := range row.Cells {
			if cell.GridCol < 0 || cell.GridCol >= cols {
				continue
			}
			var pieces []string
			for bi, block := range cell.Blocks {
				loc := fmt.Sprintf("%s.rows[%d].cells[%d].blocks[%d]", location, ri, ci, bi)
				if block.Kind == udf.BlockTable && block.Table != nil {
					pieces = append(pieces, markdownEscape(strings.ReplaceAll(strings.ReplaceAll(block.Table.Text(), "\t", " / "), "\n", " / ")))
					s.add("table.nested_projection", udf.DiagnosticVisualApproximation, loc, "Nested table is flattened to readable cell text")
				} else {
					pieces = append(pieces, s.renderBlock(block, loc))
				}
			}
			if len(cell.Blocks) > 1 {
				s.add("table.cell_paragraphs_approximated", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci), "Cell paragraphs are joined with a slash separator")
			}
			rows[ri][cell.GridCol] = markdownCell(strings.Join(pieces, " / "))
			if cell.RowSpan > 1 || cell.ColSpan > 1 {
				s.add("table.spans_approximated", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci), "Markdown table syntax cannot encode merged cells; text stays in the anchor cell")
			}
			if cell.Background.Set || cell.Border.Width > 0 || cell.VerticalAlign != "" {
				s.add("table.cell_style_omitted", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci), "Cell borders, fill, and vertical alignment are not represented in Markdown")
			}
		}
		if row.Height > 0 {
			s.add("table.row_height_omitted", udf.DiagnosticVisualApproximation, fmt.Sprintf("%s.rows[%d]", location, ri), "Row height is not represented in Markdown")
		}
	}
	if len(rows) == 0 {
		return ""
	}
	if t.ColumnCount == 0 {
		s.add("table.columns_inferred", udf.DiagnosticNormalization, location, "Table columns were inferred from cell positions")
	}
	if len(t.ColumnWidths) > 0 {
		s.add("table.widths_omitted", udf.DiagnosticVisualApproximation, location, "Column widths are not represented in Markdown")
	}
	if t.Rows[0].Type != "headerRow" {
		s.add("table.header_inferred", udf.DiagnosticVisualApproximation, location, "The first source row is used as the Markdown table header")
	}
	for i, row := range t.Rows {
		if i > 0 && row.Type == "headerRow" {
			s.add("table.header_position_approximated", udf.DiagnosticVisualApproximation, location, "Markdown only supports the header at the first row")
		}
	}
	var lines []string
	lines = append(lines, "| "+strings.Join(rows[0], " | ")+" |")
	separator := make([]string, cols)
	for i := range separator {
		separator[i] = "---"
	}
	lines = append(lines, "| "+strings.Join(separator, " | ")+" |")
	for _, row := range rows[1:] {
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " / ")
	value = strings.ReplaceAll(value, "\n", " / ")
	return value
}
func markdownEscape(value string) string {
	var b strings.Builder
	lineStart := true
	for i, r := range value {
		needsEscape := strings.ContainsRune("\\`*_[]|<>#", r)
		next := rune(0)
		if i+utf8.RuneLen(r) < len(value) {
			next, _ = utf8.DecodeRuneInString(value[i+utf8.RuneLen(r):])
		}
		if lineStart && (r == '-' || r == '+') && unicode.IsSpace(next) {
			needsEscape = true
		}
		if (r == '.' || r == ')') && unicode.IsSpace(next) {
			start, digits := i, 0
			for start > 0 {
				previous, size := utf8.DecodeLastRuneInString(value[:start])
				if previous < '0' || previous > '9' {
					break
				}
				digits++
				start -= size
			}
			lineStartIndex := strings.LastIndex(value[:i], "\n") + 1
			if digits > 0 && digits <= 9 && strings.Trim(value[lineStartIndex:start], " \t") == "" {
				needsEscape = true
			}
		}
		if lineStart && r == '-' && next == '-' {
			line := strings.TrimSpace(value[i:])
			if len(line) >= 3 && strings.Trim(line, "-") == "" {
				needsEscape = true
			}
		}
		if !lineStart && r == '#' {
			needsEscape = false
		}
		if r == '\n' {
			lineStart = true
		} else if !unicode.IsSpace(r) {
			lineStart = false
		}
		if needsEscape {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func safeImageType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0])) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/tiff":
		return true
	}
	return false
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
