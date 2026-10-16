package goword

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	wordMLNamespace       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	relationshipNamespace = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

type xmlDocument struct {
	Body xmlBody `xml:"body"`
}

type xmlBody struct {
	Elements []xmlBodyElement `xml:",any"`
}

type xmlBodyElement struct {
	XMLName  xml.Name
	InnerXML string `xml:",innerxml"`
}

type xmlSection struct {
	InnerXML string `xml:",innerxml"`
}

type xmlParagraph struct {
	Properties *xmlParagraphProperties `xml:"pPr"`
	Runs       []xmlRun                `xml:"r"`
}

type xmlTable struct {
	Properties *xmlRawInner  `xml:"tblPr"`
	Grid       *xmlRawInner  `xml:"tblGrid"`
	Rows       []xmlTableRow `xml:"tr"`
}

type xmlTableRow struct {
	Properties *xmlRawInner   `xml:"trPr"`
	Cells      []xmlTableCell `xml:"tc"`
}

type xmlTableRowProperties struct {
	RepeatHeader *xmlOnOff `xml:"tblHeader"`
	CantSplit    *xmlOnOff `xml:"cantSplit"`
}

type xmlTableCell struct {
	Elements []xmlBodyElement `xml:",any"`
}

type xmlRawInner struct {
	InnerXML string `xml:",innerxml"`
}

type xmlParagraphProperties struct {
	Style           *xmlStyleRef            `xml:"pStyle"`
	Alignment       *xmlAlignment           `xml:"jc"`
	Spacing         *xmlSpacing             `xml:"spacing"`
	Indent          *xmlIndent              `xml:"ind"`
	KeepNext        *xmlOnOff               `xml:"keepNext"`
	KeepLines       *xmlOnOff               `xml:"keepLines"`
	PageBreakBefore *xmlOnOff               `xml:"pageBreakBefore"`
	WidowControl    *xmlOnOff               `xml:"widowControl"`
	Numbering       *xmlNumberingProperties `xml:"numPr"`
	Section         *xmlRawInner            `xml:"sectPr"`
}

type xmlStyleRef struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlNumberingProperties struct {
	Level *xmlLevelValue `xml:"ilvl"`
	NumID *xmlLevelValue `xml:"numId"`
}

type xmlLevelValue struct {
	Value int `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlAlignment struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlSpacing struct {
	Before   string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main before,attr"`
	After    string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main after,attr"`
	Line     string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main line,attr"`
	LineRule string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main lineRule,attr"`
}

type xmlIndent struct {
	FirstLine string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main firstLine,attr"`
	Left      string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main left,attr"`
}

type xmlRun struct {
	Properties *xmlRunProperties `xml:"rPr"`
	Elements   []xmlRunElement   `xml:",any"`
}

type xmlRunElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	InnerXML string     `xml:",innerxml"`
}

type xmlBreak struct {
	Type string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main type,attr"`
}

type xmlDrawing struct {
	InnerXML string               `xml:",innerxml"`
	Inline   *xmlDrawingContainer `xml:"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing inline"`
	Anchor   *xmlDrawingContainer `xml:"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing anchor"`
}

type xmlDrawingContainer struct {
	Extent  *xmlDrawingExtent `xml:"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing extent"`
	DocPr   *xmlDrawingDocPr  `xml:"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing docPr"`
	Graphic *xmlGraphic       `xml:"http://schemas.openxmlformats.org/drawingml/2006/main graphic"`
}

type xmlDrawingExtent struct {
	CX int64 `xml:"cx,attr"`
	CY int64 `xml:"cy,attr"`
}

type xmlDrawingDocPr struct {
	Name string `xml:"name,attr"`
}

type xmlGraphic struct {
	GraphicData *xmlGraphicData `xml:"http://schemas.openxmlformats.org/drawingml/2006/main graphicData"`
}

type xmlGraphicData struct {
	Picture *xmlPicture `xml:"http://schemas.openxmlformats.org/drawingml/2006/picture pic"`
}

type xmlPicture struct {
	BlipFill *xmlBlipFill `xml:"http://schemas.openxmlformats.org/drawingml/2006/picture blipFill"`
}

type xmlBlipFill struct {
	Blip *xmlBlip `xml:"http://schemas.openxmlformats.org/drawingml/2006/main blip"`
}

type xmlBlip struct {
	Embed string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships embed,attr"`
	Link  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships link,attr"`
}

type xmlRunProperties struct {
	Style     *xmlStyleRef  `xml:"rStyle"`
	Bold      *xmlOnOff     `xml:"b"`
	Italic    *xmlOnOff     `xml:"i"`
	Underline *xmlUnderline `xml:"u"`
	Color     *xmlColor     `xml:"color"`
	Size      *xmlSize      `xml:"sz"`
	Fonts     *xmlFonts     `xml:"rFonts"`
}

type xmlOnOff struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlUnderline struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlColor struct {
	Value      string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
	ThemeColor string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main themeColor,attr"`
}

type xmlSize struct {
	Value int `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlFonts struct {
	ASCII         string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main ascii,attr"`
	EastAsia      string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main eastAsia,attr"`
	ASCIITheme    string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main asciiTheme,attr"`
	EastAsiaTheme string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main eastAsiaTheme,attr"`
}

type xmlText struct {
	Value string `xml:",chardata"`
}

type xmlStyles struct {
	DocDefaults *xmlDocDefaults `xml:"docDefaults"`
	Styles      []xmlStyle      `xml:"style"`
}

type xmlDocDefaults struct {
	ParagraphDefaults *xmlParagraphDefaults `xml:"pPrDefault"`
	RunDefaults       *xmlRunDefaults       `xml:"rPrDefault"`
}

type xmlParagraphDefaults struct {
	Properties *xmlParagraphProperties `xml:"pPr"`
}

type xmlRunDefaults struct {
	Properties *xmlRunProperties `xml:"rPr"`
}

type xmlStyle struct {
	ID                  string                  `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main styleId,attr"`
	Type                string                  `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main type,attr"`
	Default             string                  `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main default,attr"`
	Name                *xmlStyleName           `xml:"name"`
	BasedOn             *xmlStyleRef            `xml:"basedOn"`
	ParagraphProperties *xmlParagraphProperties `xml:"pPr"`
	RunProperties       *xmlRunProperties       `xml:"rPr"`
}

type xmlStyleName struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlNumbering struct {
	Abstracts []xmlAbstractNum `xml:"abstractNum"`
	Nums      []xmlNum         `xml:"num"`
}

type xmlAbstractNum struct {
	ID     int      `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main abstractNumId,attr"`
	Levels []xmlLvl `xml:"lvl"`
}

type xmlLvl struct {
	Level               int                     `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main ilvl,attr"`
	Start               *xmlLevelValue          `xml:"start"`
	Format              *xmlStringValue         `xml:"numFmt"`
	Text                *xmlStringValue         `xml:"lvlText"`
	ParagraphProperties *xmlParagraphProperties `xml:"pPr"`
	RunProperties       *xmlRunProperties       `xml:"rPr"`
}

type xmlStringValue struct {
	Value string `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlNum struct {
	ID         int                `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main numId,attr"`
	AbstractID *xmlAbstractNumRef `xml:"abstractNumId"`
}

type xmlAbstractNumRef struct {
	Value int `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main val,attr"`
}

type xmlRelationships struct {
	XMLName       xml.Name          `xml:"Relationships"`
	Xmlns         string            `xml:"xmlns,attr,omitempty"`
	Relationships []xmlRelationship `xml:"Relationship"`
}

type xmlRelationship struct {
	ID     string `xml:"Id,attr"`
	Type   string `xml:"Type,attr"`
	Target string `xml:"Target,attr"`
}

type xmlContentTypes struct {
	XMLName   xml.Name             `xml:"Types"`
	Xmlns     string               `xml:"xmlns,attr,omitempty"`
	Defaults  []xmlContentDefault  `xml:"Default"`
	Overrides []xmlContentOverride `xml:"Override"`
}

type xmlContentDefault struct {
	Extension   string `xml:"Extension,attr"`
	ContentType string `xml:"ContentType,attr"`
}

type xmlContentOverride struct {
	PartName    string `xml:"PartName,attr"`
	ContentType string `xml:"ContentType,attr"`
}

type xmlTheme struct {
	ThemeElements *xmlThemeElements `xml:"http://schemas.openxmlformats.org/drawingml/2006/main themeElements"`
}

type xmlThemeElements struct {
	ColorScheme *xmlThemeColorScheme `xml:"http://schemas.openxmlformats.org/drawingml/2006/main clrScheme"`
	FontScheme  *xmlThemeFontScheme  `xml:"http://schemas.openxmlformats.org/drawingml/2006/main fontScheme"`
}

type xmlThemeColorScheme struct {
	Dark1     *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main dk1"`
	Light1    *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main lt1"`
	Dark2     *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main dk2"`
	Light2    *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main lt2"`
	Accent1   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent1"`
	Accent2   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent2"`
	Accent3   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent3"`
	Accent4   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent4"`
	Accent5   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent5"`
	Accent6   *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main accent6"`
	Hyperlink *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main hlink"`
	Followed  *xmlThemeColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main folHlink"`
}

type xmlThemeColor struct {
	SRGBClr *xmlThemeSRGBColor `xml:"http://schemas.openxmlformats.org/drawingml/2006/main srgbClr"`
	SysClr  *xmlThemeSysColor  `xml:"http://schemas.openxmlformats.org/drawingml/2006/main sysClr"`
}

type xmlThemeSRGBColor struct {
	Value string `xml:"val,attr"`
}

type xmlThemeSysColor struct {
	LastClr string `xml:"lastClr,attr"`
}

type xmlThemeFontScheme struct {
	Major *xmlThemeFontCollection `xml:"http://schemas.openxmlformats.org/drawingml/2006/main majorFont"`
	Minor *xmlThemeFontCollection `xml:"http://schemas.openxmlformats.org/drawingml/2006/main minorFont"`
}

type xmlThemeFontCollection struct {
	Latin    *xmlThemeTypeface `xml:"http://schemas.openxmlformats.org/drawingml/2006/main latin"`
	EastAsia *xmlThemeTypeface `xml:"http://schemas.openxmlformats.org/drawingml/2006/main ea"`
	Complex  *xmlThemeTypeface `xml:"http://schemas.openxmlformats.org/drawingml/2006/main cs"`
}

type xmlThemeTypeface struct {
	Typeface string `xml:"typeface,attr"`
}

type documentRelationshipIndex map[string]RunImage

type contentTypeLookup struct {
	defaults  map[string]string
	overrides map[string]string
}

