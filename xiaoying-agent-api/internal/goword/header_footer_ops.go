package goword

import (
	"fmt"
	"strings"
)

func applyUpdateHeaderFooter(document *Document, operation Operation) error {
	if document == nil {
		return fmt.Errorf("document is nil")
	}
	if operation.HeaderFooter == nil {
		return fmt.Errorf("header_footer payload is required")
	}
	part, err := buildHeaderFooterPartFromInput(*operation.HeaderFooter)
	if err != nil {
		return err
	}
	for index := range document.Meta.HeaderFooters {
		existing := &document.Meta.HeaderFooters[index]
		if normalizeHeaderFooterKind(existing.Kind) == part.Kind && normalizeHeaderFooterVariant(existing.Variant) == part.Variant {
			if strings.TrimSpace(existing.RelationshipID) != "" {
				part.RelationshipID = existing.RelationshipID
			}
			if strings.TrimSpace(existing.Target) != "" {
				part.Target = existing.Target
			}
			document.Meta.HeaderFooters[index] = part
			return nil
		}
	}
	document.Meta.HeaderFooters = append(document.Meta.HeaderFooters, part)
	return nil
}

func buildHeaderFooterPartFromInput(input HeaderFooterInput) (HeaderFooterPart, error) {
	kind := normalizeHeaderFooterKind(input.Kind)
	if kind == "" {
		return HeaderFooterPart{}, fmt.Errorf("header_footer kind must be header or footer")
	}
	part := HeaderFooterPart{
		Kind:           kind,
		Variant:        normalizeHeaderFooterVariant(input.Variant),
		RelationshipID: headerFooterRelationshipID(kind, input.Variant),
		Target:         headerFooterTargetPath(kind, input.Variant),
		Blocks:         buildBlocksFromInputs(input.Blocks),
	}
	if len(part.Blocks) == 0 {
		paragraph := buildParagraphFromInput(ParagraphInput{})
		part.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
	}
	part.rebuildParagraphs()
	return part, nil
}

func buildBlocksFromInputs(inputs []BlockInput) []Block {
	if len(inputs) == 0 {
		return nil
	}
	blocks := make([]Block, 0, len(inputs))
	for _, input := range inputs {
		switch input.Type {
		case BlockTypeTable:
			if input.Table == nil {
				continue
			}
			table := buildTableFromInput(*input.Table)
			blocks = append(blocks, Block{Type: BlockTypeTable, Table: &table})
		default:
			paragraphInput := ParagraphInput{}
			if input.Paragraph != nil {
				paragraphInput = *input.Paragraph
			}
			paragraph := buildParagraphFromInput(paragraphInput)
			blocks = append(blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
		}
	}
	return blocks
}

func normalizeHeaderFooterKind(kind string) string {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	if normalized != "header" && normalized != "footer" {
		return ""
	}
	return normalized
}

func normalizeHeaderFooterVariant(variant string) string {
	normalized := strings.ToLower(strings.TrimSpace(variant))
	switch normalized {
	case "", "default":
		return "default"
	case "first", "even":
		return normalized
	default:
		return "default"
	}
}
