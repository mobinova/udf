// Package udf writes an unsigned UYAP UDF derivative from the normalized model.
// It does not sign output or transfer a source signature.
package udf

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	model "github.com/mobinova/udf"
)

const verificationCodeKey = "uyapdogrulamakodu"

// Options controls deliberate loss when producing an unsigned UDF derivative.
type Options struct {
	// AllowUnsigned is required when the source document has sign.sgn.
	AllowUnsigned bool
	// AllowLossy permits the writer to omit source details the model did not retain.
	AllowLossy bool
}

type Exporter struct{ Options Options }

func Export(ctx context.Context, doc *model.Document, dst io.Writer) (model.ExportReport, error) {
	return (Exporter{}).Export(ctx, doc, dst)
}

func ExportWithOptions(ctx context.Context, doc *model.Document, dst io.Writer, options Options) (model.ExportReport, error) {
	return (Exporter{Options: options}).Export(ctx, doc, dst)
}

func (e Exporter) Export(ctx context.Context, doc *model.Document, dst io.Writer) (model.ExportReport, error) {
	var report model.ExportReport
	if err := model.Validate(doc); err != nil {
		return report, err
	}
	if dst == nil {
		return report, fmt.Errorf("udf export: nil writer")
	}
	if err := contextError(ctx); err != nil {
		return report, err
	}
	state := &encoderState{ctx: ctx, options: e.Options, doc: doc, resources: make(map[string]model.Resource, len(doc.Resources))}
	for _, resource := range doc.Resources {
		state.resources[resource.ID] = resource
	}
	if doc.Signature.Present {
		if !e.Options.AllowUnsigned {
			state.add("signature.not_transferred", model.DiagnosticSignatureNotTransferred, "signature", "Writing an unsigned UDF requires explicit AllowUnsigned opt-in")
			return state.report(), model.ErrUnsupported
		}
		state.add("signature.not_transferred", model.DiagnosticSignatureNotTransferred, "signature", "The output UDF is unsigned; source signature bytes are not transferred")
	}
	for _, diagnostic := range doc.Diagnostics {
		if isUnretainedSourceDetail(diagnostic.Code) {
			state.add("source.details_not_preserved", model.DiagnosticContentLoss, diagnostic.Location, "The normalized model does not retain all source XML details")
			break
		}
	}
	root, content, err := state.buildXML()
	if err != nil {
		return state.report(), err
	}
	contentNode := &xmlNode{Name: "content", Text: content}
	root.Children = append([]*xmlNode{contentNode}, root.Children...)
	contentXML, err := marshalXML(root)
	if err != nil {
		return state.report(), err
	}
	propertiesXML, err := state.marshalDocumentProperties(doc)
	if err != nil {
		return state.report(), err
	}
	if state.cancelErr != nil {
		return state.report(), state.cancelErr
	}
	if state.hasContentLoss && !e.Options.AllowLossy {
		return state.report(), model.ErrUnsupported
	}
	zw := zip.NewWriter(dst)
	if err := writeZipEntry(zw, "content.xml", contentXML); err != nil {
		_ = zw.Close()
		return state.report(), err
	}
	if len(propertiesXML) > 0 {
		if err := writeZipEntry(zw, "documentproperties.xml", propertiesXML); err != nil {
			_ = zw.Close()
			return state.report(), err
		}
	}
	if err := zw.Close(); err != nil {
		return state.report(), fmt.Errorf("udf export: finalize archive failed")
	}
	return state.report(), nil
}

func isUnretainedSourceDetail(code string) bool {
	return code == "source.unknown_element" || code == "source.unknown_attribute" || code == "source.template_data_unsupported" || code == "table.row_spans_unresolved" || code == "table.column_widths_unresolved" || code == "layout.nested_header_footer"
}

type encoderState struct {
	ctx            context.Context
	options        Options
	doc            *model.Document
	resources      map[string]model.Resource
	pool           strings.Builder
	units          int
	diagnostics    []model.Diagnostic
	hasContentLoss bool
	cancelErr      error
}

type xmlNode struct {
	Name     string
	Attr     map[string]string
	Text     string
	Children []*xmlNode
}

func node(name string, attrs map[string]string, children ...*xmlNode) *xmlNode {
	if attrs == nil {
		attrs = map[string]string{}
	}
	return &xmlNode{Name: name, Attr: attrs, Children: children}
}