func OpenDOCX(path string) (*Document, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	defer reader.Close()

	var documentFile *zip.File
	var stylesFile *zip.File
	var numberingFile *zip.File
	var themeFile *zip.File
	var relationshipsFile *zip.File
	var contentTypesFile *zip.File
	var relsContent []byte
	var contentTypesContent []byte
	for _, file := range reader.File {
		if file.Name == "[Content_Types].xml" {
			contentTypesFile = file
			continue
		}
		if file.Name == "word/document.xml" {
			documentFile = file
			continue
		}
		if file.Name == "word/_rels/document.xml.rels" {
			relationshipsFile = file
			continue
		}
		if file.Name == "word/styles.xml" {
			stylesFile = file
			continue
		}
		if file.Name == "word/numbering.xml" {
			numberingFile = file
			continue
		}
		if file.Name == "word/theme/theme1.xml" {
			themeFile = file
		}
	}
	if documentFile == nil {
		return nil, fmt.Errorf("word/document.xml not found")
	}

	content, err := readZipFile(documentFile)
	if err != nil {
		return nil, err
	}

	var parsed xmlDocument
	if err := xml.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("parse document.xml: %w", err)
	}

	relationshipIndex := documentRelationshipIndex{}
	if relationshipsFile != nil {
		relsContent, err = readZipFile(relationshipsFile)
		if err != nil {
			return nil, err
		}
		if contentTypesFile != nil {
			contentTypesContent, err = readZipFile(contentTypesFile)
			if err != nil {
				return nil, err
			}
		}
		relationshipIndex, err = parseDocumentRelationshipIndex(relsContent, contentTypesContent)
		if err != nil {
			return nil, err
		}
	}

	document := &Document{
		ID:         uuid.NewString(),
		Name:       filepath.Base(path),
		SourcePath: path,
		Revision:   1,
		Meta: DocumentMeta{
			Format:        "docx",
			ObjectSummary: summarizeOOXMLObjects(content),
		},
		Blocks: make([]Block, 0, len(parsed.Body.Elements)),
	}
	if themeFile != nil {
		themeContent, err := readZipFile(themeFile)
		if err != nil {
			return nil, err
		}
		document.Meta.Theme, err = parseThemeXML(themeContent)
		if err != nil {
			return nil, err
		}
	}
	if stylesFile != nil {
		stylesContent, err := readZipFile(stylesFile)
		if err != nil {
			return nil, err
		}
		styles, defaultParagraphStyle, defaultTextStyle, err := parseStylesXML(stylesContent, document.Meta.Theme)
		if err != nil {
			return nil, err
		}
		document.Meta.Styles = styles
		document.Meta.DefaultParagraphStyle = defaultParagraphStyle
		document.Meta.DefaultTextStyle = defaultTextStyle
	}
	if numberingFile != nil {
		numberingContent, err := readZipFile(numberingFile)
		if err != nil {
			return nil, err
		}
		document.Meta.Numbering, err = parseNumberingXML(numberingContent, document.Meta.Theme)
		if err != nil {
			return nil, err
		}
	}

	blocks, sectionXML, err := parseBlockElements(parsed.Body.Elements, document.Meta.Theme, relationshipIndex)
	if err != nil {
		return nil, err
	}
	document.Blocks = blocks
	document.Meta.SectionXML = sectionXML
	document.Meta.SectionLayout = parseSectionLayout(sectionXML)
	document.Meta.HeaderFooters, err = parseHeaderFooterParts(reader.File, sectionXML, relsContent, contentTypesContent, document.Meta.Theme)
	if err != nil {
		return nil, err
	}
	document.Meta.Sections, err = buildDocumentSections(reader.File, document.Blocks, sectionXML, relsContent, contentTypesContent, document.Meta.Theme)
	if err != nil {
		return nil, err
	}

	if len(document.Blocks) == 0 {
		document.Blocks = []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: nextID("para"), Runs: []Run{{ID: nextID("run"), Text: ""}}},
		}}
	}
	applyResolvedStyles(document)
	document.rebuildParagraphs()

	return document, nil
}

func SaveDOCX(document *Document, targetPath string) error {
	if document == nil {
		return fmt.Errorf("document is nil")
	}
	workingDocument := document.Clone()
	if workingDocument == nil {
		return fmt.Errorf("document is nil")
	}

	resolvedPath := strings.TrimSpace(targetPath)
	if resolvedPath == "" {
		return fmt.Errorf("target path is required")
	}
	workingDocument.Meta.HeaderFooters = prepareHeaderFooterPartsForSave(workingDocument.Meta.HeaderFooters)
	sectionXML := workingDocument.Meta.SectionXML
	var err error
	if hasSectionLayoutPatch(workingDocument.Meta.SectionLayout) {
		sectionXML, err = mergeSectionLayout(sectionXML, workingDocument.Meta.SectionLayout)
		if err != nil {
			return err
		}
	}
	sectionXML, err = ensureSectionHeaderFooterReferences(sectionXML, workingDocument.Meta.HeaderFooters)
	if err != nil {
		return err
	}
	workingDocument.Meta.SectionXML = sectionXML
	workingDocument.Meta.SectionLayout = parseSectionLayout(sectionXML)

	documentXML, err := buildDocumentXML(workingDocument)
	if err != nil {
		return err
	}
	stylesXML, err := buildStylesXML(workingDocument)
	if err != nil {
		return err
	}
	numberingXML, err := buildNumberingXML(workingDocument)
	if err != nil {
		return err
	}

	if sourcePath := strings.TrimSpace(workingDocument.SourcePath); sourcePath != "" {
		if _, err := os.Stat(sourcePath); err == nil {
			return rewriteDOCXFromSource(sourcePath, resolvedPath, documentXML, stylesXML, numberingXML, workingDocument.Meta.HeaderFooters)
		}
	}
	if documentContainsImages(workingDocument) {
		return fmt.Errorf("saving image-bearing documents without source package is not supported yet")
	}

	return writeMinimalDOCX(resolvedPath, documentXML, stylesXML, numberingXML, workingDocument.Meta.HeaderFooters)
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", file.Name, err)
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func parseRunElementText(element xmlRunElement) string {
	if element.InnerXML == "" {
		return ""
	}
	var text xmlText
	if err := xml.Unmarshal([]byte(wrapWordMLElement("t", element.InnerXML)), &text); err != nil {
		return element.InnerXML
	}
	return text.Value
}

func runElementAttrValue(element xmlRunElement, localName string) string {
	for _, attr := range element.Attrs {
		if attr.Name.Local == localName {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func parseRunElementDrawing(element xmlRunElement) (xmlDrawing, error) {
	var drawing xmlDrawing
	if err := xml.Unmarshal([]byte(wrapWordMLElement("drawing", element.InnerXML)), &drawing); err != nil {
		return xmlDrawing{}, err
	}
	return drawing, nil
}

func parseRunElementLegacyImage(element xmlRunElement, style TextStyle, relationships documentRelationshipIndex) *Run {
	raw := strings.TrimSpace(element.InnerXML)
	if raw == "" {
		return nil
	}

	decoder := xml.NewDecoder(strings.NewReader(wrapLegacyPictureMLElement("pict", raw)))
	image := RunImage{}
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		startElement, ok := token.(xml.StartElement)
		if !ok || startElement.Name.Local != "imagedata" {
			continue
		}
		for _, attr := range startElement.Attr {
			switch attr.Name.Local {
			case "id":
				if image.RelationshipID == "" {
					image.RelationshipID = strings.TrimSpace(attr.Value)
				}
			case "title":
				if image.Name == "" {
					image.Name = strings.TrimSpace(attr.Value)
				}
			case "src":
				if image.Target == "" {
					image.Target = normalizeRelationshipTarget(attr.Value)
				}
			}
		}
		break
	}

	if image.RelationshipID == "" && image.Target == "" {
		return nil
	}
	if relationship, ok := relationships[image.RelationshipID]; ok {
		if image.Target == "" {
			image.Target = relationship.Target
		}
		if image.Filename == "" {
			image.Filename = relationship.Filename
		}
		if image.ContentType == "" {
			image.ContentType = relationship.ContentType
		}
		if image.Name == "" {
			image.Name = relationship.Name
		}
	}
	if image.Filename == "" && image.Target != "" {
		image.Filename = filepath.Base(image.Target)
	}

	run := Run{
		ID:         nextID("run"),
		Style:      style,
		DrawingXML: raw,
		Image:      &image,
	}
	return &run
}

func parseDrawingRun(drawing xmlDrawing, style TextStyle, relationships documentRelationshipIndex) Run {
	run := Run{
		ID:         nextID("run"),
		Style:      style,
		DrawingXML: strings.TrimSpace(drawing.InnerXML),
	}
	container := firstDrawingContainer(drawing)
	if container == nil {
		return run
	}
	image := RunImage{Inline: drawing.Inline != nil}
	if container.DocPr != nil {
		image.Name = container.DocPr.Name
	}
	if container.Extent != nil {
		image.WidthEMU = container.Extent.CX
		image.HeightEMU = container.Extent.CY
	}
	if relationshipID := drawingRelationshipID(container); relationshipID != "" {
		image.RelationshipID = relationshipID
		if relationship, ok := relationships[relationshipID]; ok {
			image.Target = relationship.Target
			image.Filename = relationship.Filename
			image.ContentType = relationship.ContentType
		}
	}
	if image.RelationshipID != "" || run.DrawingXML != "" {
		run.Image = &image
	}
	return run
}

func firstDrawingContainer(drawing xmlDrawing) *xmlDrawingContainer {
	if drawing.Inline != nil {
		return drawing.Inline
	}
	return drawing.Anchor
}

func drawingRelationshipID(container *xmlDrawingContainer) string {
	if container == nil || container.Graphic == nil || container.Graphic.GraphicData == nil || container.Graphic.GraphicData.Picture == nil || container.Graphic.GraphicData.Picture.BlipFill == nil || container.Graphic.GraphicData.Picture.BlipFill.Blip == nil {
		return ""
	}
	blip := container.Graphic.GraphicData.Picture.BlipFill.Blip
	if embedID := strings.TrimSpace(blip.Embed); embedID != "" {
		return embedID
	}
	return strings.TrimSpace(blip.Link)
}

func parseDocumentRelationshipIndex(relationshipsXML, contentTypesXML []byte) (documentRelationshipIndex, error) {
	index := documentRelationshipIndex{}
	if len(relationshipsXML) == 0 {
		return index, nil
	}
	lookup, err := parseContentTypeLookup(contentTypesXML)
	if err != nil {
		return nil, err
	}
	var relationships xmlRelationships
	if err := xml.Unmarshal(relationshipsXML, &relationships); err != nil {
		return nil, fmt.Errorf("parse document relationships: %w", err)
	}
	for _, relationship := range relationships.Relationships {
		if !strings.Contains(strings.ToLower(relationship.Type), "/image") {
			continue
		}
		target := normalizeRelationshipTarget(relationship.Target)
		index[relationship.ID] = RunImage{
			RelationshipID: relationship.ID,
			Target:         target,
			Filename:       filepath.Base(target),
			ContentType:    resolveContentType(lookup, "/word/"+target),
		}
	}
	return index, nil
}

func summarizeOOXMLObjects(content []byte) *OOXMLObjectSummary {
	if len(content) == 0 {
		return nil
	}
	summary := &OOXMLObjectSummary{}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	tableComplexStack := make([]bool, 0)
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "tbl":
				if len(tableComplexStack) > 0 {
					summary.NestedTables++
					tableComplexStack[len(tableComplexStack)-1] = true
				}
				tableComplexStack = append(tableComplexStack, false)
			case "gridSpan", "hMerge", "vMerge":
				summary.MergedTableCells++
				if len(tableComplexStack) > 0 {
					tableComplexStack[len(tableComplexStack)-1] = true
				}
			case "inline":
				summary.InlineImages++
			case "anchor":
				summary.AnchoredImages++
			case "pict":
				summary.LegacyImages++
			case "txbxContent":
				summary.TextBoxes++
			case "wsp":
				summary.Shapes++
			case "blip":
				for _, attr := range element.Attr {
					if attr.Name.Local == "link" && strings.TrimSpace(attr.Value) != "" {
						summary.LinkedImages++
						break
					}
				}
			case "hyperlink":
				summary.Hyperlinks++
			case "bookmarkStart":
				summary.Bookmarks++
			case "fldSimple":
				summary.SimpleFields++
			case "instrText":
				summary.ComplexFields++
			case "footnoteReference":
				summary.FootnoteReferences++
			case "endnoteReference":
				summary.EndnoteReferences++
			case "commentReference":
				summary.CommentReferences++
			case "ins":
				summary.Insertions++
			case "del":
				summary.Deletions++
			}
		case xml.EndElement:
			if element.Name.Local != "tbl" || len(tableComplexStack) == 0 {
				continue
			}
			lastIndex := len(tableComplexStack) - 1
			wasComplex := tableComplexStack[lastIndex]
			tableComplexStack = tableComplexStack[:lastIndex]
			if wasComplex {
				summary.ComplexTables++
				if len(tableComplexStack) > 0 {
					tableComplexStack[len(tableComplexStack)-1] = true
				}
			}
		}
	}
	if *summary == (OOXMLObjectSummary{}) {
		return nil
	}
	return summary
}

