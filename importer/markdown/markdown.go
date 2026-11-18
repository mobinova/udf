// Package markdown imports CommonMark and GitHub Flavored Markdown into the
// normalized UDF document model.
package markdown

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	textm "github.com/yuin/goldmark/text"

	udf "github.com/mobinova/udf"
)

const (
	defaultMaxInputBytes = 32 << 20
	defaultMaxNodes      = 200000
	defaultMaxDepth      = 128
	maxImportDiagnostics = 512
)

// Options bounds Markdown parsing. Non-positive limits use the defaults.
type Options struct {
	MaxInputBytes int64
	MaxNodes      int
	MaxDepth      int
}

// Importer parses Markdown without loading linked files or fetching URLs.
type Importer struct {
	Options Options
}

var _ udf.Importer = Importer{}

type importState struct {
	ctx         context.Context
	source      []byte
	doc         *udf.Document
	maxNodes    int
	maxDepth    int
	nodes       int
	listID      int
	paragraphID int
}

func (i Importer) Import(ctx context.Context, src io.Reader) (*udf.Document, error) {
	if src == nil {
		return nil, fmt.Errorf("markdown import: nil reader")
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	maxBytes := i.Options.MaxInputBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxInputBytes
	}
	data, err := io.ReadAll(io.LimitReader(src, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("markdown import: read input: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, udf.ErrLimitExceeded
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("markdown import: input is not valid UTF-8: %w", udf.ErrInvalidXML)
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	maxNodes := i.Options.MaxNodes
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	maxDepth := i.Options.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	root := parser.Parse(textm.NewReader(data))
	s := &importState{
		ctx:      ctx,
		source:   data,
		doc:      &udf.Document{FormatID: "markdown", Properties: udf.DocumentProperties{Values: map[string]string{}}},
		maxNodes: maxNodes,
		maxDepth: maxDepth,
	}
	if err := s.checkTree(root); err != nil {
		return nil, err
	}
	s.doc.Body = s.parseBlocks(root, 0, nil, false)
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := udf.Validate(s.doc); err != nil {
		return nil, fmt.Errorf("markdown import produced invalid model: %w", err)
	}
	return s.doc, nil
}

func Import(ctx context.Context, src io.Reader) (*udf.Document, error) {
	return (Importer{}).Import(ctx, src)
}

func ImportWithOptions(ctx context.Context, src io.Reader, options Options) (*udf.Document, error) {
	return (Importer{Options: options}).Import(ctx, src)
}

func (s *importState) checkTree(root ast.Node) error {
	depth := 0
	return ast.Walk(root, func(_ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			depth++
			s.nodes++
			if depth > s.maxDepth || s.nodes > s.maxNodes {
				return ast.WalkStop, udf.ErrLimitExceeded
			}
		} else {
			depth--
		}
		return ast.WalkContinue, nil
	})
}

func (s *importState) parseBlocks(parent ast.Node, depth int, list *listProperties, quote bool) []udf.Block {
	var blocks []udf.Block
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if contextError(s.ctx) != nil {
			return blocks
		}
		switch node := child.(type) {
		case *ast.Paragraph:
			blocks = append(blocks, s.paragraph(node, "", list, quote))
		case *ast.TextBlock:
			blocks = append(blocks, s.paragraph(node, "", list, quote))
		case *ast.Heading:
			blocks = append(blocks, s.paragraph(node, fmt.Sprintf("Heading %d", node.Level), list, quote))
		case *ast.List:
			blocks = append(blocks, s.parseList(node, depth, quote)...)
		case *ast.Blockquote:
			s.add("markdown.blockquote_approximated", udf.DiagnosticVisualApproximation, "body", "Block quote semantics are represented through the paragraph style name")
			blocks = append(blocks, s.parseBlocks(node, depth, list, true)...)
		case *extast.Table:
			blocks = append(blocks, s.parseTable(node, depth, quote))
		case *ast.FencedCodeBlock:
			blocks = append(blocks, s.codeBlock(node, true))
		case *ast.CodeBlock:
			blocks = append(blocks, s.codeBlock(node, false))
		case *ast.ThematicBreak:
			s.add("markdown.thematic_break_unmodeled", udf.DiagnosticVisualApproximation, "body", "Thematic break has no dedicated document-model block")
			blocks = append(blocks, udf.Block{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "markdown_thematic_break"}})
		case *ast.HTMLBlock:
			text := string(node.Lines().Value(s.source))
			s.add("markdown.raw_html_preserved_as_text", udf.DiagnosticVisualApproximation, "body", "Raw HTML is retained as text and is not interpreted")
			blocks = append(blocks, udf.Block{Kind: udf.BlockUnknown, Unknown: &udf.UnknownBlock{Element: "markdown_html", Text: text}})
		default:
			if child.HasChildren() {
				blocks = append(blocks, s.parseBlocks(child, depth, list, quote)...)
			}
		}
	}
	return blocks
}