func (s *encoderState) report() model.ExportReport {
	return model.ExportReport{Diagnostics: append([]model.Diagnostic(nil), s.diagnostics...)}
}

func (s *encoderState) add(code string, class model.DiagnosticClass, location, message string) {
	if class == model.DiagnosticContentLoss {
		s.hasContentLoss = true
	}
	severity := model.SeverityWarning
	if class == model.DiagnosticContentLoss {
		severity = model.SeverityError
	}
	d := model.Diagnostic{Code: code, Severity: severity, Class: class, Location: location, Message: message}
	const maxDiagnostics = 512
	if len(s.diagnostics) < maxDiagnostics {
		s.diagnostics = append(s.diagnostics, d)
		return
	}
	s.diagnostics[maxDiagnostics-1] = model.Diagnostic{Code: "diagnostics.truncated", Severity: model.SeverityWarning, Class: model.DiagnosticNormalization, Location: "document", Message: "Additional export diagnostics were omitted by the limit"}
}

func (s *encoderState) buildXML() (*xmlNode, string, error) {
	format := s.doc.FormatID
	if format == "" {
		format = "1.8"
	}
	root := node("template", map[string]string{"format_id": format})
	properties := node("properties", nil, s.pageFormat())
	styles := node("styles", nil)
	for _, style := range s.doc.Styles {
		styles.Children = append(styles.Children, s.styleNode(style))
	}
	elements := node("elements", nil)
	for i, header := range s.doc.Headers {
		if err := contextError(s.ctx); err != nil {
			return nil, "", err
		}
		elements.Children = append(elements.Children, s.headerFooterNode(header, fmt.Sprintf("headers[%d]", i)))
	}
	for i, block := range s.doc.Body {
		if err := contextError(s.ctx); err != nil {
			return nil, "", err
		}
		n, err := s.blockNode(block, fmt.Sprintf("body[%d]", i))
		if err != nil {
			return nil, "", err
		}
		elements.Children = append(elements.Children, n...)
	}
	for i, footer := range s.doc.Footers {
		if err := contextError(s.ctx); err != nil {
			return nil, "", err
		}
		elements.Children = append(elements.Children, s.headerFooterNode(footer, fmt.Sprintf("footers[%d]", i)))
	}
	root.Children = append(root.Children, properties, elements, styles)
	if s.doc.WebID != "" {
		root.Children = append(root.Children, node("webID", map[string]string{"id": s.doc.WebID}))
	}
	return root, s.pool.String(), nil
}

func (s *encoderState) pageFormat() *xmlNode {
	a := map[string]string{}
	p := s.doc.Page
	if p.MediaSizeName != "" {
		a["mediaSizeName"] = p.MediaSizeName
	}
	if p.Orientation == model.Portrait {
		a["paperOrientation"] = "1"
	} else if p.Orientation == model.Landscape {
		a["paperOrientation"] = "0"
	}
	for key, value := range map[string]float64{"leftMargin": p.LeftMargin, "rightMargin": p.RightMargin, "topMargin": p.TopMargin, "bottomMargin": p.BottomMargin, "headerFOffset": p.HeaderDistance, "footerFOffset": p.FooterDistance} {
		if value != 0 {
			a[key] = number(value)
		}
	}
	return node("pageFormat", a)
}

func (s *encoderState) styleNode(style model.Style) *xmlNode {
	a := map[string]string{"name": style.Name}
	if style.Description != "" {
		a["description"] = style.Description
	}
	addTextStyle(a, style.Text)
	addParagraphStyle(a, style.Paragraph)
	return node("style", a)
}

func (s *encoderState) headerFooterNode(section model.HeaderFooter, location string) *xmlNode {
	name := section.Kind
	if name != "header" && name != "footer" {
		name = "header"
		s.add("header_footer.kind_normalized", model.DiagnosticNormalization, location, "Unknown header/footer kind was emitted as a header")
	}
	a := map[string]string{}
	if section.StartPage > 0 {
		a["startPage"] = strconv.Itoa(section.StartPage)
	}
	children := s.blockNodes(section.Blocks, location)
	return node(name, a, children...)
}