func parseContentTypeLookup(content []byte) (contentTypeLookup, error) {
	lookup := contentTypeLookup{defaults: map[string]string{}, overrides: map[string]string{}}
	if len(content) == 0 {
		return lookup, nil
	}
	var types xmlContentTypes
	if err := xml.Unmarshal(content, &types); err != nil {
		return lookup, fmt.Errorf("parse content types: %w", err)
	}
	for _, item := range types.Defaults {
		lookup.defaults[strings.ToLower(strings.TrimSpace(item.Extension))] = strings.TrimSpace(item.ContentType)
	}
	for _, item := range types.Overrides {
		lookup.overrides[strings.TrimSpace(item.PartName)] = strings.TrimSpace(item.ContentType)
	}
	return lookup, nil
}

func resolveContentType(lookup contentTypeLookup, partName string) string {
	trimmed := strings.TrimSpace(partName)
	if contentType := lookup.overrides[trimmed]; contentType != "" {
		return contentType
	}
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(trimmed)), ".")
	return lookup.defaults[extension]
}

func normalizeRelationshipTarget(target string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, "./")
	return trimmed
}

func documentContainsImages(document *Document) bool {
	if document == nil {
		return false
	}
	document.ensureBlockStructure()
	if blocksContainImages(document.Blocks) {
		return true
	}
	for _, part := range document.Meta.HeaderFooters {
		if blocksContainImages(part.Blocks) {
			return true
		}
	}
	return false
}

func blocksContainImages(blocks []Block) bool {
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for _, row := range block.Table.Rows {
				for _, cell := range row.Cells {
					if blocksContainImages(cell.Blocks) {
						return true
					}
				}
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			for _, run := range block.Paragraph.Runs {
				if run.Image != nil || strings.TrimSpace(run.DrawingXML) != "" {
					return true
				}
			}
		}
	}
	return false
}

func buildDocumentSections(files []*zip.File, blocks []Block, finalSectionXML string, relationshipsXML, contentTypesXML []byte, theme *DocumentTheme) ([]DocumentSection, error) {
	if len(blocks) == 0 {
		return nil, nil
	}

	sections := make([]DocumentSection, 0)
	sectionStart := 0
	for index, block := range blocks {
		paragraph := block.Paragraph
		if block.Type != BlockTypeParagraph || paragraph == nil || strings.TrimSpace(paragraph.SectionBreakXML) == "" {
			continue
		}
		headerFooters, err := parseHeaderFooterParts(files, paragraph.SectionBreakXML, relationshipsXML, contentTypesXML, theme)
		if err != nil {
			return nil, err
		}
		layout := parseSectionLayout(paragraph.SectionBreakXML)
		sections = append(sections, DocumentSection{
			StartBlockIndex: sectionStart,
			EndBlockIndex:   index,
			Layout:          layout,
			HeaderFooters:   headerFooters,
		})
		sectionStart = index + 1
	}

	if sectionStart <= len(blocks)-1 {
		headerFooters, err := parseHeaderFooterParts(files, finalSectionXML, relationshipsXML, contentTypesXML, theme)
		if err != nil {
			return nil, err
		}
		sections = append(sections, DocumentSection{
			StartBlockIndex: sectionStart,
			EndBlockIndex:   len(blocks) - 1,
			Layout:          parseSectionLayout(finalSectionXML),
			HeaderFooters:   headerFooters,
		})
	}

	if len(sections) == 0 {
		sections = append(sections, DocumentSection{
			StartBlockIndex: 0,
			EndBlockIndex:   len(blocks) - 1,
			Layout:          parseSectionLayout(finalSectionXML),
		})
	}

	return sections, nil
}

func parseBlockElements(elements []xmlBodyElement, theme *DocumentTheme, relationships documentRelationshipIndex) ([]Block, string, error) {
	blocks := make([]Block, 0, len(elements))
	sectionXML := ""
	for _, element := range elements {
		switch element.XMLName.Local {
		case "p":
			paragraph, err := parseParagraphElement(element.InnerXML, theme, relationships)
			if err != nil {
				return nil, "", err
			}
			blocks = append(blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
		case "tbl":
			table, err := parseTableElement(element.InnerXML, theme, relationships)
			if err != nil {
				return nil, "", err
			}
			blocks = append(blocks, Block{Type: BlockTypeTable, Table: &table})
		case "sectPr":
			sectionXML = strings.TrimSpace(element.InnerXML)
		}
	}
	return blocks, sectionXML, nil
}

func parseParagraphElement(innerXML string, theme *DocumentTheme, relationships documentRelationshipIndex) (Paragraph, error) {
	var node xmlParagraph
	if err := xml.Unmarshal([]byte(wrapWordMLElement("p", innerXML)), &node); err != nil {
		return Paragraph{}, fmt.Errorf("parse paragraph element: %w", err)
	}
	return parseParagraphNode(node, theme, relationships), nil
}

func parseParagraphNode(node xmlParagraph, theme *DocumentTheme, relationships documentRelationshipIndex) Paragraph {
	paragraph := Paragraph{ID: nextID("para")}
	if node.Properties != nil {
		paragraph.Style = parseParagraphStyle(node.Properties)
		if node.Properties.Section != nil {
			sectionXML := strings.TrimSpace(node.Properties.Section.InnerXML)
			if sectionXML != "" {
				paragraph.SectionBreakXML = sectionXML
				layout := parseSectionLayout(sectionXML)
				paragraph.SectionBreakLayout = &layout
			}
		}
	}
	for _, runNode := range node.Runs {
		baseStyle := TextStyle{}
		if runNode.Properties != nil {
			if runNode.Properties.Style != nil {
				baseStyle.StyleID = runNode.Properties.Style.Value
			}
			mergeTextStyle(&baseStyle, parseRunStyle(runNode.Properties, theme))
		}
		var textBuilder strings.Builder
		flushText := func() {
			if textBuilder.Len() == 0 {
				return
			}
			paragraph.Runs = append(paragraph.Runs, Run{ID: nextID("run"), Text: textBuilder.String(), Style: baseStyle})
			textBuilder.Reset()
		}
		for _, element := range runNode.Elements {
			switch element.XMLName.Local {
			case "t":
				textBuilder.WriteString(parseRunElementText(element))
			case "br":
				flushText()
				breakType := normalizeRunBreakType(runElementAttrValue(element, "type"))
				if breakType == "" {
					continue
				}
				paragraph.Runs = append(paragraph.Runs, Run{ID: nextID("run"), BreakType: breakType, Style: baseStyle})
			case "lastRenderedPageBreak":
				flushText()
				paragraph.Runs = append(paragraph.Runs, Run{ID: nextID("run"), BreakType: RunBreakTypeRenderedPage, Style: baseStyle})
			case "drawing":
				flushText()
				drawing, err := parseRunElementDrawing(element)
				if err != nil {
					continue
				}
				paragraph.Runs = append(paragraph.Runs, parseDrawingRun(drawing, baseStyle, relationships))
			case "pict":
				flushText()
				legacyImageRun := parseRunElementLegacyImage(element, baseStyle, relationships)
				if legacyImageRun == nil {
					continue
				}
				paragraph.Runs = append(paragraph.Runs, *legacyImageRun)
			}
		}
		flushText()
	}
	if len(paragraph.Runs) == 0 {
		paragraph.Runs = []Run{{ID: nextID("run"), Text: ""}}
	}
	return paragraph
}

func parseTableElement(innerXML string, theme *DocumentTheme, relationships documentRelationshipIndex) (Table, error) {
	var node xmlTable
	if err := xml.Unmarshal([]byte(wrapWordMLElement("tbl", innerXML)), &node); err != nil {
		return Table{}, fmt.Errorf("parse table element: %w", err)
	}
	table := Table{ID: nextID("tbl")}
	if node.Properties != nil {
		table.PropertiesXML = strings.TrimSpace(node.Properties.InnerXML)
	}
	if node.Grid != nil {
		table.GridXML = strings.TrimSpace(node.Grid.InnerXML)
	}
	for _, rowNode := range node.Rows {
		row := TableRow{ID: nextID("row")}
		if rowNode.Properties != nil {
			row.PropertiesXML = strings.TrimSpace(rowNode.Properties.InnerXML)
			applyParsedTableRowProperties(&row, row.PropertiesXML)
		}
		for _, cellNode := range rowNode.Cells {
			cell, err := parseTableCellNode(cellNode, theme, relationships)
			if err != nil {
				return Table{}, err
			}
			row.Cells = append(row.Cells, cell)
		}
		table.Rows = append(table.Rows, row)
	}
	return table, nil
}

func parseTableCellNode(node xmlTableCell, theme *DocumentTheme, relationships documentRelationshipIndex) (TableCell, error) {
	cell := TableCell{ID: nextID("cell")}
	for _, element := range node.Elements {
		switch element.XMLName.Local {
		case "tcPr":
			cell.PropertiesXML = strings.TrimSpace(element.InnerXML)
		case "p":
			paragraph, err := parseParagraphElement(element.InnerXML, theme, relationships)
			if err != nil {
				return TableCell{}, err
			}
			cell.Blocks = append(cell.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
		case "tbl":
			table, err := parseTableElement(element.InnerXML, theme, relationships)
			if err != nil {
				return TableCell{}, err
			}
			cell.Blocks = append(cell.Blocks, Block{Type: BlockTypeTable, Table: &table})
		}
	}
	if len(cell.Blocks) == 0 {
		cell.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: nextID("para"), Runs: []Run{{ID: nextID("run"), Text: ""}}}}}
	}
	return cell, nil
}

