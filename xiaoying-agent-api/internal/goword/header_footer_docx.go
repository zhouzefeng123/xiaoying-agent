package goword

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
)

type xmlHeaderFooterContainer struct {
	Elements []xmlBodyElement `xml:",any"`
}

type xmlSectionReferenceContainer struct {
	HeaderRefs []xmlSectionHeaderFooterRef `xml:"headerReference"`
	FooterRefs []xmlSectionHeaderFooterRef `xml:"footerReference"`
}

type xmlSectionHeaderFooterRef struct {
	Type string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main type,attr"`
	ID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
}

type headerFooterReference struct {
	Kind           string
	Variant        string
	RelationshipID string
}

func parseHeaderFooterParts(files []*zip.File, sectionXML string, relationshipsXML, contentTypesXML []byte, theme *DocumentTheme) ([]HeaderFooterPart, error) {
	references, err := parseSectionHeaderFooterReferences(sectionXML)
	if err != nil {
		return nil, err
	}
	if len(references) == 0 {
		return nil, nil
	}
	relationships, err := parseRelationshipLookup(relationshipsXML)
	if err != nil {
		return nil, err
	}
	parts := make([]HeaderFooterPart, 0, len(references))
	for _, reference := range references {
		relationship, ok := relationships[reference.RelationshipID]
		if !ok {
			continue
		}
		target := normalizeRelationshipTarget(relationship.Target)
		if target == "" {
			continue
		}
		content, err := readOptionalZipEntry(files, "word/"+target)
		if err != nil {
			return nil, err
		}
		if len(content) == 0 {
			continue
		}
		partRelationshipsXML, err := readOptionalZipEntry(files, headerFooterRelationshipsPath(target))
		if err != nil {
			return nil, err
		}
		imageRelationships, err := parseDocumentRelationshipIndex(partRelationshipsXML, contentTypesXML)
		if err != nil {
			return nil, err
		}
		blocks, err := parseHeaderFooterBlocks(content, imageRelationships, theme)
		if err != nil {
			return nil, err
		}
		part := HeaderFooterPart{
			Kind:           reference.Kind,
			Variant:        reference.Variant,
			RelationshipID: reference.RelationshipID,
			Target:         target,
			Blocks:         blocks,
		}
		part.rebuildParagraphs()
		parts = append(parts, part)
	}
	return parts, nil
}

func parseSectionHeaderFooterReferences(sectionXML string) ([]headerFooterReference, error) {
	raw := strings.TrimSpace(sectionXML)
	if raw == "" {
		return nil, nil
	}
	var container xmlSectionReferenceContainer
	if err := xml.Unmarshal([]byte(wrapWordMLElement("sectPr", raw)), &container); err != nil {
		return nil, fmt.Errorf("parse header/footer references: %w", err)
	}
	references := make([]headerFooterReference, 0, len(container.HeaderRefs)+len(container.FooterRefs))
	for _, item := range container.HeaderRefs {
		references = append(references, headerFooterReference{
			Kind:           "header",
			Variant:        normalizeHeaderFooterVariant(item.Type),
			RelationshipID: strings.TrimSpace(item.ID),
		})
	}
	for _, item := range container.FooterRefs {
		references = append(references, headerFooterReference{
			Kind:           "footer",
			Variant:        normalizeHeaderFooterVariant(item.Type),
			RelationshipID: strings.TrimSpace(item.ID),
		})
	}
	return references, nil
}

func parseRelationshipLookup(relationshipsXML []byte) (map[string]xmlRelationship, error) {
	lookup := map[string]xmlRelationship{}
	if len(relationshipsXML) == 0 {
		return lookup, nil
	}
	var relationships xmlRelationships
	if err := xml.Unmarshal(relationshipsXML, &relationships); err != nil {
		return nil, fmt.Errorf("parse relationship lookup: %w", err)
	}
	for _, relationship := range relationships.Relationships {
		lookup[strings.TrimSpace(relationship.ID)] = relationship
	}
	return lookup, nil
}

