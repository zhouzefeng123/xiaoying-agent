package goword

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type xmlSectionContainer struct {
	XMLName  xml.Name            `xml:"sectPr"`
	Elements []xmlSectionElement `xml:",any"`
}

type xmlSectionElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	InnerXML string     `xml:",innerxml"`
}

func applyUpdateSectionLayout(document *Document, operation Operation) error {
	if !hasSectionLayoutPatch(operation.SectionLayoutPatch) {
		return fmt.Errorf("section_layout_patch is required for update_section_layout")
	}
	sectionXML, err := mergeSectionLayout(document.Meta.SectionXML, operation.SectionLayoutPatch)
	if err != nil {
		return err
	}
	document.Meta.SectionXML = sectionXML
	document.Meta.SectionLayout = parseSectionLayout(sectionXML)
	return nil
}

func parseSectionLayout(sectionXML string) SectionLayout {
	raw := strings.TrimSpace(sectionXML)
	if raw == "" {
		raw = defaultSectionXML()
	}
	wrapper := `<w:sectPr xmlns:w="` + wordMLNamespace + `">` + raw + `</w:sectPr>`
	var container xmlSectionContainer
	if err := xml.Unmarshal([]byte(wrapper), &container); err != nil {
		return SectionLayout{
			PageWidth:    11906,
			PageHeight:   16838,
			Orientation:  "portrait",
			MarginTop:    1440,
			MarginRight:  1800,
			MarginBottom: 1440,
			MarginLeft:   1800,
			MarginHeader: 720,
			MarginFooter: 720,
			MarginGutter: 0,
		}
	}
	pgSz := findOrAppendSectionElement(&container.Elements, "pgSz")
	pgMar := findOrAppendSectionElement(&container.Elements, "pgMar")
	typeElement := findSectionElement(container.Elements, "type")
	titlePageElement := findSectionElement(container.Elements, "titlePg")
	pageNumberElement := findSectionElement(container.Elements, "pgNumType")
	pageWidth := sectionAttrInt(pgSz, "w", 11906)
	pageHeight := sectionAttrInt(pgSz, "h", 16838)
	orientation := strings.ToLower(strings.TrimSpace(sectionAttrString(pgSz, "orient", "")))
	if orientation == "" {
		if pageWidth > pageHeight {
			orientation = "landscape"
		} else {
			orientation = "portrait"
		}
	}
	return SectionLayout{
		BreakType:    strings.ToLower(strings.TrimSpace(sectionAttrString(typeElement, "val", ""))),
		PageWidth:    pageWidth,
		PageHeight:   pageHeight,
		Orientation:  orientation,
		TitlePageSet: titlePageElement != nil,
		TitlePage:    titlePageElement != nil,
		PageNumStart: sectionAttrInt(pageNumberElement, "start", 0),
		PageNumFmt:   strings.TrimSpace(sectionAttrString(pageNumberElement, "fmt", "")),
		MarginTop:    sectionAttrInt(pgMar, "top", 1440),
		MarginRight:  sectionAttrInt(pgMar, "right", 1800),
		MarginBottom: sectionAttrInt(pgMar, "bottom", 1440),
		MarginLeft:   sectionAttrInt(pgMar, "left", 1800),
		MarginHeader: sectionAttrInt(pgMar, "header", 720),
		MarginFooter: sectionAttrInt(pgMar, "footer", 720),
		MarginGutter: sectionAttrInt(pgMar, "gutter", 0),
	}
}

func hasSectionLayoutPatch(layout SectionLayout) bool {
	return strings.TrimSpace(layout.BreakType) != "" ||
		layout.PageWidth > 0 ||
		layout.PageHeight > 0 ||
		strings.TrimSpace(layout.Orientation) != "" ||
		layout.TitlePageSet ||
		layout.TitlePage ||
		layout.PageNumStart > 0 ||
		strings.TrimSpace(layout.PageNumFmt) != "" ||
		layout.MarginTop > 0 ||
		layout.MarginRight > 0 ||
		layout.MarginBottom > 0 ||
		layout.MarginLeft > 0 ||
		layout.MarginHeader > 0 ||
		layout.MarginFooter > 0 ||
		layout.MarginGutter > 0
}

func findSectionElement(elements []xmlSectionElement, localName string) *xmlSectionElement {
	for index := range elements {
		if elements[index].XMLName.Local == localName {
			return &elements[index]
		}
	}
	return nil
}