func wrapWordMLElement(localName, innerXML string) string {
	return `<w:` + localName + ` xmlns:w="` + wordMLNamespace + `" xmlns:r="` + relationshipNamespace + `" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">` + innerXML + `</w:` + localName + `>`
}

func wrapLegacyPictureMLElement(localName, innerXML string) string {
	return `<w:` + localName + ` xmlns:w="` + wordMLNamespace + `" xmlns:r="` + relationshipNamespace + `" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">` + innerXML + `</w:` + localName + `>`
}

func parseInt(value string) int {
	if value == "" {
		return 0
	}
	var parsed int
	_, _ = fmt.Sscanf(value, "%d", &parsed)
	return parsed
}

func SupportsPath(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return strings.EqualFold(filepath.Ext(path), ".docx")
}

func buildDocumentXML(document *Document) ([]byte, error) {
	var builder strings.Builder
	builder.Grow(2048)
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<w:document xmlns:w="`)
	builder.WriteString(wordMLNamespace)
	builder.WriteString(`" xmlns:r="`)
	builder.WriteString(relationshipNamespace)
	builder.WriteString(`" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`)

	document.ensureBlockStructure()
	blocks := document.Blocks
	if len(blocks) == 0 {
		blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{Runs: []Run{{Text: ""}}}}}
	}

	for _, block := range blocks {
		writeBlockXML(&builder, block)
	}

	sectionXML := strings.TrimSpace(document.Meta.SectionXML)
	if sectionXML == "" {
		sectionXML = defaultSectionXML()
	}
	builder.WriteString(`<w:sectPr>`)
	builder.WriteString(sectionXML)
	builder.WriteString(`</w:sectPr></w:body></w:document>`)

	return []byte(builder.String()), nil
}

func writeBlockXML(builder *strings.Builder, block Block) {
	switch block.Type {
	case BlockTypeTable:
		if block.Table != nil {
			writeTableXML(builder, *block.Table)
		}
	default:
		if block.Paragraph != nil {
			writeParagraphXML(builder, *block.Paragraph)
		}
	}
}

func writeTableXML(builder *strings.Builder, table Table) {
	builder.WriteString(`<w:tbl>`)
	if strings.TrimSpace(table.PropertiesXML) != "" {
		builder.WriteString(`<w:tblPr>`)
		builder.WriteString(table.PropertiesXML)
		builder.WriteString(`</w:tblPr>`)
	}
	if strings.TrimSpace(table.GridXML) != "" {
		builder.WriteString(`<w:tblGrid>`)
		builder.WriteString(table.GridXML)
		builder.WriteString(`</w:tblGrid>`)
	}
	for _, row := range table.Rows {
		builder.WriteString(`<w:tr>`)
		if strings.TrimSpace(row.PropertiesXML) != "" || hasTableRowProperties(row) {
			builder.WriteString(`<w:trPr>`)
			writeTableRowPropertiesXML(builder, row)
			builder.WriteString(`</w:trPr>`)
		}
		for _, cell := range row.Cells {
			builder.WriteString(`<w:tc>`)
			if strings.TrimSpace(cell.PropertiesXML) != "" {
				builder.WriteString(`<w:tcPr>`)
				builder.WriteString(cell.PropertiesXML)
				builder.WriteString(`</w:tcPr>`)
			}
			cellBlocks := cell.Blocks
			if len(cellBlocks) == 0 {
				cellBlocks = []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{Runs: []Run{{Text: ""}}}}}
			}
			for _, cellBlock := range cellBlocks {
				writeBlockXML(builder, cellBlock)
			}
			builder.WriteString(`</w:tc>`)
		}
		builder.WriteString(`</w:tr>`)
	}
	builder.WriteString(`</w:tbl>`)
}

func writeParagraphXML(builder *strings.Builder, paragraph Paragraph) {
	builder.WriteString(`<w:p>`)
	if hasParagraphStyle(paragraph.Style) || strings.TrimSpace(paragraph.SectionBreakXML) != "" {
		builder.WriteString(`<w:pPr>`)
		writeParagraphPropertiesXML(builder, paragraph.Style, false)
		if strings.TrimSpace(paragraph.SectionBreakXML) != "" {
			builder.WriteString(`<w:sectPr>`)
			builder.WriteString(paragraph.SectionBreakXML)
			builder.WriteString(`</w:sectPr>`)
		}
		builder.WriteString(`</w:pPr>`)
	}

	runs := paragraph.Runs
	if len(runs) == 0 {
		runs = []Run{{Text: ""}}
	}
	for _, run := range runs {
		writeRunXML(builder, run)
	}
	builder.WriteString(`</w:p>`)
}

func applyParsedTableRowProperties(row *TableRow, innerXML string) {
	if row == nil || strings.TrimSpace(innerXML) == "" {
		return
	}
	var properties xmlTableRowProperties
	if err := xml.Unmarshal([]byte(wrapWordMLElement("trPr", innerXML)), &properties); err != nil {
		return
	}
	if isSet, value := parseOnOffValue(properties.RepeatHeader); isSet {
		row.RepeatHeaderSet = true
		row.RepeatHeader = value
	}
	if isSet, value := parseOnOffValue(properties.CantSplit); isSet {
		row.CantSplitSet = true
		row.CantSplit = value
	}
}

func hasTableRowProperties(row TableRow) bool {
	return row.RepeatHeaderSet || row.RepeatHeader || row.CantSplitSet || row.CantSplit
}

func writeTableRowPropertiesXML(builder *strings.Builder, row TableRow) {
	if strings.TrimSpace(row.PropertiesXML) != "" {
		builder.WriteString(row.PropertiesXML)
		return
	}
	if row.RepeatHeaderSet || row.RepeatHeader {
		writeOnOffElement(builder, "tblHeader", row.RepeatHeader)
	}
	if row.CantSplitSet || row.CantSplit {
		writeOnOffElement(builder, "cantSplit", row.CantSplit)
	}
}

func writeRunXML(builder *strings.Builder, run Run) {
	builder.WriteString(`<w:r>`)
	if hasRunStyle(run.Style) {
		builder.WriteString(`<w:rPr>`)
		if run.Style.StyleID != "" {
			builder.WriteString(`<w:rStyle w:val="`)
			builder.WriteString(escapeXML(run.Style.StyleID))
			builder.WriteString(`"/>`)
		}
		if run.Style.FontFamily != "" {
			builder.WriteString(`<w:rFonts w:ascii="`)
			builder.WriteString(escapeXML(run.Style.FontFamily))
			builder.WriteString(`" w:eastAsia="`)
			builder.WriteString(escapeXML(run.Style.FontFamily))
			if run.Style.ThemeFontASCII != "" {
				builder.WriteString(`" w:asciiTheme="`)
				builder.WriteString(escapeXML(run.Style.ThemeFontASCII))
			}
			if run.Style.ThemeFontEastAsia != "" {
				builder.WriteString(`" w:eastAsiaTheme="`)
				builder.WriteString(escapeXML(run.Style.ThemeFontEastAsia))
			}
			builder.WriteString(`"/>`)
		} else if run.Style.ThemeFontASCII != "" || run.Style.ThemeFontEastAsia != "" {
			builder.WriteString(`<w:rFonts`)
			if run.Style.ThemeFontASCII != "" {
				builder.WriteString(` w:asciiTheme="`)
				builder.WriteString(escapeXML(run.Style.ThemeFontASCII))
				builder.WriteString(`"`)
			}
			if run.Style.ThemeFontEastAsia != "" {
				builder.WriteString(` w:eastAsiaTheme="`)
				builder.WriteString(escapeXML(run.Style.ThemeFontEastAsia))
				builder.WriteString(`"`)
			}
			builder.WriteString(`/>`)
		}
		if run.Style.FontSizeHP != 0 {
			builder.WriteString(`<w:sz w:val="`)
			builder.WriteString(fmt.Sprintf("%d", run.Style.FontSizeHP))
			builder.WriteString(`"/>`)
		}
		if run.Style.BoldSet || run.Style.Bold {
			writeOnOffElement(builder, "b", run.Style.Bold)
		}
		if run.Style.ItalicSet || run.Style.Italic {
			writeOnOffElement(builder, "i", run.Style.Italic)
		}
		if run.Style.UnderlineSet || run.Style.Underline {
			if run.Style.Underline {
				builder.WriteString(`<w:u w:val="single"/>`)
			} else {
				builder.WriteString(`<w:u w:val="none"/>`)
			}
		}
		if run.Style.Color != "" || run.Style.ThemeColor != "" {
			builder.WriteString(`<w:color`)
			if run.Style.Color != "" {
				builder.WriteString(` w:val="`)
				builder.WriteString(escapeXML(run.Style.Color))
				builder.WriteString(`"`)
			}
			if run.Style.ThemeColor != "" {
				builder.WriteString(` w:themeColor="`)
				builder.WriteString(escapeXML(run.Style.ThemeColor))
				builder.WriteString(`"`)
			}
			builder.WriteString(`/>`)
		}
		builder.WriteString(`</w:rPr>`)
	}
	if run.BreakType != "" {
		writeRunBreakXML(builder, run.BreakType)
	}
	if strings.TrimSpace(run.DrawingXML) != "" {
		builder.WriteString(`<w:drawing>`)
		builder.WriteString(run.DrawingXML)
		builder.WriteString(`</w:drawing></w:r>`)
		return
	}
	builder.WriteString(`<w:t`)
	if requiresPreserveSpace(run.Text) {
		builder.WriteString(` xml:space="preserve"`)
	}
	builder.WriteString(`>`)
	builder.WriteString(escapeXML(run.Text))
	builder.WriteString(`</w:t></w:r>`)
}