func parseHeaderFooterBlocks(content []byte, relationships documentRelationshipIndex, theme *DocumentTheme) ([]Block, error) {
	var parsed xmlHeaderFooterContainer
	if err := xml.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("parse header/footer xml: %w", err)
	}
	blocks, _, err := parseBlockElements(parsed.Elements, theme, relationships)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func prepareHeaderFooterPartsForSave(parts []HeaderFooterPart) []HeaderFooterPart {
	if len(parts) == 0 {
		return nil
	}
	prepared := make([]HeaderFooterPart, 0, len(parts))
	for _, part := range parts {
		kind := normalizeHeaderFooterKind(part.Kind)
		if kind == "" {
			continue
		}
		variant := normalizeHeaderFooterVariant(part.Variant)
		cloned := part.clone()
		cloned.Kind = kind
		cloned.Variant = variant
		if strings.TrimSpace(cloned.RelationshipID) == "" {
			cloned.RelationshipID = headerFooterRelationshipID(kind, variant)
		}
		if strings.TrimSpace(cloned.Target) == "" {
			cloned.Target = headerFooterTargetPath(kind, variant)
		}
		if len(cloned.Blocks) == 0 {
			paragraph := buildParagraphFromInput(ParagraphInput{})
			cloned.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
		}
		cloned.rebuildParagraphs()
		prepared = append(prepared, cloned)
	}
	return prepared
}

func ensureSectionHeaderFooterReferences(sectionXML string, parts []HeaderFooterPart) (string, error) {
	raw := strings.TrimSpace(sectionXML)
	if raw == "" {
		raw = defaultSectionXML()
	}
	if len(parts) == 0 {
		return raw, nil
	}
	wrapper := `<w:sectPr xmlns:w="` + wordMLNamespace + `" xmlns:r="` + relationshipNamespace + `">` + raw + `</w:sectPr>`
	var container xmlSectionContainer
	if err := xml.Unmarshal([]byte(wrapper), &container); err != nil {
		return "", fmt.Errorf("parse section xml for header/footer refs: %w", err)
	}
	filtered := make([]xmlSectionElement, 0, len(container.Elements))
	for _, element := range container.Elements {
		if element.XMLName.Local == "headerReference" || element.XMLName.Local == "footerReference" {
			continue
		}
		filtered = append(filtered, element)
	}
	refs := make([]xmlSectionElement, 0, len(parts))
	for _, part := range parts {
		localName := "headerReference"
		if part.Kind == "footer" {
			localName = "footerReference"
		}
		refs = append(refs, xmlSectionElement{
			XMLName: xml.Name{Space: wordMLNamespace, Local: localName},
			Attrs: []xml.Attr{
				{Name: xml.Name{Space: wordMLNamespace, Local: "type"}, Value: normalizeHeaderFooterVariant(part.Variant)},
				{Name: xml.Name{Space: relationshipNamespace, Local: "id"}, Value: strings.TrimSpace(part.RelationshipID)},
			},
		})
	}
	container.Elements = append(refs, filtered...)
	return renderSectionElements(container.Elements), nil
}

func buildHeaderFooterXML(part HeaderFooterPart) ([]byte, error) {
	localName := "hdr"
	if part.Kind == "footer" {
		localName = "ftr"
	}
	var builder strings.Builder
	builder.Grow(1024)
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<w:`)
	builder.WriteString(localName)
	builder.WriteString(` xmlns:w="`)
	builder.WriteString(wordMLNamespace)
	builder.WriteString(`" xmlns:r="`)
	builder.WriteString(relationshipNamespace)
	builder.WriteString(`" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">`)
	blocks := part.Blocks
	if len(blocks) == 0 {
		paragraph := buildParagraphFromInput(ParagraphInput{})
		blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
	}
	for _, block := range blocks {
		writeBlockXML(&builder, block)
	}
	builder.WriteString(`</w:`)
	builder.WriteString(localName)
	builder.WriteString(`>`)
	return []byte(builder.String()), nil
}

func writeHeaderFooterEntries(writer *zip.Writer, parts []HeaderFooterPart) error {
	for _, part := range parts {
		content, err := buildHeaderFooterXML(part)
		if err != nil {
			return err
		}
		if err := writeZipEntry(writer, "word/"+strings.TrimPrefix(part.Target, "/"), content); err != nil {
			return err
		}
	}
	return nil
}

func headerFooterEntryPaths(parts []HeaderFooterPart) map[string]struct{} {
	paths := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		paths["word/"+strings.TrimPrefix(part.Target, "/")] = struct{}{}
	}
	return paths
}

func headerFooterRelationshipID(kind, variant string) string {
	return "rId" + strings.Title(kind) + strings.Title(normalizeHeaderFooterVariant(variant))
}

func headerFooterTargetPath(kind, variant string) string {
	return fmt.Sprintf("%s-%s.xml", kind, normalizeHeaderFooterVariant(variant))
}

func headerFooterRelationshipsPath(target string) string {
	normalized := normalizeRelationshipTarget(target)
	directory := filepath.Dir(normalized)
	filename := filepath.Base(normalized)
	if directory == "." {
		return filepath.ToSlash(filepath.Join("word", "_rels", filename+".rels"))
	}
	return filepath.ToSlash(filepath.Join("word", directory, "_rels", filename+".rels"))
}