type listProperties struct {
	ordered bool
	start   int
	level   int
	id      string
	typeTag string
}

func (s *importState) parseList(listNode *ast.List, depth int, quote bool) []udf.Block {
	s.listID++
	props := &listProperties{
		ordered: listNode.IsOrdered(),
		start:   listNode.Start,
		level:   depth,
		id:      fmt.Sprintf("markdown-list-%d", s.listID),
	}
	if props.ordered {
		props.typeTag = "NUMBER_TYPE_NUMBER_DOT"
		if listNode.Marker == ')' {
			props.typeTag = "NUMBER_TYPE_NUMBER_PARENTHESIS"
		}
		if listNode.Start > 1 {
			s.add("markdown.list_start_approximated", udf.DiagnosticVisualApproximation, "body", "List start number is not represented by the shared model")
		}
	}
	var blocks []udf.Block
	for item := listNode.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		itemBlockStart := len(blocks)
		for child := listItem.FirstChild(); child != nil; child = child.NextSibling() {
			switch node := child.(type) {
			case *ast.Paragraph:
				blocks = append(blocks, s.paragraph(node, "", props, quote))
			case *ast.TextBlock:
				blocks = append(blocks, s.paragraph(node, "", props, quote))
			case *ast.List:
				blocks = append(blocks, s.parseList(node, depth+1, quote)...)
			default:
				blocks = append(blocks, s.parseBlocks(child, depth+1, props, quote)...)
			}
		}
		if len(blocks)-itemBlockStart > 1 {
			s.add("markdown.multiblock_list_item_approximated", udf.DiagnosticVisualApproximation, "body", "Multiple blocks in a list item are flattened into sequential list paragraphs")
		}
	}
	return blocks
}

func (s *importState) paragraph(node ast.Node, styleName string, list *listProperties, quote bool) udf.Block {
	p := &udf.Paragraph{StyleName: styleName}
	if quote && p.StyleName == "" {
		p.StyleName = "Quote"
	}
	if list != nil {
		p.Properties.ListID = list.id
		p.Properties.ListLevel = list.level
		if list.ordered {
			p.Properties.Numbered = true
			p.Properties.NumberType = list.typeTag
		} else {
			p.Properties.Bulleted = true
		}
	}
	p.Inlines = s.inlines(node, udf.TextStyle{})
	return udf.Block{Kind: udf.BlockParagraph, Paragraph: p}
}

func (s *importState) codeBlock(node ast.Node, fenced bool) udf.Block {
	text := string(node.Lines().Value(s.source))
	text = strings.TrimSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\r")
	if fenced {
		s.add("markdown.code_style_omitted", udf.DiagnosticVisualApproximation, "body", "Code block text is preserved, but code font styling is not represented")
	} else {
		s.add("markdown.indented_code_style_omitted", udf.DiagnosticVisualApproximation, "body", "Indented code text is preserved without code styling")
	}
	return udf.Block{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{
		StyleName: "CodeBlock",
		Inlines:   []udf.Inline{{Kind: udf.InlineRun, Run: &udf.TextRun{Text: text}}},
	}}
}

func (s *importState) parseTable(tableNode *extast.Table, depth int, quote bool) udf.Block {
	table := &udf.Table{}
	if len(tableNode.Alignments) > 0 {
		table.ColumnCount = len(tableNode.Alignments)
	}
	for child := tableNode.FirstChild(); child != nil; child = child.NextSibling() {
		rowType := "dataRow"
		var row ast.Node
		if header, ok := child.(*extast.TableHeader); ok {
			row = header
			rowType = "headerRow"
		} else if dataRow, ok := child.(*extast.TableRow); ok {
			row = dataRow
		} else {
			continue
		}
		modelRow := udf.TableRow{Type: rowType}
		column := 0
		for cellNode := row.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
			cellAST, ok := cellNode.(*extast.TableCell)
			if !ok {
				continue
			}
			cell := udf.TableCell{
				GridRow: tableRowIndex(table), GridCol: column,
				RowSpan: 1, ColSpan: 1,
				Blocks: []udf.Block{{Kind: udf.BlockParagraph, Paragraph: &udf.Paragraph{StyleName: quoteStyle(quote), Inlines: s.inlines(cellAST, udf.TextStyle{})}}},
			}
			if cellAST.Alignment != extast.AlignNone {
				s.add("markdown.table_alignment_omitted", udf.DiagnosticVisualApproximation, "body", "Markdown table cell alignment is not represented by the shared model")
			}
			modelRow.Cells = append(modelRow.Cells, cell)
			column++
		}
		if column > table.ColumnCount {
			table.ColumnCount = column
		}
		table.Rows = append(table.Rows, modelRow)
	}
	for i := range table.Rows {
		for j := range table.Rows[i].Cells {
			table.Rows[i].Cells[j].GridRow = i
		}
	}
	for i := 0; i < table.ColumnCount; i++ {
		table.ColumnWidths = append(table.ColumnWidths, udf.ColumnWidth{Unit: udf.UnitAuto})
	}
	return udf.Block{Kind: udf.BlockTable, Table: table}
}