func hasParagraphStyle(style ParagraphStyle) bool {
	return style.StyleID != "" || style.Alignment != "" || style.SpacingBefore != 0 || style.SpacingAfter != 0 || style.LineSpacing != 0 || style.LineSpacingRule != "" || style.FirstLineIndent != 0 || style.LeftIndent != 0 || style.KeepNextSet || style.KeepNext || style.KeepLinesSet || style.KeepLines || style.PageBreakBeforeSet || style.PageBreakBefore || style.WidowControlSet || style.WidowControl || style.Numbering != nil
}

func hasRunStyle(style TextStyle) bool {
	return style.StyleID != "" || style.FontFamily != "" || style.ThemeFontASCII != "" || style.ThemeFontEastAsia != "" || style.FontSizeHP != 0 || style.BoldSet || style.Bold || style.ItalicSet || style.Italic || style.UnderlineSet || style.Underline || style.Color != "" || style.ThemeColor != ""
}

func requiresPreserveSpace(text string) bool {
	if text == "" {
		return false
	}
	return strings.HasPrefix(text, " ") || strings.HasSuffix(text, " ") || strings.Contains(text, "  ")
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func defaultSectionXML() string {
	return `<w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/>`
}

func rewriteDOCXFromSource(sourcePath, targetPath string, documentXML, stylesXML, numberingXML []byte, headerFooters []HeaderFooterPart) error {
	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return fmt.Errorf("open source docx: %w", err)
	}
	hasStyles := len(stylesXML) > 0
	hasNumbering := len(numberingXML) > 0
	contentTypesXML, err := readOptionalZipEntry(reader.File, "[Content_Types].xml")
	if err != nil {
		_ = reader.Close()
		return err
	}
	relsXML, err := readOptionalZipEntry(reader.File, "word/_rels/document.xml.rels")
	if err != nil {
		_ = reader.Close()
		return err
	}
	contentTypesXML, err = updateContentTypesXML(contentTypesXML, hasStyles, hasNumbering, headerFooters)
	if err != nil {
		_ = reader.Close()
		return err
	}
	relsXML, err = updateDocumentRelationshipsXML(relsXML, hasStyles, hasNumbering, headerFooters)
	if err != nil {
		_ = reader.Close()
		return err
	}
	overwrittenHeaderFooterEntries := headerFooterEntryPaths(headerFooters)

	tempPath, err := writeTempDOCX(targetPath, func(writer *zip.Writer) error {
		for _, file := range reader.File {
			if file.Name == "word/document.xml" || file.Name == "[Content_Types].xml" || file.Name == "word/_rels/document.xml.rels" {
				continue
			}
			if _, ok := overwrittenHeaderFooterEntries[file.Name]; ok {
				continue
			}
			if hasStyles && file.Name == "word/styles.xml" {
				continue
			}
			if hasNumbering && file.Name == "word/numbering.xml" {
				continue
			}
			if err := copyZipEntry(writer, file); err != nil {
				return err
			}
		}
		if err := writeZipEntry(writer, "[Content_Types].xml", contentTypesXML); err != nil {
			return err
		}
		if err := writeZipEntry(writer, "word/_rels/document.xml.rels", relsXML); err != nil {
			return err
		}
		if err := writeZipEntry(writer, "word/document.xml", documentXML); err != nil {
			return err
		}
		if err := writeHeaderFooterEntries(writer, headerFooters); err != nil {
			return err
		}
		if hasStyles {
			if err := writeZipEntry(writer, "word/styles.xml", stylesXML); err != nil {
				return err
			}
		}
		if hasNumbering {
			if err := writeZipEntry(writer, "word/numbering.xml", numberingXML); err != nil {
				return err
			}
		}
		return nil
	})
	closeErr := reader.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close source docx: %w", closeErr)
	}

	return replaceFile(tempPath, targetPath)
}