func (s *encoderState) blockNodes(blocks []model.Block, location string) []*xmlNode {
	var out []*xmlNode
	for i, block := range blocks {
		if err := contextError(s.ctx); err != nil {
			s.cancelErr = err
			return out
		}
		nodes, err := s.blockNode(block, fmt.Sprintf("%s[%d]", location, i))
		if err != nil {
			s.add("content.block_omitted", model.DiagnosticContentLoss, fmt.Sprintf("%s[%d]", location, i), "Block could not be serialized")
			continue
		}
		out = append(out, nodes...)
	}
	return out
}

func (s *encoderState) blockNode(block model.Block, location string) ([]*xmlNode, error) {
	switch block.Kind {
	case model.BlockParagraph:
		if block.Paragraph == nil {
			return nil, model.ErrInvalidModel
		}
		return []*xmlNode{s.paragraphNode(block.Paragraph, location)}, nil
	case model.BlockTable:
		if block.Table == nil {
			return nil, model.ErrInvalidModel
		}
		n, err := s.tableNode(block.Table, location)
		return []*xmlNode{n}, err
	case model.BlockPageBreak:
		return []*xmlNode{node("page-break", nil)}, nil
	case model.BlockUnknown:
		if block.Unknown == nil {
			return nil, model.ErrInvalidModel
		}
		if block.Unknown.Text == "" {
			s.add("content.unknown_block_omitted", model.DiagnosticContentLoss, location, "Unknown block has no text representation")
			if s.options.AllowLossy {
				return []*xmlNode{s.paragraphNode(&model.Paragraph{}, location)}, nil
			}
			return nil, model.ErrUnsupported
		}
		s.add("content.unknown_block_structure", model.DiagnosticVisualApproximation, location, "Unknown block was serialized as a paragraph containing its readable text")
		p := &model.Paragraph{Inlines: []model.Inline{{Kind: model.InlineRun, Run: &model.TextRun{Text: block.Unknown.Text}}}}
		return []*xmlNode{s.paragraphNode(p, location)}, nil
	default:
		s.add("content.unknown_block_kind", model.DiagnosticContentLoss, location, "Unknown block kind cannot be serialized")
		if s.options.AllowLossy {
			return []*xmlNode{s.paragraphNode(&model.Paragraph{}, location)}, nil
		}
		return nil, model.ErrUnsupported
	}
}

func (s *encoderState) paragraphNode(p *model.Paragraph, location string) *xmlNode {
	a := map[string]string{}
	if p.StyleName != "" {
		a["resolver"] = p.StyleName
	}
	addParagraphStyle(a, p.Properties)
	children := make([]*xmlNode, 0, len(p.Inlines)+1)
	for i, in := range p.Inlines {
		loc := fmt.Sprintf("%s.inlines[%d]", location, i)
		if n := s.inlineNode(in, loc); n != nil {
			children = append(children, n)
		}
	}
	// Every serialized UDF paragraph receives a source-buffer terminator. The
	// parser treats this final LF as structural and omits it from Text().
	children = append(children, s.spanNode("content", "\n", nil))
	return node("paragraph", a, children...)
}

func (s *encoderState) inlineNode(in model.Inline, location string) *xmlNode {
	switch in.Kind {
	case model.InlineRun:
		if in.Run == nil {
			return nil
		}
		attrs := map[string]string{}
		addTextStyle(attrs, in.Run.Style)
		if in.Run.Text == "" {
			return nil
		}
		return s.spanNode("content", in.Run.Text, attrs)
	case model.InlineField:
		if in.Field == nil {
			return nil
		}
		attrs := map[string]string{"fieldName": in.Field.Name, "fieldType": in.Field.Type, "fieldGroupName": in.Field.Group}
		addTextStyle(attrs, in.Field.Style)
		if in.Field.Text == "" {
			s.add("field.empty_omitted", model.DiagnosticVisualApproximation, location, "Empty field element was omitted")
			return nil
		}
		return s.spanNode("field", in.Field.Text, attrs)
	case model.InlineTab:
		if in.Tab == nil {
			return nil
		}
		text := in.Tab.Text
		if text == "" {
			text = "\t"
		}
		return s.spanNode("tab", text, nil)
	case model.InlineBreak:
		if in.Break == nil {
			return nil
		}
		text := in.Break.Text
		if text == "" {
			text = "\n"
		}
		return s.spanNode("content", text, nil)
	case model.InlineImage:
		if in.Image == nil {
			return nil
		}
		resource, ok := s.resources[in.Image.ResourceID]
		if !ok {
			s.add("resource.image_omitted", model.DiagnosticContentLoss, location, "Image resource is unavailable")
			return nil
		}
		a := map[string]string{"imageData": base64.StdEncoding.EncodeToString(resource.Data), "width": number(in.Image.Width), "height": number(in.Image.Height)}
		start, length := s.appendText("\uFFFC")
		a["startOffset"], a["length"] = strconv.Itoa(start), strconv.Itoa(length)
		return node("image", a)
	case model.InlineUnknown:
		if in.Unknown == nil {
			return nil
		}
		if in.Unknown.Text == "" {
			s.add("content.unknown_inline_omitted", model.DiagnosticContentLoss, location, "Unknown inline item has no text representation")
			return nil
		}
		s.add("content.unknown_inline_structure", model.DiagnosticVisualApproximation, location, "Unknown inline item was serialized as a text run")
		return s.spanNode("content", in.Unknown.Text, nil)
	default:
		s.add("content.unknown_inline_kind", model.DiagnosticContentLoss, location, "Unknown inline kind cannot be serialized")
		return nil
	}
}