func tableRowIndex(table *udf.Table) int { return len(table.Rows) }

func quoteStyle(quote bool) string {
	if quote {
		return "Quote"
	}
	return ""
}

func (s *importState) inlines(parent ast.Node, style udf.TextStyle) []udf.Inline {
	var result []udf.Inline
	appendRun := func(text string, runStyle udf.TextStyle) {
		if text == "" {
			return
		}
		if len(result) > 0 && result[len(result)-1].Kind == udf.InlineRun && result[len(result)-1].Run.Style == runStyle {
			result[len(result)-1].Run.Text += text
			return
		}
		result = append(result, udf.Inline{Kind: udf.InlineRun, Run: &udf.TextRun{Text: text, Style: runStyle}})
	}
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *ast.Text:
			appendRun(string(node.Value(s.source)), style)
			if node.HardLineBreak() {
				result = append(result, udf.Inline{Kind: udf.InlineBreak, Break: &udf.LineBreak{Text: "\n"}})
			} else if node.SoftLineBreak() {
				appendRun(" ", style)
			}
		case *ast.String:
			appendRun(string(node.Value), style)
		case *ast.Emphasis:
			nested := style
			if node.Level >= 1 {
				nested.Italic = true
			}
			if node.Level >= 2 {
				nested.Bold = true
			}
			result = append(result, s.inlines(node, nested)...)
		case *extast.Strikethrough:
			nested := style
			nested.Strike = true
			result = append(result, s.inlines(node, nested)...)
		case *ast.CodeSpan:
			s.add("markdown.code_style_omitted", udf.DiagnosticVisualApproximation, "body", "Inline code text is preserved without code font styling")
			appendRun(string(node.Text(s.source)), style)
		case *ast.Link:
			s.add("markdown.link_target_omitted", udf.DiagnosticVisualApproximation, "body", "Link label is preserved but its destination is not represented")
			result = append(result, s.inlines(node, style)...)
		case *ast.AutoLink:
			url := string(node.URL(s.source))
			result = append(result, udf.Inline{Kind: udf.InlineRun, Run: &udf.TextRun{Text: url, Style: style}})
		case *ast.Image:
			alt := strings.TrimSpace(string(node.Text(s.source)))
			s.add("markdown.image_not_embedded", udf.DiagnosticVisualApproximation, "body", "Markdown image was not fetched; only its alt text is retained")
			if alt == "" {
				s.add("markdown.image_alt_missing", udf.DiagnosticContentLoss, "body", "Markdown image has no alt text")
			}
			result = append(result, udf.Inline{Kind: udf.InlineUnknown, Unknown: &udf.UnknownInline{Element: "markdown_image", Text: alt}})
		case *extast.TaskCheckBox:
			mark := "[ ] "
			if node.IsChecked {
				mark = "[x] "
			}
			s.add("markdown.task_checkbox_approximated", udf.DiagnosticVisualApproximation, "body", "Task checkbox is represented as text")
			appendRun(mark, style)
		case *ast.RawHTML:
			var html strings.Builder
			for i := 0; i < node.Segments.Len(); i++ {
				segment := node.Segments.At(i)
				html.Write(segment.Value(s.source))
			}
			s.add("markdown.raw_html_preserved_as_text", udf.DiagnosticVisualApproximation, "body", "Raw HTML is retained as text and is not interpreted")
			appendRun(html.String(), style)
		default:
			if child.HasChildren() {
				result = append(result, s.inlines(child, style)...)
			}
		}
	}
	return result
}

func (s *importState) add(code string, class udf.DiagnosticClass, location, message string) {
	if len(s.doc.Diagnostics) >= maxImportDiagnostics {
		if len(s.doc.Diagnostics) > 0 && s.doc.Diagnostics[len(s.doc.Diagnostics)-1].Code == "diagnostics.truncated" {
			return
		}
		s.doc.Diagnostics[len(s.doc.Diagnostics)-1] = udf.Diagnostic{
			Code: "diagnostics.truncated", Severity: udf.SeverityWarning,
			Class: udf.DiagnosticNormalization, Location: "document",
			Message: "Additional import diagnostics were omitted by the limit",
		}
		return
	}
	s.doc.Diagnostics = append(s.doc.Diagnostics, udf.Diagnostic{
		Code: code, Severity: udf.SeverityWarning, Class: class,
		Location: location, Message: message,
	})
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