func writeMinimalDOCX(targetPath string, documentXML, stylesXML, numberingXML []byte, headerFooters []HeaderFooterPart) error {
	tempPath, err := writeTempDOCX(targetPath, func(writer *zip.Writer) error {
		createdAt := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		hasStyles := len(stylesXML) > 0
		hasNumbering := len(numberingXML) > 0
		contentTypesXML, err := updateContentTypesXML(nil, hasStyles, hasNumbering, headerFooters)
		if err != nil {
			return err
		}
		relsXML, err := updateDocumentRelationshipsXML(nil, hasStyles, hasNumbering, headerFooters)
		if err != nil {
			return err
		}
		files := map[string][]byte{
			"[Content_Types].xml":          contentTypesXML,
			"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`),
			"docProps/app.xml":             []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>XiaoYing go-word</Application></Properties>`),
			"docProps/core.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>go-word document</dc:title><dc:creator>XiaoYing</dc:creator><cp:lastModifiedBy>XiaoYing</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + createdAt + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + createdAt + `</dcterms:modified></cp:coreProperties>`),
			"word/_rels/document.xml.rels": relsXML,
		}

		for name, content := range files {
			if err := writeZipEntry(writer, name, content); err != nil {
				return err
			}
		}

		if err := writeZipEntry(writer, "word/document.xml", documentXML); err != nil {
			return err
		}
		if err := writeHeaderFooterEntries(writer, headerFooters); err != nil {
			return err
		}
		if hasStyles {
			if err := writeZipEntry(writer, "word/styles.xml", stylesXML); err != nil {
				return err
			}
		}
		if hasNumbering {
			if err := writeZipEntry(writer, "word/numbering.xml", numberingXML); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	return replaceFile(tempPath, targetPath)
}

func writeTempDOCX(targetPath string, populate func(writer *zip.Writer) error) (string, error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", fmt.Errorf("create target directory: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), "goword-*.docx")
	if err != nil {
		return "", fmt.Errorf("create temp docx: %w", err)
	}
	tempPath := tempFile.Name()
	keepFile := false
	defer func() {
		_ = tempFile.Close()
		if !keepFile {
			_ = os.Remove(tempPath)
		}
	}()

	writer := zip.NewWriter(tempFile)
	if err := populate(writer); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close zip writer: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}

	keepFile = true
	return tempPath, nil
}

func replaceFile(tempPath, targetPath string) error {
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tempPath)
		if isFileInUseError(err) {
			return fmt.Errorf("无法覆盖目标文档：文件正在被其他程序占用，请先关闭后重试（%s）", targetPath)
		}
		return fmt.Errorf("remove existing target: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Remove(tempPath)
		if isFileInUseError(err) {
			return fmt.Errorf("无法替换目标文档：文件正在被其他程序占用，请先关闭后重试（%s）", targetPath)
		}
		return fmt.Errorf("replace target docx: %w", err)
	}
	return nil
}

func isFileInUseError(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}

	if errno, ok := err.(syscall.Errno); ok {
		if errno == 32 {
			return true
		}
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "being used by another process") ||
		strings.Contains(message, "used by another process") ||
		strings.Contains(message, "另一个程序正在使用此文件")
}

func copyZipEntry(writer *zip.Writer, file *zip.File) error {
	header := file.FileHeader
	entryWriter, err := writer.CreateHeader(&header)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", file.Name, err)
	}
	if file.FileInfo().IsDir() {
		return nil
	}

	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", file.Name, err)
	}
	defer reader.Close()

	if _, err := io.Copy(entryWriter, reader); err != nil {
		return fmt.Errorf("copy zip entry %s: %w", file.Name, err)
	}
	return nil
}

func writeZipEntry(writer *zip.Writer, name string, content []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o644)
	entryWriter, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := io.Copy(entryWriter, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}

func parseStylesXML(content []byte, theme *DocumentTheme) ([]DocumentStyle, *ParagraphStyle, *TextStyle, error) {
	var parsed xmlStyles
	if err := xml.Unmarshal(content, &parsed); err != nil {
		return nil, nil, nil, fmt.Errorf("parse styles.xml: %w", err)
	}
	styles := make([]DocumentStyle, 0, len(parsed.Styles))
	var defaultParagraphStyle *ParagraphStyle
	var defaultTextStyle *TextStyle
	if parsed.DocDefaults != nil {
		if parsed.DocDefaults.ParagraphDefaults != nil && parsed.DocDefaults.ParagraphDefaults.Properties != nil {
			paragraphStyle := parseParagraphStyle(parsed.DocDefaults.ParagraphDefaults.Properties)
			defaultParagraphStyle = &paragraphStyle
		}
		if parsed.DocDefaults.RunDefaults != nil && parsed.DocDefaults.RunDefaults.Properties != nil {
			textStyle := parseRunStyle(parsed.DocDefaults.RunDefaults.Properties, theme)
			defaultTextStyle = &textStyle
		}
	}
	for _, styleNode := range parsed.Styles {
		style := DocumentStyle{ID: styleNode.ID, Type: styleNode.Type, IsDefault: parseXMLBool(styleNode.Default)}
		if styleNode.Name != nil {
			style.Name = styleNode.Name.Value
		}
		if styleNode.BasedOn != nil {
			style.BasedOn = styleNode.BasedOn.Value
		}
		if styleNode.ParagraphProperties != nil {
			style.Paragraph = parseParagraphStyle(styleNode.ParagraphProperties)
		}
		if styleNode.RunProperties != nil {
			style.Text = parseRunStyle(styleNode.RunProperties, theme)
		}
		styles = append(styles, style)
	}
	return styles, defaultParagraphStyle, defaultTextStyle, nil
}

func parseNumberingXML(content []byte, theme *DocumentTheme) ([]NumberingDefinition, error) {
	var parsed xmlNumbering
	if err := xml.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("parse numbering.xml: %w", err)
	}
	abstracts := make(map[int][]NumberingLevel, len(parsed.Abstracts))
	for _, abstract := range parsed.Abstracts {
		levels := make([]NumberingLevel, 0, len(abstract.Levels))
		for _, levelNode := range abstract.Levels {
			level := NumberingLevel{Level: levelNode.Level}
			if levelNode.Start != nil {
				level.Start = levelNode.Start.Value
			}
			if levelNode.Format != nil {
				level.Format = levelNode.Format.Value
			}
			if levelNode.Text != nil {
				level.Text = levelNode.Text.Value
			}
			if levelNode.ParagraphProperties != nil {
				level.Paragraph = parseParagraphStyle(levelNode.ParagraphProperties)
			}
			if levelNode.RunProperties != nil {
				level.TextStyle = parseRunStyle(levelNode.RunProperties, theme)
			}
			levels = append(levels, level)
		}
		abstracts[abstract.ID] = levels
	}
	definitions := make([]NumberingDefinition, 0, len(parsed.Nums))
	for _, num := range parsed.Nums {
		definition := NumberingDefinition{NumID: fmt.Sprintf("%d", num.ID)}
		if num.AbstractID != nil {
			definition.AbstractID = fmt.Sprintf("%d", num.AbstractID.Value)
			definition.Levels = append(definition.Levels, abstracts[num.AbstractID.Value]...)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func parseParagraphStyle(properties *xmlParagraphProperties) ParagraphStyle {
	style := ParagraphStyle{}
	if properties == nil {
		return style
	}
	if properties.Style != nil {
		style.StyleID = properties.Style.Value
	}
	if properties.Alignment != nil {
		style.Alignment = properties.Alignment.Value
	}
	if properties.Spacing != nil {
		style.SpacingBefore = parseInt(properties.Spacing.Before)
		style.SpacingAfter = parseInt(properties.Spacing.After)
		style.LineSpacing = parseInt(properties.Spacing.Line)
		style.LineSpacingRule = strings.TrimSpace(properties.Spacing.LineRule)
	}
	if properties.Indent != nil {
		style.FirstLineIndent = parseInt(properties.Indent.FirstLine)
		style.LeftIndent = parseInt(properties.Indent.Left)
	}
	if isSet, value := parseOnOffValue(properties.KeepNext); isSet {
		style.KeepNextSet = true
		style.KeepNext = value
	}
	if isSet, value := parseOnOffValue(properties.KeepLines); isSet {
		style.KeepLinesSet = true
		style.KeepLines = value
	}
	if isSet, value := parseOnOffValue(properties.PageBreakBefore); isSet {
		style.PageBreakBeforeSet = true
		style.PageBreakBefore = value
	}
	if isSet, value := parseOnOffValue(properties.WidowControl); isSet {
		style.WidowControlSet = true
		style.WidowControl = value
	}
	if properties.Numbering != nil && properties.Numbering.NumID != nil {
		style.Numbering = &NumberingRef{NumID: fmt.Sprintf("%d", properties.Numbering.NumID.Value)}
		if properties.Numbering.Level != nil {
			style.Numbering.Level = properties.Numbering.Level.Value
		}
	}
	return style
}

func parseRunStyle(properties *xmlRunProperties, theme *DocumentTheme) TextStyle {
	style := TextStyle{}
	if properties == nil {
		return style
	}
	if properties.Style != nil {
		style.StyleID = properties.Style.Value
	}
	style.BoldSet, style.Bold = parseOnOffValue(properties.Bold)
	style.ItalicSet, style.Italic = parseOnOffValue(properties.Italic)
	style.UnderlineSet, style.Underline = parseUnderlineValue(properties.Underline)
	if properties.Color != nil {
		style.Color = normalizeHexColor(properties.Color.Value)
		style.ThemeColor = properties.Color.ThemeColor
		if style.Color == "" && style.ThemeColor != "" {
			style.Color = resolveThemeColor(theme, style.ThemeColor)
		}
	}
	if properties.Size != nil {
		style.FontSizeHP = properties.Size.Value
	}
	if properties.Fonts != nil {
		style.ThemeFontASCII = properties.Fonts.ASCIITheme
		style.ThemeFontEastAsia = properties.Fonts.EastAsiaTheme
		style.FontFamily = firstNonEmpty(properties.Fonts.EastAsia, properties.Fonts.ASCII)
		if style.FontFamily == "" {
			style.FontFamily = resolveThemeFont(theme, style.ThemeFontEastAsia, style.ThemeFontASCII)
		}
	}
	return style
}

func buildStylesXML(document *Document) ([]byte, error) {
	if document == nil {
		return nil, nil
	}
	if len(document.Meta.Styles) == 0 && document.Meta.DefaultParagraphStyle == nil && document.Meta.DefaultTextStyle == nil {
		return nil, nil
	}
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<w:styles xmlns:w="`)
	builder.WriteString(wordMLNamespace)
	builder.WriteString(`">`)
	if document.Meta.DefaultParagraphStyle != nil || document.Meta.DefaultTextStyle != nil {
		builder.WriteString(`<w:docDefaults>`)
		if document.Meta.DefaultParagraphStyle != nil {
			builder.WriteString(`<w:pPrDefault>`)
			writeParagraphPropertiesXML(&builder, *document.Meta.DefaultParagraphStyle, true)
			builder.WriteString(`</w:pPrDefault>`)
		}
		if document.Meta.DefaultTextStyle != nil {
			builder.WriteString(`<w:rPrDefault>`)
			writeRunPropertiesXML(&builder, *document.Meta.DefaultTextStyle, true)
			builder.WriteString(`</w:rPrDefault>`)
		}
		builder.WriteString(`</w:docDefaults>`)
	}
	for _, style := range document.Meta.Styles {
		builder.WriteString(`<w:style w:type="`)
		builder.WriteString(escapeXML(firstNonEmpty(style.Type, "paragraph")))
		builder.WriteString(`" w:styleId="`)
		builder.WriteString(escapeXML(style.ID))
		if style.IsDefault {
			builder.WriteString(`" w:default="1"`)
		} else {
			builder.WriteString(`"`)
		}
		builder.WriteString(`>`)
		if style.Name != "" {
			builder.WriteString(`<w:name w:val="`)
			builder.WriteString(escapeXML(style.Name))
			builder.WriteString(`"/>`)
		}
		if style.BasedOn != "" {
			builder.WriteString(`<w:basedOn w:val="`)
			builder.WriteString(escapeXML(style.BasedOn))
			builder.WriteString(`"/>`)
		}
		if hasParagraphStyle(style.Paragraph) {
			builder.WriteString(`<w:pPr>`)
			writeParagraphPropertiesXML(&builder, style.Paragraph, false)
			builder.WriteString(`</w:pPr>`)
		}
		if hasRunStyle(style.Text) {
			builder.WriteString(`<w:rPr>`)
			writeRunPropertiesXML(&builder, style.Text, false)
			builder.WriteString(`</w:rPr>`)
		}
		builder.WriteString(`</w:style>`)
	}
	builder.WriteString(`</w:styles>`)
	return []byte(builder.String()), nil
}

func applyResolvedStyles(document *Document) {
	if document == nil {
		return
	}
	document.ensureBlockStructure()
	styleIndex := make(map[string]DocumentStyle, len(document.Meta.Styles))
	for _, style := range document.Meta.Styles {
		if style.ID != "" {
			styleIndex[style.ID] = style
		}
	}
	applyResolvedStylesToBlocks(document.Blocks, document.Meta, styleIndex)
	for index := range document.Meta.HeaderFooters {
		applyResolvedStylesToBlocks(document.Meta.HeaderFooters[index].Blocks, document.Meta, styleIndex)
		document.Meta.HeaderFooters[index].rebuildParagraphs()
	}
	document.rebuildParagraphs()

}

func applyResolvedStylesToBlocks(blocks []Block, meta DocumentMeta, styleIndex map[string]DocumentStyle) {
	for blockIndex := range blocks {
		block := &blocks[blockIndex]
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex := range block.Table.Rows {
				for cellIndex := range block.Table.Rows[rowIndex].Cells {
					applyResolvedStylesToBlocks(block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, meta, styleIndex)
				}
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			applyResolvedStylesToParagraph(block.Paragraph, meta, styleIndex)
		}
	}
}

func applyResolvedStylesToParagraph(paragraph *Paragraph, meta DocumentMeta, styleIndex map[string]DocumentStyle) {
	if paragraph == nil {
		return
	}
	resolvedParagraphStyle := ParagraphStyle{}
	if meta.DefaultParagraphStyle != nil {
		mergeParagraphStyle(&resolvedParagraphStyle, *meta.DefaultParagraphStyle)
	}
	if paragraph.Style.StyleID != "" {
		mergeParagraphStyle(&resolvedParagraphStyle, resolveParagraphStyleByID(styleIndex, paragraph.Style.StyleID, nil))
	}
	mergeParagraphStyle(&resolvedParagraphStyle, paragraph.Style)
	paragraph.Style = resolvedParagraphStyle

	paragraphTextBase := TextStyle{}
	if meta.DefaultTextStyle != nil {
		mergeTextStyle(&paragraphTextBase, *meta.DefaultTextStyle)
	}
	if paragraph.Style.StyleID != "" {
		mergeTextStyle(&paragraphTextBase, resolveTextStyleByID(styleIndex, paragraph.Style.StyleID, nil))
	}

	for runIndex := range paragraph.Runs {
		run := &paragraph.Runs[runIndex]
		resolvedRunStyle := paragraphTextBase
		if run.Style.StyleID != "" {
			mergeTextStyle(&resolvedRunStyle, resolveTextStyleByID(styleIndex, run.Style.StyleID, nil))
		}
		mergeTextStyle(&resolvedRunStyle, run.Style)
		run.Style = resolvedRunStyle
	}
}

func resolveParagraphStyleByID(styleIndex map[string]DocumentStyle, styleID string, visited map[string]bool) ParagraphStyle {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if styleID == "" || visited[styleID] {
		return ParagraphStyle{}
	}
	visited[styleID] = true
	style, ok := styleIndex[styleID]
	if !ok {
		return ParagraphStyle{}
	}
	resolved := ParagraphStyle{}
	if style.BasedOn != "" {
		mergeParagraphStyle(&resolved, resolveParagraphStyleByID(styleIndex, style.BasedOn, visited))
	}
	mergeParagraphStyle(&resolved, style.Paragraph)
	if resolved.StyleID == "" {
		resolved.StyleID = style.ID
	}
	return resolved
}

func resolveTextStyleByID(styleIndex map[string]DocumentStyle, styleID string, visited map[string]bool) TextStyle {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if styleID == "" || visited[styleID] {
		return TextStyle{}
	}
	visited[styleID] = true
	style, ok := styleIndex[styleID]
	if !ok {
		return TextStyle{}
	}
	resolved := TextStyle{}
	if style.BasedOn != "" {
		mergeTextStyle(&resolved, resolveTextStyleByID(styleIndex, style.BasedOn, visited))
	}
	mergeTextStyle(&resolved, style.Text)
	if resolved.StyleID == "" {
		resolved.StyleID = style.ID
	}
	return resolved
}

func mergeParagraphStyle(dst *ParagraphStyle, src ParagraphStyle) {
	if dst == nil {
		return
	}
	if src.StyleID != "" {
		dst.StyleID = src.StyleID
	}
	if src.Alignment != "" {
		dst.Alignment = src.Alignment
	}
	if src.SpacingBefore != 0 {
		dst.SpacingBefore = src.SpacingBefore
	}
	if src.SpacingAfter != 0 {
		dst.SpacingAfter = src.SpacingAfter
	}
	if src.LineSpacing != 0 {
		dst.LineSpacing = src.LineSpacing
	}
	if src.LineSpacingRule != "" {
		dst.LineSpacingRule = src.LineSpacingRule
	}
	if src.FirstLineIndent != 0 {
		dst.FirstLineIndent = src.FirstLineIndent
	}
	if src.LeftIndent != 0 {
		dst.LeftIndent = src.LeftIndent
	}
	if src.KeepNextSet || src.KeepNext {
		dst.KeepNextSet = true
		dst.KeepNext = src.KeepNext
	}
	if src.KeepLinesSet || src.KeepLines {
		dst.KeepLinesSet = true
		dst.KeepLines = src.KeepLines
	}
	if src.PageBreakBeforeSet || src.PageBreakBefore {
		dst.PageBreakBeforeSet = true
		dst.PageBreakBefore = src.PageBreakBefore
	}
	if src.WidowControlSet || src.WidowControl {
		dst.WidowControlSet = true
		dst.WidowControl = src.WidowControl
	}
	if src.Numbering != nil {
		copyValue := *src.Numbering
		dst.Numbering = &copyValue
	}
}

func mergeTextStyle(dst *TextStyle, src TextStyle) {
	if dst == nil {
		return
	}
	if src.StyleID != "" {
		dst.StyleID = src.StyleID
	}
	if src.FontFamily != "" {
		dst.FontFamily = src.FontFamily
	}
	if src.ThemeFontASCII != "" {
		dst.ThemeFontASCII = src.ThemeFontASCII
	}
	if src.ThemeFontEastAsia != "" {
		dst.ThemeFontEastAsia = src.ThemeFontEastAsia
	}
	if src.FontSizeHP != 0 {
		dst.FontSizeHP = src.FontSizeHP
	}
	if src.BoldSet || src.Bold {
		dst.BoldSet = true
		dst.Bold = src.Bold
	}
	if src.ItalicSet || src.Italic {
		dst.ItalicSet = true
		dst.Italic = src.Italic
	}
	if src.UnderlineSet || src.Underline {
		dst.UnderlineSet = true
		dst.Underline = src.Underline
	}
	if src.Color != "" {
		dst.Color = src.Color
	}
	if src.ThemeColor != "" {
		dst.ThemeColor = src.ThemeColor
	}
}

func parseXMLBool(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return trimmed == "1" || trimmed == "true" || trimmed == "on"
}

func parseOnOffValue(node *xmlOnOff) (bool, bool) {
	if node == nil {
		return false, false
	}
	trimmed := strings.TrimSpace(strings.ToLower(node.Value))
	if trimmed == "" {
		return true, true
	}
	return true, !(trimmed == "0" || trimmed == "false" || trimmed == "off")
}

func parseUnderlineValue(node *xmlUnderline) (bool, bool) {
	if node == nil {
		return false, false
	}
	trimmed := strings.TrimSpace(strings.ToLower(node.Value))
	if trimmed == "" {
		return true, true
	}
	return true, !(trimmed == "none" || trimmed == "0" || trimmed == "false" || trimmed == "off")
}

func normalizeHexColor(value string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if len(trimmed) != 6 {
		return ""
	}
	return strings.ToUpper(trimmed)
}

func resolveThemeColor(theme *DocumentTheme, key string) string {
	if theme == nil || theme.Colors == nil {
		return ""
	}
	return normalizeHexColor(theme.Colors[key])
}

func resolveThemeFont(theme *DocumentTheme, eastAsiaKey, asciiKey string) string {
	if theme == nil || theme.Fonts == nil {
		return ""
	}
	if font := strings.TrimSpace(theme.Fonts[eastAsiaKey]); font != "" {
		return font
	}
	return strings.TrimSpace(theme.Fonts[asciiKey])
}

func parseThemeXML(content []byte) (*DocumentTheme, error) {
	var parsed xmlTheme
	if err := xml.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("parse theme1.xml: %w", err)
	}
	theme := &DocumentTheme{Colors: map[string]string{}, Fonts: map[string]string{}}
	if parsed.ThemeElements == nil {
		return theme, nil
	}
	if scheme := parsed.ThemeElements.ColorScheme; scheme != nil {
		addThemeColor(theme.Colors, "dk1", scheme.Dark1)
		addThemeColor(theme.Colors, "lt1", scheme.Light1)
		addThemeColor(theme.Colors, "dk2", scheme.Dark2)
		addThemeColor(theme.Colors, "lt2", scheme.Light2)
		addThemeColor(theme.Colors, "accent1", scheme.Accent1)
		addThemeColor(theme.Colors, "accent2", scheme.Accent2)
		addThemeColor(theme.Colors, "accent3", scheme.Accent3)
		addThemeColor(theme.Colors, "accent4", scheme.Accent4)
		addThemeColor(theme.Colors, "accent5", scheme.Accent5)
		addThemeColor(theme.Colors, "accent6", scheme.Accent6)
		addThemeColor(theme.Colors, "hyperlink", scheme.Hyperlink)
		addThemeColor(theme.Colors, "followedHyperlink", scheme.Followed)
	}
	if fontScheme := parsed.ThemeElements.FontScheme; fontScheme != nil {
		addThemeFont(theme.Fonts, "majorAscii", fontScheme.Major, "latin")
		addThemeFont(theme.Fonts, "majorHAnsi", fontScheme.Major, "latin")
		addThemeFont(theme.Fonts, "majorEastAsia", fontScheme.Major, "eastAsia")
		addThemeFont(theme.Fonts, "majorBidi", fontScheme.Major, "cs")
		addThemeFont(theme.Fonts, "minorAscii", fontScheme.Minor, "latin")
		addThemeFont(theme.Fonts, "minorHAnsi", fontScheme.Minor, "latin")
		addThemeFont(theme.Fonts, "minorEastAsia", fontScheme.Minor, "eastAsia")
		addThemeFont(theme.Fonts, "minorBidi", fontScheme.Minor, "cs")
	}
	return theme, nil
}

func addThemeColor(colors map[string]string, key string, color *xmlThemeColor) {
	if colors == nil || color == nil {
		return
	}
	if color.SRGBClr != nil && color.SRGBClr.Value != "" {
		colors[key] = normalizeHexColor(color.SRGBClr.Value)
		return
	}
	if color.SysClr != nil && color.SysClr.LastClr != "" {
		colors[key] = normalizeHexColor(color.SysClr.LastClr)
	}
}

func addThemeFont(fonts map[string]string, key string, collection *xmlThemeFontCollection, kind string) {
	if fonts == nil || collection == nil {
		return
	}
	var typeface string
	switch kind {
	case "latin":
		if collection.Latin != nil {
			typeface = collection.Latin.Typeface
		}
	case "eastAsia":
		if collection.EastAsia != nil {
			typeface = collection.EastAsia.Typeface
		}
	case "cs":
		if collection.Complex != nil {
			typeface = collection.Complex.Typeface
		}
	}
	if strings.TrimSpace(typeface) != "" {
		fonts[key] = typeface
	}
}

func buildNumberingXML(document *Document) ([]byte, error) {
	if document == nil || len(document.Meta.Numbering) == 0 {
		return nil, nil
	}
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<w:numbering xmlns:w="`)
	builder.WriteString(wordMLNamespace)
	builder.WriteString(`">`)
	for _, numbering := range document.Meta.Numbering {
		abstractID := firstNonEmpty(numbering.AbstractID, numbering.NumID)
		builder.WriteString(`<w:abstractNum w:abstractNumId="`)
		builder.WriteString(escapeXML(abstractID))
		builder.WriteString(`">`)
		for _, level := range numbering.Levels {
			builder.WriteString(`<w:lvl w:ilvl="`)
			builder.WriteString(fmt.Sprintf("%d", level.Level))
			builder.WriteString(`">`)
			if level.Start != 0 {
				builder.WriteString(`<w:start w:val="`)
				builder.WriteString(fmt.Sprintf("%d", level.Start))
				builder.WriteString(`"/>`)
			}
			if level.Format != "" {
				builder.WriteString(`<w:numFmt w:val="`)
				builder.WriteString(escapeXML(level.Format))
				builder.WriteString(`"/>`)
			}
			if level.Text != "" {
				builder.WriteString(`<w:lvlText w:val="`)
				builder.WriteString(escapeXML(level.Text))
				builder.WriteString(`"/>`)
			}
			if hasParagraphStyle(level.Paragraph) {
				builder.WriteString(`<w:pPr>`)
				writeParagraphPropertiesXML(&builder, level.Paragraph, false)
				builder.WriteString(`</w:pPr>`)
			}
			if hasRunStyle(level.TextStyle) {
				builder.WriteString(`<w:rPr>`)
				writeRunPropertiesXML(&builder, level.TextStyle, false)
				builder.WriteString(`</w:rPr>`)
			}
			builder.WriteString(`</w:lvl>`)
		}
		builder.WriteString(`</w:abstractNum>`)
		builder.WriteString(`<w:num w:numId="`)
		builder.WriteString(escapeXML(numbering.NumID))
		builder.WriteString(`"><w:abstractNumId w:val="`)
		builder.WriteString(escapeXML(abstractID))
		builder.WriteString(`"/></w:num>`)
	}
	builder.WriteString(`</w:numbering>`)
	return []byte(builder.String()), nil
}

func writeParagraphPropertiesXML(builder *strings.Builder, style ParagraphStyle, includeContainer bool) {
	if includeContainer {
		builder.WriteString(`<w:pPr>`)
	}
	if style.StyleID != "" {
		builder.WriteString(`<w:pStyle w:val="`)
		builder.WriteString(escapeXML(style.StyleID))
		builder.WriteString(`"/>`)
	}
	if style.Alignment != "" {
		builder.WriteString(`<w:jc w:val="`)
		builder.WriteString(escapeXML(style.Alignment))
		builder.WriteString(`"/>`)
	}
	if style.SpacingBefore != 0 || style.SpacingAfter != 0 || style.LineSpacing != 0 || style.LineSpacingRule != "" {
		builder.WriteString(`<w:spacing`)
		if style.SpacingBefore != 0 {
			builder.WriteString(` w:before="`)
			builder.WriteString(fmt.Sprintf("%d", style.SpacingBefore))
			builder.WriteString(`"`)
		}
		if style.SpacingAfter != 0 {
			builder.WriteString(` w:after="`)
			builder.WriteString(fmt.Sprintf("%d", style.SpacingAfter))
			builder.WriteString(`"`)
		}
		if style.LineSpacing != 0 {
			builder.WriteString(` w:line="`)
			builder.WriteString(fmt.Sprintf("%d", style.LineSpacing))
			builder.WriteString(`"`)
		}
		if style.LineSpacingRule != "" {
			builder.WriteString(` w:lineRule="`)
			builder.WriteString(escapeXML(style.LineSpacingRule))
			builder.WriteString(`"`)
		}
		builder.WriteString(`/>`)
	}
	if style.FirstLineIndent != 0 || style.LeftIndent != 0 {
		builder.WriteString(`<w:ind`)
		if style.FirstLineIndent != 0 {
			builder.WriteString(` w:firstLine="`)
			builder.WriteString(fmt.Sprintf("%d", style.FirstLineIndent))
			builder.WriteString(`"`)
		}
		if style.LeftIndent != 0 {
			builder.WriteString(` w:left="`)
			builder.WriteString(fmt.Sprintf("%d", style.LeftIndent))
			builder.WriteString(`"`)
		}
		builder.WriteString(`/>`)
	}
	if style.KeepNextSet || style.KeepNext {
		writeOnOffElement(builder, "keepNext", style.KeepNext)
	}
	if style.KeepLinesSet || style.KeepLines {
		writeOnOffElement(builder, "keepLines", style.KeepLines)
	}
	if style.PageBreakBeforeSet || style.PageBreakBefore {
		writeOnOffElement(builder, "pageBreakBefore", style.PageBreakBefore)
	}
	if style.WidowControlSet || style.WidowControl {
		writeOnOffElement(builder, "widowControl", style.WidowControl)
	}
	if style.Numbering != nil && style.Numbering.NumID != "" {
		builder.WriteString(`<w:numPr><w:ilvl w:val="`)
		builder.WriteString(fmt.Sprintf("%d", style.Numbering.Level))
		builder.WriteString(`"/><w:numId w:val="`)
		builder.WriteString(escapeXML(style.Numbering.NumID))
		builder.WriteString(`"/></w:numPr>`)
	}
	if includeContainer {
		builder.WriteString(`</w:pPr>`)
	}
}

func normalizeRunBreakType(value string) RunBreakType {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	switch trimmed {
	case "":
		return RunBreakTypeLine
	case "line":
		return RunBreakTypeLine
	case "page":
		return RunBreakTypePage
	case "renderedpage", "rendered_page":
		return RunBreakTypeRenderedPage
	case "textwrapping", "text_wrapping":
		return RunBreakTypeTextWrapping
	default:
		return ""
	}
}

func writeRunBreakXML(builder *strings.Builder, breakType RunBreakType) {
	switch breakType {
	case RunBreakTypePage:
		builder.WriteString(`<w:br w:type="page"/>`)
	case RunBreakTypeRenderedPage:
		builder.WriteString(`<w:lastRenderedPageBreak/>`)
	case RunBreakTypeTextWrapping:
		builder.WriteString(`<w:br w:type="textWrapping"/>`)
	case RunBreakTypeLine:
		builder.WriteString(`<w:br/>`)
	}
}

func writeRunPropertiesXML(builder *strings.Builder, style TextStyle, includeContainer bool) {
	if includeContainer {
		builder.WriteString(`<w:rPr>`)
	}
	if style.StyleID != "" {
		builder.WriteString(`<w:rStyle w:val="`)
		builder.WriteString(escapeXML(style.StyleID))
		builder.WriteString(`"/>`)
	}
	if style.FontFamily != "" {
		builder.WriteString(`<w:rFonts w:ascii="`)
		builder.WriteString(escapeXML(style.FontFamily))
		builder.WriteString(`" w:eastAsia="`)
		builder.WriteString(escapeXML(style.FontFamily))
		if style.ThemeFontASCII != "" {
			builder.WriteString(`" w:asciiTheme="`)
			builder.WriteString(escapeXML(style.ThemeFontASCII))
		}
		if style.ThemeFontEastAsia != "" {
			builder.WriteString(`" w:eastAsiaTheme="`)
			builder.WriteString(escapeXML(style.ThemeFontEastAsia))
		}
		builder.WriteString(`"/>`)
	} else if style.ThemeFontASCII != "" || style.ThemeFontEastAsia != "" {
		builder.WriteString(`<w:rFonts`)
		if style.ThemeFontASCII != "" {
			builder.WriteString(` w:asciiTheme="`)
			builder.WriteString(escapeXML(style.ThemeFontASCII))
			builder.WriteString(`"`)
		}
		if style.ThemeFontEastAsia != "" {
			builder.WriteString(` w:eastAsiaTheme="`)
			builder.WriteString(escapeXML(style.ThemeFontEastAsia))
			builder.WriteString(`"`)
		}
		builder.WriteString(`/>`)
	}
	if style.FontSizeHP != 0 {
		builder.WriteString(`<w:sz w:val="`)
		builder.WriteString(fmt.Sprintf("%d", style.FontSizeHP))
		builder.WriteString(`"/>`)
	}
	if style.BoldSet || style.Bold {
		writeOnOffElement(builder, "b", style.Bold)
	}
	if style.ItalicSet || style.Italic {
		writeOnOffElement(builder, "i", style.Italic)
	}
	if style.UnderlineSet || style.Underline {
		if style.Underline {
			builder.WriteString(`<w:u w:val="single"/>`)
		} else {
			builder.WriteString(`<w:u w:val="none"/>`)
		}
	}
	if style.Color != "" || style.ThemeColor != "" {
		builder.WriteString(`<w:color`)
		if style.Color != "" {
			builder.WriteString(` w:val="`)
			builder.WriteString(escapeXML(style.Color))
			builder.WriteString(`"`)
		}
		if style.ThemeColor != "" {
			builder.WriteString(` w:themeColor="`)
			builder.WriteString(escapeXML(style.ThemeColor))
			builder.WriteString(`"`)
		}
		builder.WriteString(`/>`)
	}
	if includeContainer {
		builder.WriteString(`</w:rPr>`)
	}
}

func writeOnOffElement(builder *strings.Builder, name string, value bool) {
	if value {
		builder.WriteString(`<w:`)
		builder.WriteString(name)
		builder.WriteString(`/>`)
		return
	}
	builder.WriteString(`<w:`)
	builder.WriteString(name)
	builder.WriteString(` w:val="0"/>`)
}

func readOptionalZipEntry(files []*zip.File, name string) ([]byte, error) {
	for _, file := range files {
		if file.Name == name {
			return readZipFile(file)
		}
	}
	return nil, nil
}

func updateDocumentRelationshipsXML(existing []byte, hasStyles, hasNumbering bool, headerFooters []HeaderFooterPart) ([]byte, error) {
	rels := xmlRelationships{Xmlns: "http://schemas.openxmlformats.org/package/2006/relationships"}
	if len(existing) > 0 {
		if err := xml.Unmarshal(existing, &rels); err != nil {
			return nil, fmt.Errorf("parse document relationships: %w", err)
		}
		if rels.Xmlns == "" {
			rels.Xmlns = "http://schemas.openxmlformats.org/package/2006/relationships"
		}
	}
	if hasStyles {
		ensureRelationship(&rels, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles", "styles.xml")
	}
	if hasNumbering {
		ensureRelationship(&rels, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering", "numbering.xml")
	}
	for _, part := range headerFooters {
		relType := "http://schemas.openxmlformats.org/officeDocument/2006/relationships/header"
		if part.Kind == "footer" {
			relType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer"
		}
		ensureRelationshipWithID(&rels, part.RelationshipID, relType, part.Target)
	}
	content, err := xml.Marshal(rels)
	if err != nil {
		return nil, fmt.Errorf("marshal document relationships: %w", err)
	}
	return append([]byte(xml.Header), content...), nil
}

func updateContentTypesXML(existing []byte, hasStyles, hasNumbering bool, headerFooters []HeaderFooterPart) ([]byte, error) {
	types := xmlContentTypes{Xmlns: "http://schemas.openxmlformats.org/package/2006/content-types"}
	if len(existing) > 0 {
		if err := xml.Unmarshal(existing, &types); err != nil {
			return nil, fmt.Errorf("parse content types: %w", err)
		}
		if types.Xmlns == "" {
			types.Xmlns = "http://schemas.openxmlformats.org/package/2006/content-types"
		}
	}
	ensureContentOverride(&types, "/word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")
	ensureContentOverride(&types, "/docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml")
	ensureContentOverride(&types, "/docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml")
	ensureContentDefault(&types, "rels", "application/vnd.openxmlformats-package.relationships+xml")
	ensureContentDefault(&types, "xml", "application/xml")
	if hasStyles {
		ensureContentOverride(&types, "/word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml")
	}
	if hasNumbering {
		ensureContentOverride(&types, "/word/numbering.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml")
	}
	for _, part := range headerFooters {
		contentType := "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"
		if part.Kind == "footer" {
			contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"
		}
		ensureContentOverride(&types, "/word/"+strings.TrimPrefix(part.Target, "/"), contentType)
	}
	content, err := xml.Marshal(types)
	if err != nil {
		return nil, fmt.Errorf("marshal content types: %w", err)
	}
	return append([]byte(xml.Header), content...), nil
}

func ensureRelationship(rels *xmlRelationships, relType, target string) {
	for _, relationship := range rels.Relationships {
		if relationship.Type == relType {
			return
		}
	}
	rels.Relationships = append(rels.Relationships, xmlRelationship{
		ID:     nextRelationshipID(rels.Relationships),
		Type:   relType,
		Target: target,
	})
}

func ensureRelationshipWithID(rels *xmlRelationships, id, relType, target string) {
	trimmedID := strings.TrimSpace(id)
	trimmedTarget := strings.TrimSpace(target)
	for index := range rels.Relationships {
		relationship := &rels.Relationships[index]
		if relationship.ID == trimmedID || (relationship.Type == relType && relationship.Target == trimmedTarget) {
			if trimmedID != "" {
				relationship.ID = trimmedID
			}
			relationship.Type = relType
			relationship.Target = trimmedTarget
			return
		}
	}
	rels.Relationships = append(rels.Relationships, xmlRelationship{
		ID:     firstNonEmpty(trimmedID, nextRelationshipID(rels.Relationships)),
		Type:   relType,
		Target: trimmedTarget,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ensureContentDefault(types *xmlContentTypes, extension, contentType string) {
	for _, item := range types.Defaults {
		if strings.EqualFold(item.Extension, extension) {
			return
		}
	}
	types.Defaults = append(types.Defaults, xmlContentDefault{Extension: extension, ContentType: contentType})
}

func ensureContentOverride(types *xmlContentTypes, partName, contentType string) {
	for _, item := range types.Overrides {
		if item.PartName == partName {
			return
		}
	}
	types.Overrides = append(types.Overrides, xmlContentOverride{PartName: partName, ContentType: contentType})
}

func nextRelationshipID(rels []xmlRelationship) string {
	maxID := 0
	for _, rel := range rels {
		var current int
		if _, err := fmt.Sscanf(rel.ID, "rId%d", &current); err == nil && current > maxID {
			maxID = current
		}
	}
	return fmt.Sprintf("rId%d", maxID+1)
}