func (s *encoderState) spanNode(name, text string, attrs map[string]string) *xmlNode {
	if attrs == nil {
		attrs = map[string]string{}
	}
	start, length := s.appendText(text)
	attrs["startOffset"], attrs["length"] = strconv.Itoa(start), strconv.Itoa(length)
	return node(name, attrs)
}

func (s *encoderState) appendText(text string) (int, int) {
	start := s.units
	s.pool.WriteString(text)
	length := utf16Length(text)
	s.units += length
	return start, length
}

func (s *encoderState) tableNode(t *model.Table, location string) (*xmlNode, error) {
	a := map[string]string{"columnCount": strconv.Itoa(t.ColumnCount)}
	if t.Name != "" {
		a["tableName"] = t.Name
	}
	if len(t.ColumnWidths) > 0 {
		var widths []string
		for _, width := range t.ColumnWidths {
			if width.Unit != model.UnitPoint {
				s.add("table.width_unit_omitted", model.DiagnosticContentLoss, location, "Only point-valued UDF table widths can be serialized")
				if !s.options.AllowLossy {
					return nil, model.ErrUnsupported
				}
				continue
			}
			widths = append(widths, number(width.Value))
		}
		if len(widths) > 0 {
			a["columnSpans"] = strings.Join(widths, ",")
		}
	}
	table := node("table", a)
	for ri, row := range t.Rows {
		ra := map[string]string{}
		if row.Name != "" {
			ra["rowName"] = row.Name
		}
		if row.Type != "" {
			ra["rowType"] = row.Type
		}
		if row.Height != 0 {
			ra["height"] = number(row.Height)
		}
		rn := node("row", ra)
		for ci, cell := range row.Cells {
			ca := map[string]string{}
			if cell.RowSpan > 1 {
				ca["rowspan"] = strconv.Itoa(cell.RowSpan)
			}
			if cell.ColSpan > 1 {
				ca["colspan"] = strconv.Itoa(cell.ColSpan)
			}
			if cell.VerticalAlign != "" {
				ca["align"] = cell.VerticalAlign
			}
			if cell.Background.Set {
				ca["fillColor"] = strconv.FormatInt(int64(cell.Background.Val), 10)
			}
			if cell.Border.Kind != "" {
				ca["border"] = cell.Border.Kind
			}
			if cell.Border.Style != "" {
				ca["borderStyle"] = cell.Border.Style
			}
			if cell.Border.Width != 0 {
				ca["borderWidth"] = number(cell.Border.Width)
			}
			if cell.Border.Color.Set {
				ca["borderColor"] = strconv.FormatInt(int64(cell.Border.Color.Val), 10)
			}
			if cell.Border.Spec != 0 {
				ca["borderSpec"] = strconv.FormatUint(uint64(cell.Border.Spec), 10)
			}
			cn := node("cell", ca)
			cn.Children = s.blockNodes(cell.Blocks, fmt.Sprintf("%s.rows[%d].cells[%d]", location, ri, ci))
			rn.Children = append(rn.Children, cn)
		}
		table.Children = append(table.Children, rn)
	}
	return table, nil
}

