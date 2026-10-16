package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/auramate/auramate/internal/goword"
)

func main() {
	inputPath := filepath.Clean("workspace/goword-real-image.docx")
	outputPath := filepath.Clean("workspace/goword-real-image.edited.docx")
	if len(os.Args) > 1 {
		inputPath = filepath.Clean(os.Args[1])
	}
	if len(os.Args) > 2 {
		outputPath = filepath.Clean(os.Args[2])
	}

	service := goword.NewService(".")
	session, err := service.OpenDocument(inputPath)
	if err != nil {
		fail("open real image docx", err)
	}
	paragraph := firstParagraphWithImage(session.Document)
	if paragraph == nil {
		fail("locate image paragraph", fmt.Errorf("expected paragraph containing image run"))
	}
	imageRuns := countImageRuns(paragraph.Runs)
	if imageRuns != 1 {
		fail("validate image run count", fmt.Errorf("expected 1 image run, got %d", imageRuns))
	}

	result, err := service.Apply(session.ID, []goword.Operation{{
		Type:        goword.OperationReplaceText,
		ParagraphID: paragraph.ID,
		StartOffset: 0,
		EndOffset:   len([]rune("图片前文本")),
		Text:        "已编辑图片前文",
	}})
	if err != nil {
		fail("apply text edit near image", err)
	}
	if len(result.Diffs) != 1 {
		fail("verify diff count", fmt.Errorf("expected 1 diff, got %d", len(result.Diffs)))
	}
	if len(result.Diffs[0].CharacterRanges) == 0 {
		fail("verify character diff", fmt.Errorf("expected character range diffs"))
	}
	if result.Diffs[0].CharacterRanges[0].BeforeText != "图片前文本" || result.Diffs[0].CharacterRanges[0].AfterText != "已编辑图片前文" {
		fail("verify character diff payload", fmt.Errorf("unexpected character diff: %+v", result.Diffs[0].CharacterRanges[0]))
	}
	if len(result.Diffs[0].AfterRuns) < 2 || result.Diffs[0].AfterRuns[1].Image == nil {
		fail("verify run snapshots", fmt.Errorf("expected image run snapshot in diff"))
	}

	saved, err := service.Save(session.ID, outputPath)
	if err != nil {
		fail("save edited image docx", err)
	}

	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		fail("reopen edited image docx", err)
	}
	reopenedParagraph := firstParagraphWithImage(reopened)
	if reopenedParagraph == nil {
		fail("verify image paragraph survives", fmt.Errorf("image paragraph missing after save"))
	}
	if text := reopenedParagraph.PlainText(); text != "已编辑图片前文图片后文本" {
		fail("verify visible text", fmt.Errorf("unexpected paragraph text: %s", text))
	}
	if count := countImageRuns(reopenedParagraph.Runs); count != 1 {
		fail("verify image run count after save", fmt.Errorf("expected 1 image run after save, got %d", count))
	}
	if err := ensureZipEntry(saved.Path, "word/media/image1.png"); err != nil {
		fail("verify media asset", err)
	}
	if err := ensureZipEntryContains(saved.Path, "word/_rels/document.xml.rels", `Target="media/image1.png"`); err != nil {
		fail("verify image relationship", err)
	}

	fmt.Printf("real-image regression passed\ninput=%s\noutput=%s\n", inputPath, saved.Path)
}

func firstParagraphWithImage(document *goword.Document) *goword.Paragraph {
	if document == nil {
		return nil
	}
	for index := range document.Paragraphs {
		if countImageRuns(document.Paragraphs[index].Runs) > 0 {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func countImageRuns(runs []goword.Run) int {
	count := 0
	for _, run := range runs {
		if run.Image != nil || strings.TrimSpace(run.DrawingXML) != "" {
			count++
		}
	}
	return count
}

func ensureZipEntry(path, name string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == name {
			return nil
		}
	}
	return fmt.Errorf("zip entry not found: %s", name)
}

func ensureZipEntryContains(path, name, expected string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		content, err := readZipEntry(file)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), expected) {
			return fmt.Errorf("zip entry %s does not contain %q", name, expected)
		}
		return nil
	}
	return fmt.Errorf("zip entry not found: %s", name)
}

func readZipEntry(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s failed: %v\n", step, err)
	os.Exit(1)
}
