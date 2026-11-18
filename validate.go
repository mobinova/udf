package udf

import (
	"fmt"
	"math"
)

func finiteNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func Validate(doc *Document) error {
	if doc == nil {
		return fmt.Errorf("nil document: %w", ErrInvalidModel)
	}
	if !finiteNumber(doc.Page.Width) || !finiteNumber(doc.Page.Height) || doc.Page.Width < 0 || doc.Page.Height < 0 {
		return fmt.Errorf("negative page dimensions: %w", ErrInvalidModel)
	}
	resources := make(map[string]bool, len(doc.Resources))
	for _, r := range doc.Resources {
		if r.ID == "" || resources[r.ID] || len(r.Data) == 0 {
			return fmt.Errorf("invalid resource: %w", ErrInvalidModel)
		}
		resources[r.ID] = true
	}
	tableStack := make(map[*Table]bool)
	var check func(Block) error
	check = func(block Block) error {
		payloads := 0
		if block.Paragraph != nil {
			payloads++
		}
		if block.Table != nil {
			payloads++
		}
		if block.PageBreak != nil {
			payloads++
		}
		if block.Unknown != nil {
			payloads++
		}
		if block.Kind == BlockPageBreak && block.PageBreak != nil && payloads == 1 {
			return nil
		}
		if payloads != 1 {
			return fmt.Errorf("block payload mismatch: %w", ErrInvalidModel)
		}
		switch block.Kind {
		case BlockParagraph:
			if block.Paragraph == nil {
				return fmt.Errorf("paragraph payload mismatch: %w", ErrInvalidModel)
			}
			if err := validateParagraph(block.Paragraph); err != nil {
				return err
			}
			for _, in := range block.Paragraph.Inlines {
				if err := validateInline(in, resources); err != nil {
					return err
				}
			}
		case BlockTable:
			if block.Table == nil {
				return fmt.Errorf("table payload mismatch: %w", ErrInvalidModel)
			}
			if tableStack[block.Table] {
				return fmt.Errorf("cyclic nested table: %w", ErrInvalidModel)
			}
			tableStack[block.Table] = true
			if err := validateTable(block.Table, check); err != nil {
				delete(tableStack, block.Table)
				return err
			}
			delete(tableStack, block.Table)
		case BlockUnknown:
			if block.Unknown == nil {
				return fmt.Errorf("unknown payload mismatch: %w", ErrInvalidModel)
			}
		default:
			return fmt.Errorf("unknown block kind: %w", ErrInvalidModel)
		}
		return nil
	}
	for _, block := range doc.Body {
		if err := check(block); err != nil {
			return err
		}
	}
	for _, h := range doc.Headers {
		for _, block := range h.Blocks {
			if err := check(block); err != nil {
				return err
			}
		}
	}
	for _, f := range doc.Footers {
		for _, block := range f.Blocks {
			if err := check(block); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateParagraph(p *Paragraph) error {
	props := p.Properties
	for _, value := range []float64{props.LeftIndent, props.RightIndent, props.FirstLineIndent, props.Hanging, props.SpaceAbove, props.SpaceBelow, props.LineSpacing} {
		if !finiteNumber(value) {
			return fmt.Errorf("invalid paragraph measurement: %w", ErrInvalidModel)
		}
	}
	for _, tab := range props.Tabs {
		if !finiteNumber(tab.Position) || tab.Position < 0 {
			return fmt.Errorf("invalid tab stop: %w", ErrInvalidModel)
		}
	}
	return nil
}

func validateInline(in Inline, resources map[string]bool) error {
	payloads := 0
	if in.Run != nil {
		payloads++
	}
	if in.Tab != nil {
		payloads++
	}
	if in.Break != nil {
		payloads++
	}
	if in.Field != nil {
		payloads++
	}
	if in.Image != nil {
		payloads++
	}
	if in.Unknown != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("inline payload mismatch: %w", ErrInvalidModel)
	}
	switch in.Kind {
	case InlineRun:
		if in.Run == nil {
			return ErrInvalidModel
		}
		if !finiteNumber(in.Run.Style.FontSize) || in.Run.Style.FontSize < 0 {
			return fmt.Errorf("invalid run font size: %w", ErrInvalidModel)
		}
	case InlineTab:
		if in.Tab == nil {
			return ErrInvalidModel
		}
	case InlineBreak:
		if in.Break == nil {
			return ErrInvalidModel
		}
	case InlineField:
		if in.Field == nil {
			return ErrInvalidModel
		}
		if !finiteNumber(in.Field.Style.FontSize) || in.Field.Style.FontSize < 0 {
			return fmt.Errorf("invalid field font size: %w", ErrInvalidModel)
		}
	case InlineImage:
		if in.Image == nil {
			return ErrInvalidModel
		}
		if !finiteNumber(in.Image.Width) || !finiteNumber(in.Image.Height) || in.Image.Width < 0 || in.Image.Height < 0 {
			return fmt.Errorf("invalid image dimensions: %w", ErrInvalidModel)
		}
		if in.Image.ResourceID != "" && !resources[in.Image.ResourceID] {
			return fmt.Errorf("unknown resource reference: %w", ErrInvalidModel)
		}
	case InlineUnknown:
		if in.Unknown == nil {
			return ErrInvalidModel
		}
	default:
		return fmt.Errorf("unknown inline kind: %w", ErrInvalidModel)
	}
	return nil
}

func validateTable(t *Table, check func(Block) error) error {
	if t.ColumnCount < 0 || t.ColumnCount > 10000 {
		return ErrInvalidModel
	}
	for _, width := range t.ColumnWidths {
		if !finiteNumber(width.Value) || width.Value < 0 {
			return fmt.Errorf("invalid column width: %w", ErrInvalidModel)
		}
		if width.Unit != UnitPoint && width.Unit != UnitPercent && width.Unit != UnitAuto {
			return fmt.Errorf("unknown column width unit: %w", ErrInvalidModel)
		}
	}
	occupied := map[[2]int]bool{}
	for ri, row := range t.Rows {
		if !finiteNumber(row.Height) || row.Height < 0 {
			return fmt.Errorf("invalid row height: %w", ErrInvalidModel)
		}
		for _, cell := range row.Cells {
			if cell.GridRow != ri || cell.GridCol < 0 || cell.RowSpan < 1 || cell.ColSpan < 1 {
				return fmt.Errorf("invalid table cell placement: %w", ErrInvalidModel)
			}
			if cell.GridRow+cell.RowSpan > len(t.Rows) {
				return fmt.Errorf("row span exceeds table: %w", ErrInvalidModel)
			}
			if cell.GridCol+cell.ColSpan > t.ColumnCount {
				return fmt.Errorf("column span exceeds table: %w", ErrInvalidModel)
			}
			if !finiteNumber(cell.Border.Width) || cell.Border.Width < 0 {
				return fmt.Errorf("invalid cell border width: %w", ErrInvalidModel)
			}
			for r := cell.GridRow; r < cell.GridRow+cell.RowSpan; r++ {
				for c := cell.GridCol; c < cell.GridCol+cell.ColSpan; c++ {
					key := [2]int{r, c}
					if occupied[key] {
						return fmt.Errorf("overlapping table cells: %w", ErrInvalidModel)
					}
					occupied[key] = true
				}
			}
			for _, block := range cell.Blocks {
				if err := check(block); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