func addTextStyle(attrs map[string]string, style model.TextStyle) {
	if style.FontFamily != "" {
		attrs["family"] = style.FontFamily
	}
	if style.FontSize > 0 {
		attrs["size"] = number(style.FontSize)
	}
	attrs["bold"] = strconv.FormatBool(style.Bold)
	attrs["italic"] = strconv.FormatBool(style.Italic)
	attrs["underline"] = strconv.FormatBool(style.Underline)
	if style.Foreground.Set {
		attrs["foreground"] = strconv.FormatInt(int64(style.Foreground.Val), 10)
	}
	if style.Background.Set {
		attrs["background"] = strconv.FormatInt(int64(style.Background.Val), 10)
	}
}

func addParagraphStyle(attrs map[string]string, p model.ParagraphStyle) {
	alignment := map[model.Alignment]string{model.AlignLeft: "0", model.AlignCenter: "1", model.AlignRight: "2", model.AlignJustify: "3"}
	if v := alignment[p.Alignment]; v != "" {
		attrs["Alignment"] = v
	}
	for key, value := range map[string]float64{"LeftIndent": p.LeftIndent, "RightIndent": p.RightIndent, "FirstLineIndent": p.FirstLineIndent, "Hanging": p.Hanging, "SpaceAbove": p.SpaceAbove, "SpaceBelow": p.SpaceBelow, "LineSpacing": p.LineSpacing} {
		if value != 0 {
			attrs[key] = number(value)
		}
	}
	if p.Numbered {
		attrs["Numbered"] = "true"
	}
	if p.Bulleted {
		attrs["Bulleted"] = "true"
	}
	if p.NumberType != "" {
		attrs["NumberType"] = p.NumberType
	}
	if p.BulletType != "" {
		attrs["BulletType"] = p.BulletType
	}
	if p.ListID != "" {
		attrs["ListId"] = p.ListID
	}
	if p.ListLevel != 0 {
		attrs["ListLevel"] = strconv.Itoa(p.ListLevel)
	}
	if len(p.Tabs) > 0 {
		var stops []string
		for _, tab := range p.Tabs {
			align := map[string]string{"left": "0", "center": "1", "right": "2", "decimal": "3"}[tab.Alignment]
			leader := map[string]string{"none": "0", "dot": "1", "dash": "2"}[tab.Leader]
			if align == "" || leader == "" {
				continue
			}
			stops = append(stops, number(tab.Position)+":"+align+":"+leader)
		}
		if len(stops) > 0 {
			attrs["TabSet"] = strings.Join(stops, ",")
		}
	}
}

func marshalXML(root *xmlNode) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(xml.Header)
	enc := xml.NewEncoder(&out)
	if err := encodeNode(enc, root); err != nil {
		return nil, fmt.Errorf("udf export: encode content XML failed")
	}
	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("udf export: encode content XML failed")
	}
	return out.Bytes(), nil
}

func encodeNode(enc *xml.Encoder, n *xmlNode) error {
	attrs := make([]xml.Attr, 0, len(n.Attr))
	keys := make([]string, 0, len(n.Attr))
	for key := range n.Attr {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: n.Attr[key]})
	}
	start := xml.StartElement{Name: xml.Name{Local: n.Name}, Attr: attrs}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	if n.Text != "" {
		if err := enc.EncodeToken(xml.CharData([]byte(n.Text))); err != nil {
			return err
		}
	}
	for _, child := range n.Children {
		if err := encodeNode(enc, child); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func (s *encoderState) marshalDocumentProperties(doc *model.Document) ([]byte, error) {
	keys := make([]string, 0, len(doc.Properties.Values))
	for key := range doc.Properties.Values {
		if key == verificationCodeKey {
			s.add("metadata.verification_code_omitted", model.DiagnosticContentLoss, "properties.verification_code", "The source verification code was omitted from the unsigned derivative")
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, nil
	}
	root := node("properties", nil)
	for _, key := range keys {
		root.Children = append(root.Children, &xmlNode{Name: "entry", Attr: map[string]string{"key": key}, Text: doc.Properties.Values[key]})
	}
	data, err := marshalXML(root)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("udf export: create archive entry failed")
	}
	if _, err = w.Write(data); err != nil {
		return fmt.Errorf("udf export: write archive entry failed")
	}
	return nil
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}
func number(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
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