func mergeSectionLayout(sectionXML string, patch SectionLayout) (string, error) {
	raw := strings.TrimSpace(sectionXML)
	if raw == "" {
		raw = defaultSectionXML()
	}
	wrapper := `<w:sectPr xmlns:w="` + wordMLNamespace + `">` + raw + `</w:sectPr>`
	var container xmlSectionContainer
	if err := xml.Unmarshal([]byte(wrapper), &container); err != nil {
		return "", fmt.Errorf("parse section xml: %w", err)
	}
	pgSz := findOrAppendSectionElement(&container.Elements, "pgSz")
	pgMar := findOrAppendSectionElement(&container.Elements, "pgMar")
	pgNumType := findSectionElement(container.Elements, "pgNumType")

	pageWidth := sectionAttrInt(pgSz, "w", 11906)
	pageHeight := sectionAttrInt(pgSz, "h", 16838)
	if patch.PageWidth > 0 {
		pageWidth = patch.PageWidth
	}
	if patch.PageHeight > 0 {
		pageHeight = patch.PageHeight
	}
	orientation := strings.ToLower(strings.TrimSpace(patch.Orientation))
	breakType := strings.ToLower(strings.TrimSpace(patch.BreakType))
	if orientation == "landscape" {
		if pageWidth < pageHeight {
			pageWidth, pageHeight = pageHeight, pageWidth
		}
		setSectionAttr(pgSz, "orient", "landscape")
	} else if orientation == "portrait" {
		if pageWidth > pageHeight {
			pageWidth, pageHeight = pageHeight, pageWidth
		}
		removeSectionAttr(pgSz, "orient")
	} else if pageWidth > pageHeight {
		setSectionAttr(pgSz, "orient", "landscape")
	} else {
		removeSectionAttr(pgSz, "orient")
	}
	setSectionAttr(pgSz, "w", strconv.Itoa(pageWidth))
	setSectionAttr(pgSz, "h", strconv.Itoa(pageHeight))

	mergePositiveSectionAttr(pgMar, "top", patch.MarginTop)
	mergePositiveSectionAttr(pgMar, "right", patch.MarginRight)
	mergePositiveSectionAttr(pgMar, "bottom", patch.MarginBottom)
	mergePositiveSectionAttr(pgMar, "left", patch.MarginLeft)
	mergePositiveSectionAttr(pgMar, "header", patch.MarginHeader)
	mergePositiveSectionAttr(pgMar, "footer", patch.MarginFooter)
	mergePositiveSectionAttr(pgMar, "gutter", patch.MarginGutter)

	if patch.TitlePageSet || patch.TitlePage {
		if patch.TitlePage {
			findOrAppendSectionElement(&container.Elements, "titlePg")
		} else {
			removeSectionElement(&container.Elements, "titlePg")
		}
	}

	if patch.PageNumStart > 0 || strings.TrimSpace(patch.PageNumFmt) != "" {
		pgNumType = findOrAppendSectionElement(&container.Elements, "pgNumType")
		if patch.PageNumStart > 0 {
			setSectionAttr(pgNumType, "start", strconv.Itoa(patch.PageNumStart))
		}
		if strings.TrimSpace(patch.PageNumFmt) != "" {
			setSectionAttr(pgNumType, "fmt", strings.TrimSpace(patch.PageNumFmt))
		}
	}

	if breakType != "" {
		typeElement := findOrAppendSectionElement(&container.Elements, "type")
		setSectionAttr(typeElement, "val", breakType)
	} else {
		removeSectionElement(&container.Elements, "type")
	}

	return renderSectionElements(container.Elements), nil
}

func findOrAppendSectionElement(elements *[]xmlSectionElement, localName string) *xmlSectionElement {
	for index := range *elements {
		if (*elements)[index].XMLName.Local == localName {
			return &(*elements)[index]
		}
	}
	*elements = append(*elements, xmlSectionElement{XMLName: xml.Name{Space: wordMLNamespace, Local: localName}})
	return &(*elements)[len(*elements)-1]
}

func sectionAttrInt(element *xmlSectionElement, localName string, fallback int) int {
	if element == nil {
		return fallback
	}
	for _, attr := range element.Attrs {
		if attr.Name.Local != localName {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(attr.Value))
		if err == nil {
			return value
		}
	}
	return fallback
}

func sectionAttrString(element *xmlSectionElement, localName, fallback string) string {
	if element == nil {
		return fallback
	}
	for _, attr := range element.Attrs {
		if attr.Name.Local == localName {
			value := strings.TrimSpace(attr.Value)
			if value != "" {
				return value
			}
		}
	}
	return fallback
}

func removeSectionElement(elements *[]xmlSectionElement, localName string) {
	filtered := (*elements)[:0]
	for _, element := range *elements {
		if element.XMLName.Local == localName {
			continue
		}
		filtered = append(filtered, element)
	}
	*elements = filtered
}

func mergePositiveSectionAttr(element *xmlSectionElement, localName string, value int) {
	if value > 0 {
		setSectionAttr(element, localName, strconv.Itoa(value))
	}
}

func setSectionAttr(element *xmlSectionElement, localName, value string) {
	for index := range element.Attrs {
		if element.Attrs[index].Name.Local == localName {
			element.Attrs[index].Value = value
			return
		}
	}
	element.Attrs = append(element.Attrs, xml.Attr{Name: xml.Name{Space: wordMLNamespace, Local: localName}, Value: value})
}

func removeSectionAttr(element *xmlSectionElement, localName string) {
	filtered := element.Attrs[:0]
	for _, attr := range element.Attrs {
		if attr.Name.Local == localName {
			continue
		}
		filtered = append(filtered, attr)
	}
	element.Attrs = filtered
}

func renderSectionElements(elements []xmlSectionElement) string {
	var builder strings.Builder
	for _, element := range elements {
		builder.WriteString(`<w:`)
		builder.WriteString(element.XMLName.Local)
		for _, attr := range element.Attrs {
			builder.WriteString(` `)
			if attr.Name.Space == wordMLNamespace || attr.Name.Space == "" {
				builder.WriteString(`w:`)
				builder.WriteString(attr.Name.Local)
			} else if attr.Name.Space == relationshipNamespace {
				builder.WriteString(`r:`)
				builder.WriteString(attr.Name.Local)
			} else {
				builder.WriteString(attr.Name.Local)
			}
			builder.WriteString(`="`)
			builder.WriteString(escapeXML(attr.Value))
			builder.WriteString(`"`)
		}
		if strings.TrimSpace(element.InnerXML) == "" {
			builder.WriteString(`/>)`)
			continue
		}
		builder.WriteString(`>`)
		builder.WriteString(element.InnerXML)
		builder.WriteString(`</w:`)
		builder.WriteString(element.XMLName.Local)
		builder.WriteString(`>`)
	}
	return strings.ReplaceAll(builder.String(), "/>)", "/>")
}
