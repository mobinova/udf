package udf

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var safeXMLName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]{0,63}$`)

type modelBuilder struct {
	doc                  *Document
	pool                 string
	offsets              []int32
	styles               map[string]Style
	limits               parseLimits
	opts                 ParseOptions
	resourceBytes        int64
	resources            map[[32]byte]string
	unknownBytes         int64
	diagnosticsTruncated bool
	fatal                error
}

func buildDocument(root *xmlNode, opts ParseOptions, lim parseLimits) (*Document, error) {
	if root.Name.Local != "template" {
		return nil, fmt.Errorf("udf: unsupported root: %w", ErrUnsupported)
	}
	format := attr(root, "format_id")
	if format != "" && format != "1.8" {
		return nil, fmt.Errorf("udf: unsupported UDF format version: %w", ErrUnsupported)
	}
	content := child(root, "content")
	if content == nil {
		return nil, fmt.Errorf("udf: missing content element: %w", ErrInvalidXML)
	}
	b := &modelBuilder{doc: &Document{FormatID: format, Properties: DocumentProperties{Values: map[string]string{}}}, pool: content.Text.String(), styles: map[string]Style{}, limits: lim, opts: opts, resources: map[[32]byte]string{}}
	if web := child(root, "webID"); web != nil {
		b.doc.WebID = attr(web, "id")
		if b.doc.WebID == "" {
			b.doc.WebID = web.Text.String()
		}
	}
	b.offsets = makeUTF16Map(b.pool)
	b.parsePage(child(child(root, "properties"), "pageFormat"))
	for _, n := range children(child(root, "styles"), "style") {
		style := b.parseStyle(n)
		if style.Name != "" {
			b.styles[style.Name] = style
			b.doc.Styles = append(b.doc.Styles, style)
		}
	}
	b.checkUnknownXML(root)
	elements := child(root, "elements")
	if elements == nil {
		return nil, fmt.Errorf("udf: missing elements element: %w", ErrInvalidXML)
	}
	for _, n := range elements.Children {
		switch n.Name.Local {
		case "header":
			h := HeaderFooter{Kind: "header"}
			h.StartPage, _ = intAttr(n, "startPage")
			h.Blocks = b.parseBlocks(n.Children, "header")
			b.doc.Headers = append(b.doc.Headers, h)
		case "footer":
			f := HeaderFooter{Kind: "footer"}
			f.Blocks = b.parseBlocks(n.Children, "footer")
			b.doc.Footers = append(b.doc.Footers, f)
		default:
			b.doc.Body = append(b.doc.Body, b.parseBlock(n, "body")...)
		}
	}
	if d := child(root, "data"); d != nil && len(d.Children) > 0 {
		b.diag("source.template_data_unsupported", DiagnosticSourceInterpretation, "template.data", "Template data is not part of the visible document model")
	}
	if b.fatal != nil {
		return nil, b.fatal
	}
	if len(b.doc.Diagnostics) > lim.diagnostics {
		b.doc.Diagnostics = b.doc.Diagnostics[:lim.diagnostics]
	}
	return b.doc, nil
}

func (b *modelBuilder) diag(code string, class DiagnosticClass, loc, msg string) {
	if len(b.doc.Diagnostics) >= b.limits.diagnostics {
		if !b.diagnosticsTruncated && b.limits.diagnostics > 0 {
			b.doc.Diagnostics[b.limits.diagnostics-1] = Diagnostic{Code: "diagnostics.truncated", Severity: SeverityWarning, Class: DiagnosticNormalization, Location: "document", Message: "Additional diagnostics were omitted by the configured limit"}
			b.diagnosticsTruncated = true
		}
		return
	}
	if !safeLocation(loc) {
		loc = "document"
	}
	b.doc.Diagnostics = append(b.doc.Diagnostics, Diagnostic{Code: code, Severity: SeverityWarning, Class: class, Location: loc, Message: msg})
}

func safeLocation(s string) bool {
	if len(s) > 200 {
		return false
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '[' || r == ']' }) {
		if part != "" && !safeXMLName.MatchString(strings.TrimSuffix(part, "_")) {
			return false
		}
	}
	return true
}

func (b *modelBuilder) parsePage(n *xmlNode) {
	if n == nil {
		b.diag("layout.page_format_missing", DiagnosticSourceInterpretation, "properties.pageFormat", "Page layout is unavailable")
		return
	}
	page := &b.doc.Page
	page.MediaSizeName = attr(n, "mediaSizeName")
	page.LeftMargin, _ = floatAttr(n, "leftMargin")
	page.RightMargin, _ = floatAttr(n, "rightMargin")
	page.TopMargin, _ = floatAttr(n, "topMargin")
	page.BottomMargin, _ = floatAttr(n, "bottomMargin")
	page.HeaderDistance, _ = floatAttr(n, "headerFOffset")
	page.FooterDistance, _ = floatAttr(n, "footerFOffset")
	if attr(n, "paperOrientation") == "0" {
		page.Orientation = Landscape
	} else if attr(n, "paperOrientation") == "1" {
		page.Orientation = Portrait
	} else {
		b.diag("layout.unknown_orientation", DiagnosticSourceInterpretation, "properties.pageFormat", "Page orientation is missing or unrecognized")
	}
	if attr(n, "mediaSizeName") == "1" {
		page.Width, page.Height = 595.28, 841.89
	} else {
		b.diag("layout.unknown_page_size", DiagnosticSourceInterpretation, "properties.pageFormat", "Page size is not recognized; dimensions are unavailable")
	}
	if page.Orientation == Landscape && page.Width > 0 {
		page.Width, page.Height = page.Height, page.Width
	}
}

func (b *modelBuilder) parseStyle(n *xmlNode) Style {
	s := Style{Name: attr(n, "name"), Description: attr(n, "description")}
	s.Text = mergeTextStyle(TextStyle{}, n.Attr)
	s.TextOverrides = textStyleOverrides(n.Attr)
	s.Paragraph = parseParagraphStyle(n.Attr)
	return s
}

func (b *modelBuilder) parseBlocks(nodes []*xmlNode, loc string) []Block {
	var blocks []Block
	for i, n := range nodes {
		blocks = append(blocks, b.parseBlock(n, fmt.Sprintf("%s[%d]", loc, i))...)
	}
	return blocks
}

func (b *modelBuilder) parseBlock(n *xmlNode, loc string) []Block {
	switch n.Name.Local {
	case "paragraph":
		return []Block{{Kind: BlockParagraph, Paragraph: b.parseParagraph(n, loc)}}
	case "table":
		table, err := b.parseTable(n, loc)
		if err != nil {
			b.diag("table.invalid_grid", DiagnosticSourceInterpretation, loc, "Table grid could not be normalized")
		}
		return []Block{{Kind: BlockTable, Table: table}}
	case "page-break":
		return []Block{{Kind: BlockPageBreak, PageBreak: &PageBreak{}}}
	case "header", "footer":
		// Nested headers/footers are out of scope in the first document layout.
		b.diag("layout.nested_header_footer", DiagnosticSourceInterpretation, loc, "Nested header/footer is not supported")
		return []Block{{Kind: BlockUnknown, Unknown: &UnknownBlock{Element: n.Name.Local, RawXML: b.rawUnknown(n)}}}
	default:
		b.diag("source.unknown_block", DiagnosticSourceInterpretation, loc, "Unknown structural block was retained")
		text := ""
		if start, length, hasSpan, spanErr := spanAttrs(n); hasSpan {
			if spanErr != nil {
				b.invalidSpan(loc)
			} else if value, err := b.textSpan(start, length); err == nil {
				text = value
			} else {
				b.invalidSpan(loc)
			}
		}
		unknown := &UnknownBlock{Element: safeName(n.Name.Local), Text: text, RawXML: b.rawUnknown(n)}
		return []Block{{Kind: BlockUnknown, Unknown: unknown}}
	}
}

func (b *modelBuilder) parseParagraph(n *xmlNode, loc string) *Paragraph {
	name := attr(n, "resolver")
	base := b.styles[name]
	p := &Paragraph{StyleName: name, Properties: mergeParagraphStyle(base.Paragraph, n.Attr)}
	paragraphTextStyle := mergeTextStyle(base.Text, n.Attr)
	var end int
	var lastText *string
	var lastEnd int
	for i, c := range n.Children {
		cloc := fmt.Sprintf("%s.inlines[%d]", loc, i)
		start, length, hasSpan, err := spanAttrs(c)
		if err != nil {
			b.invalidSpan(cloc)
			continue
		}
		text := ""
		if hasSpan {
			text, err = b.textSpan(start, length)
			if err != nil {
				b.invalidSpan(cloc)
				continue
			}
			if start+length > end {
				end = start + length
			}
		}
		inlineStyleBase := paragraphTextStyle
		if inlineResolver := attr(c, "resolver"); inlineResolver != "" {
			if resolved, ok := b.styles[inlineResolver]; ok {
				inlineStyleBase = applyTextOverrides(inlineStyleBase, resolved.TextOverrides)
			}
		}
		style := mergeTextStyle(inlineStyleBase, c.Attr)
		switch c.Name.Local {
		case "content":
			r := &TextRun{Text: text, Style: style}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineRun, Run: r})
			if hasSpan {
				lastText = &r.Text
				lastEnd = start + length
			}
		case "space":
			r := &TextRun{Text: text, Style: style}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineRun, Run: r})
			if hasSpan {
				lastText = &r.Text
				lastEnd = start + length
			}
		case "field":
			f := &Field{Name: attr(c, "fieldName"), Type: attr(c, "fieldType"), Group: attr(c, "fieldGroupName"), Text: text, Style: style}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineField, Field: f})
			if hasSpan {
				lastText = &f.Text
				lastEnd = start + length
			}
		case "tab":
			t := &Tab{Text: text}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineTab, Tab: t})
			if hasSpan {
				lastText = &t.Text
				lastEnd = start + length
			}
		case "image":
			img := &Image{Width: numberAttr(c, "width"), Height: numberAttr(c, "height")}
			img.ResourceID = b.addImage(attr(c, "imageData"), cloc)
			p.Inlines = append(p.Inlines, Inline{Kind: InlineImage, Image: img})
		case "page-break":
			br := &LineBreak{Text: "\n"}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineBreak, Break: br})
		default:
			b.diag("source.unknown_inline", DiagnosticSourceInterpretation, cloc, "Unknown inline item was retained")
			unknown := &UnknownInline{Element: safeName(c.Name.Local), Text: text, RawXML: b.rawUnknown(c)}
			p.Inlines = append(p.Inlines, Inline{Kind: InlineUnknown, Unknown: unknown})
			if hasSpan {
				lastText = &unknown.Text
				lastEnd = start + length
			}
		}
	}
	// UYAP's shared text pool stores a paragraph-terminating LF in the final
	// annotated span. Remove that structural delimiter from inline text; Text()
	// supplies the paragraph boundary once. A controlled non-BMP Editor fixture
	// is still needed to verify the UTF-16 offset hypothesis.
	if end > 0 && lastText != nil && lastEnd == end && strings.HasSuffix(*lastText, "\n") {
		*lastText = strings.TrimSuffix(*lastText, "\n")
	}
	return p
}

func (b *modelBuilder) invalidSpan(location string) {
	if b.opts.BestEffort {
		b.diag("text.invalid_span", DiagnosticNormalization, location, "Invalid text span was omitted during best-effort parsing")
		return
	}
	if b.fatal == nil {
		b.fatal = fmt.Errorf("udf: %w", ErrInvalidOffset)
	}
}

func (b *modelBuilder) textSpan(start, length int) (string, error) {
	if start < 0 || length < 0 || start+length < start || start+length >= len(b.offsets) {
		return "", ErrInvalidOffset
	}
	bs, be := b.offsets[start], b.offsets[start+length]
	if bs < 0 || be < 0 || be < bs || int(be) > len(b.pool) {
		return "", ErrInvalidOffset
	}
	return b.pool[bs:be], nil
}

func (b *modelBuilder) parseTable(n *xmlNode, loc string) (*Table, error) {
	t := &Table{Name: attr(n, "tableName")}
	if count, ok := intAttr(n, "columnCount"); ok && count >= 0 {
		t.ColumnCount = count
	}
	if widths := attr(n, "columnSpans"); widths != "" {
		parts := strings.Split(widths, ",")
		parsed := make([]ColumnWidth, 0, len(parts))
		valid := true
		for _, part := range parts {
			v, e := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				valid = false
				break
			}
			parsed = append(parsed, ColumnWidth{Value: v, Unit: UnitPoint})
		}
		if valid && (t.ColumnCount == 0 || len(parsed) == t.ColumnCount) {
			t.ColumnWidths = parsed
		} else {
			b.diag("table.column_widths_unresolved", DiagnosticSourceInterpretation, loc, "Table column widths are not understood")
		}
	}
	if attr(n, "rowSpans") != "" {
		b.diag("table.row_spans_unresolved", DiagnosticSourceInterpretation, loc, "Table row span metadata is not understood")
	}
	for ri, rn := range children(n, "row") {
		row := TableRow{Name: attr(rn, "rowName"), Type: attr(rn, "rowType"), Height: numberAttr(rn, "height")}
		for _, cn := range children(rn, "cell") {
			cell := TableCell{RowSpan: 1, ColSpan: 1, Background: intColor(cn, "fillColor"), Border: parseBorder(cn)}
			if v, ok := intAttr(cn, "rowspan"); ok {
				cell.RowSpan = v
			}
			if v, ok := intAttr(cn, "colspan"); ok {
				cell.ColSpan = v
			}
			cell.VerticalAlign = attr(cn, "align")
			cell.Blocks = b.parseBlocks(cn.Children, fmt.Sprintf("%s.rows[%d].cells", loc, ri))
			row.Cells = append(row.Cells, cell)
		}
		t.Rows = append(t.Rows, row)
	}
	if err := normalizeGrid(t, b.limits.gridCells); err != nil {
		return t, err
	}
	return t, nil
}

func (b *modelBuilder) addImage(encoded, loc string) string {
	if encoded == "" {
		b.diag("resource.image_missing", DiagnosticSourceInterpretation, loc, "Image data is missing")
		return ""
	}
	if int64(len(encoded)) > b.limits.resource*2 {
		b.diag("limit.image_encoded", DiagnosticSourceInterpretation, loc, "Encoded image exceeds the resource limit")
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		b.diag("resource.image_invalid", DiagnosticSourceInterpretation, loc, "Image data is invalid")
		return ""
	}
	if int64(len(data)) > b.limits.resource || b.resourceBytes+int64(len(data)) > b.limits.resources {
		b.diag("limit.image_decoded", DiagnosticSourceInterpretation, loc, "Decoded images exceed the resource budget")
		return ""
	}
	hash := sha256.Sum256(data)
	if id, ok := b.resources[hash]; ok {
		return id
	}
	media := http.DetectContentType(data)
	width, height := 0, 0
	if cfg, _, e := image.DecodeConfig(bytes.NewReader(data)); e == nil {
		width, height = cfg.Width, cfg.Height
		if width <= 0 || height <= 0 || int64(width)*int64(height) > 100_000_000 {
			b.diag("resource.image_dimensions", DiagnosticSourceInterpretation, loc, "Image dimensions exceed supported bounds")
			return ""
		}
	}
	id := "sha256:" + hex.EncodeToString(hash[:])
	b.doc.Resources = append(b.doc.Resources, Resource{ID: id, SHA256: hash, MediaType: media, Data: append([]byte(nil), data...), Width: width, Height: height})
	b.resources[hash] = id
	b.resourceBytes += int64(len(data))
	return id
}

func (b *modelBuilder) rawUnknown(n *xmlNode) []byte {
	if !b.opts.RetainUnknownXML {
		return nil
	}
	data := nodeRaw(n)
	if int64(len(data)) > b.limits.unknown || b.unknownBytes+int64(len(data)) > b.limits.unknown {
		return nil
	}
	b.unknownBytes += int64(len(data))
	return data
}

func (b *modelBuilder) checkUnknownXML(root *xmlNode) {
	known := map[string]bool{"template": true, "content": true, "properties": true, "pageFormat": true, "elements": true, "styles": true, "style": true, "webID": true, "data": true, "header": true, "footer": true, "paragraph": true, "table": true, "row": true, "cell": true, "field": true, "space": true, "tab": true, "image": true, "page-break": true}
	var walk func(*xmlNode, string)
	walk = func(n *xmlNode, p string) {
		if !known[n.Name.Local] {
			b.diag("source.unknown_element", DiagnosticSourceInterpretation, p, "Unknown XML element was ignored or retained")
			return
		}
		np := p + "." + safeName(n.Name.Local)
		for _, c := range n.Children {
			walk(c, np)
		}
	}
	walk(root, "document")
	knownAttrs := map[string]bool{}
	for _, name := range strings.Fields("format_id resolver id key name description family size bold italic underline strike foreground background Alignment LeftIndent RightIndent FirstLineIndent Hanging LineSpacing SpaceAbove SpaceBelow TabSet Numbered Bulleted NumberType BulletType ListId ListLevel SecListTypeLevel1 GroupName RepeatingLabel startOffset length fieldName fieldType fieldGroupName header isList isErasable width height imageData columnCount columnSpans rowSpans tableName border borderColor borderSpec borderStyle borderWidth rowName rowType colspan rowspan align fillColor mediaSizeName paperOrientation leftMargin rightMargin topMargin bottomMargin headerFOffset footerFOffset pageNumber-spec pageNumber-fontBold pageNumber-fontFace pageNumber-fontItalic pageNumber-fontSize pageNumber-color pageNumber-foreStr pageNumber-pageStartNumStr pageNumber-seperator startPage color-boundary") {
		knownAttrs[name] = true
	}
	var attrs func(*xmlNode, string)
	attrs = func(n *xmlNode, p string) {
		for name := range n.Attr {
			if !knownAttrs[name] {
				b.diag("source.unknown_attribute", DiagnosticSourceInterpretation, p, "Unknown XML attribute was ignored")
			}
		}
		np := p + "." + safeName(n.Name.Local)
		for _, c := range n.Children {
			attrs(c, np)
		}
	}
	attrs(root, "document")
}

func parseBorder(n *xmlNode) Border {
	b := Border{Kind: attr(n, "border"), Style: attr(n, "borderStyle"), Width: numberAttr(n, "borderWidth"), Spec: uintAttr(n, "borderSpec")}
	if v, ok := intAttr(n, "borderColor"); ok {
		b.Color = Opt[int32]{Val: int32(v), Set: true}
	}
	return b
}

func mergeTextStyle(base TextStyle, attrs map[string]string) TextStyle {
	if v, ok := attrs["family"]; ok {
		base.FontFamily = v
	}
	if v, ok := attrs["size"]; ok {
		if n, e := strconv.ParseFloat(v, 64); e == nil {
			base.FontSize = n
		}
	}
	if v, ok := attrs["bold"]; ok {
		base.Bold = strings.EqualFold(v, "true")
	}
	if v, ok := attrs["italic"]; ok {
		base.Italic = strings.EqualFold(v, "true")
	}
	if v, ok := attrs["underline"]; ok {
		base.Underline = strings.EqualFold(v, "true")
	}
	if v, ok := attrs["strike"]; ok {
		base.Strike = strings.EqualFold(v, "true")
	}
	if v, ok := attrs["foreground"]; ok {
		if n, e := strconv.ParseInt(v, 10, 32); e == nil {
			base.Foreground = Opt[int32]{Val: int32(n), Set: true}
		}
	}
	if v, ok := attrs["background"]; ok {
		if n, e := strconv.ParseInt(v, 10, 32); e == nil {
			base.Background = Opt[int32]{Val: int32(n), Set: true}
		}
	}
	return base
}

func textStyleOverrides(attrs map[string]string) TextStyleOverrides {
	var out TextStyleOverrides
	if v, ok := attrs["family"]; ok {
		out.FontFamily = Opt[string]{Val: v, Set: true}
	}
	if v, ok := attrs["size"]; ok {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			out.FontSize = Opt[float64]{Val: n, Set: true}
		}
	}
	for key, target := range map[string]*Opt[bool]{"bold": &out.Bold, "italic": &out.Italic, "underline": &out.Underline, "strike": &out.Strike} {
		if v, ok := attrs[key]; ok {
			*target = Opt[bool]{Val: strings.EqualFold(v, "true"), Set: true}
		}
	}
	if v, ok := attrs["foreground"]; ok {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			out.Foreground = Opt[int32]{Val: int32(n), Set: true}
		}
	}
	if v, ok := attrs["background"]; ok {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			out.Background = Opt[int32]{Val: int32(n), Set: true}
		}
	}
	return out
}

func applyTextOverrides(base TextStyle, o TextStyleOverrides) TextStyle {
	if o.FontFamily.Set {
		base.FontFamily = o.FontFamily.Val
	}
	if o.FontSize.Set {
		base.FontSize = o.FontSize.Val
	}
	if o.Bold.Set {
		base.Bold = o.Bold.Val
	}
	if o.Italic.Set {
		base.Italic = o.Italic.Val
	}
	if o.Underline.Set {
		base.Underline = o.Underline.Val
	}
	if o.Strike.Set {
		base.Strike = o.Strike.Val
	}
	if o.Foreground.Set {
		base.Foreground = o.Foreground
	}
	if o.Background.Set {
		base.Background = o.Background
	}
	return base
}

func mergeParagraphStyle(base ParagraphStyle, attrs map[string]string) ParagraphStyle {
	parsed := parseParagraphStyle(attrs)
	if _, ok := attrs["Alignment"]; ok {
		base.Alignment = parsed.Alignment
	}
	for key, assign := range map[string]func(){
		"LeftIndent": func() { base.LeftIndent = parsed.LeftIndent }, "RightIndent": func() { base.RightIndent = parsed.RightIndent }, "FirstLineIndent": func() { base.FirstLineIndent = parsed.FirstLineIndent }, "Hanging": func() { base.Hanging = parsed.Hanging }, "SpaceAbove": func() { base.SpaceAbove = parsed.SpaceAbove }, "SpaceBelow": func() { base.SpaceBelow = parsed.SpaceBelow }, "LineSpacing": func() { base.LineSpacing = parsed.LineSpacing }, "TabSet": func() { base.Tabs = parsed.Tabs }, "Numbered": func() { base.Numbered = parsed.Numbered }, "Bulleted": func() { base.Bulleted = parsed.Bulleted }, "NumberType": func() { base.NumberType = parsed.NumberType }, "BulletType": func() { base.BulletType = parsed.BulletType }, "ListId": func() { base.ListID = parsed.ListID }, "ListLevel": func() { base.ListLevel = parsed.ListLevel },
	} {
		if _, ok := attrs[key]; ok {
			assign()
		}
	}
	return base
}

func parseParagraphStyle(attrs map[string]string) ParagraphStyle {
	p := ParagraphStyle{NumberType: attrs["NumberType"], BulletType: attrs["BulletType"], ListID: attrs["ListId"]}
	p.LeftIndent, _ = parseFloat(attrs["LeftIndent"])
	p.RightIndent, _ = parseFloat(attrs["RightIndent"])
	p.FirstLineIndent, _ = parseFloat(attrs["FirstLineIndent"])
	p.Hanging, _ = parseFloat(attrs["Hanging"])
	p.SpaceAbove, _ = parseFloat(attrs["SpaceAbove"])
	p.SpaceBelow, _ = parseFloat(attrs["SpaceBelow"])
	p.LineSpacing, _ = parseFloat(attrs["LineSpacing"])
	p.Alignment = parseAlignment(attrs["Alignment"])
	p.Numbered = strings.EqualFold(attrs["Numbered"], "true")
	p.Bulleted = strings.EqualFold(attrs["Bulleted"], "true")
	p.ListLevel, _ = strconv.Atoi(attrs["ListLevel"])
	if value := attrs["TabSet"]; value != "" {
		p.Tabs = parseTabs(value)
	}
	return p
}

func parseAlignment(value string) Alignment {
	switch value {
	case "0":
		return AlignLeft
	case "1":
		return AlignCenter
	case "2":
		return AlignRight
	case "3":
		return AlignJustify
	}
	return AlignUnspecified
}

func parseTabs(value string) []TabStop {
	var out []TabStop
	for _, part := range strings.Split(value, ",") {
		fields := strings.Split(part, ":")
		if len(fields) != 3 {
			continue
		}
		pos, e := strconv.ParseFloat(fields[0], 64)
		if e != nil {
			continue
		}
		align := map[string]string{"0": "left", "1": "center", "2": "right", "3": "decimal"}[fields[1]]
		leader := map[string]string{"0": "none", "1": "dot", "2": "dash"}[fields[2]]
		out = append(out, TabStop{Position: pos, Alignment: align, Leader: leader})
	}
	return out
}

func makeUTF16Map(text string) []int32 {
	units := 0
	for _, r := range text {
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
	}
	out := make([]int32, units+1)
	for i := range out {
		out[i] = -1
	}
	unit := 0
	for byteStart, r := range text {
		out[unit] = int32(byteStart)
		if r > 0xffff {
			unit++
			out[unit] = -1
			unit++
		} else {
			unit++
		}
		out[unit] = int32(byteStart + len(string(r)))
	}
	return out
}

func spanAttrs(n *xmlNode) (int, int, bool, error) {
	s, l := attr(n, "startOffset"), attr(n, "length")
	if s == "" && l == "" {
		return 0, 0, false, nil
	}
	if s == "" || l == "" {
		return 0, 0, true, ErrInvalidOffset
	}
	start, e1 := strconv.Atoi(s)
	length, e2 := strconv.Atoi(l)
	if e1 != nil || e2 != nil || start < 0 || length < 0 {
		return 0, 0, true, ErrInvalidOffset
	}
	return start, length, true, nil
}

func normalizeGrid(t *Table, maxGridCells int64) error {
	occupied := map[[2]int]bool{}
	var filled int64
	maxCol := 0
	for ri := range t.Rows {
		col := 0
		for ci := range t.Rows[ri].Cells {
			cell := &t.Rows[ri].Cells[ci]
			if cell.RowSpan < 1 || cell.ColSpan < 1 || cell.RowSpan > 1000 || cell.ColSpan > 1000 {
				return ErrInvalidModel
			}
			for occupied[[2]int{ri, col}] {
				col++
			}
			cell.GridRow, cell.GridCol = ri, col
			for rr := ri; rr < ri+cell.RowSpan; rr++ {
				for cc := col; cc < col+cell.ColSpan; cc++ {
					filled++
					if filled > maxGridCells {
						return ErrLimitExceeded
					}
					key := [2]int{rr, cc}
					if occupied[key] {
						return ErrInvalidModel
					}
					occupied[key] = true
				}
			}
			col += cell.ColSpan
			if col > maxCol {
				maxCol = col
			}
		}
	}
	if t.ColumnCount == 0 {
		t.ColumnCount = maxCol
	}
	if int64(t.ColumnCount) > maxGridCells {
		return ErrLimitExceeded
	}
	if t.ColumnCount < maxCol {
		return ErrInvalidModel
	}
	return nil
}

func parseFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	v, e := strconv.ParseFloat(s, 64)
	if e == nil && (math.IsNaN(v) || math.IsInf(v, 0)) {
		return 0, errors.New("invalid number")
	}
	return v, e
}
func floatAttr(n *xmlNode, key string) (float64, error) { return parseFloat(attr(n, key)) }
func numberAttr(n *xmlNode, key string) float64         { v, _ := floatAttr(n, key); return v }
func intAttr(n *xmlNode, key string) (int, bool) {
	v, e := strconv.Atoi(attr(n, key))
	return v, e == nil
}
func uintAttr(n *xmlNode, key string) uint32 {
	v, e := strconv.ParseUint(attr(n, key), 10, 32)
	if e != nil {
		return 0
	}
	return uint32(v)
}
func intColor(n *xmlNode, key string) Opt[int32] {
	v, ok := intAttr(n, key)
	if !ok {
		return Opt[int32]{}
	}
	return Opt[int32]{Val: int32(v), Set: true}
}
func safeName(s string) string {
	if safeXMLName.MatchString(s) {
		return s
	}
	return "unknown"
}

var _ = errors.Is
