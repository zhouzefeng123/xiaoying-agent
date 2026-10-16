package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/auramate/auramate/internal/goword"
)

func main() {
	inputPath := filepath.Clean("workspace/goword-real-table.docx")
	outputPath := filepath.Clean("workspace/goword-real-table.edited.docx")
	if len(os.Args) > 1 {
		inputPath = filepath.Clean(os.Args[1])
	}
	if len(os.Args) > 2 {
		outputPath = filepath.Clean(os.Args[2])
	}

	service := goword.NewService(".")
	session, err := service.OpenDocument(inputPath)
	if err != nil {
		fail("open real docx", err)
	}
	tableBlock := firstTableBlock(session.Document)
	if tableBlock == nil || tableBlock.Table == nil {
		fail("validate structure", fmt.Errorf("expected at least one top-level table block"))
	}

	cellParagraph := tableBlock.Table.Rows[0].Cells[0].Blocks[0].Paragraph
	if cellParagraph == nil {
		fail("locate first cell paragraph", fmt.Errorf("missing first cell paragraph"))
	}

	_, err = service.Apply(session.ID, []goword.Operation{
		{
			Type:        goword.OperationReplaceText,
			ParagraphID: cellParagraph.ID,
			StartOffset: 0,
			EndOffset:   len([]rune(cellParagraph.PlainText())),
			Text:        "已由 go-word 精确修改",
		},
		{
			Type:     goword.OperationInsertParagraph,
			AnchorID: cellParagraph.ID,
			Position: "after",
			Paragraph: &goword.ParagraphInput{
				Text: "新增说明段落",
			},
		},
	})
	if err != nil {
		fail("apply operations", err)
	}

	saved, err := service.Save(session.ID, outputPath)
	if err != nil {
		fail("save edited docx", err)
	}

	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		fail("reopen edited docx", err)
	}
	reopenedTableBlock := firstTableBlock(reopened)
	if reopenedTableBlock == nil || reopenedTableBlock.Table == nil {
		fail("verify reopened structure", fmt.Errorf("table block lost after save"))
	}
	firstCellBlocks := reopenedTableBlock.Table.Rows[0].Cells[0].Blocks
	if len(firstCellBlocks) != 2 {
		fail("verify inserted paragraph", fmt.Errorf("expected 2 blocks in first cell, got %d", len(firstCellBlocks)))
	}
	if text := firstCellBlocks[0].Paragraph.PlainText(); text != "已由 go-word 精确修改" {
		fail("verify replaced text", fmt.Errorf("unexpected first cell text: %s", text))
	}
	if text := firstCellBlocks[1].Paragraph.PlainText(); text != "新增说明段落" {
		fail("verify inserted text", fmt.Errorf("unexpected inserted text: %s", text))
	}

	fmt.Printf("real-doc regression passed\ninput=%s\noutput=%s\n", inputPath, saved.Path)
}

func firstTableBlock(document *goword.Document) *goword.Block {
	if document == nil {
		return nil
	}
	for index := range document.Blocks {
		if document.Blocks[index].Table != nil {
			return &document.Blocks[index]
		}
	}
	return nil
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s failed: %v\n", step, err)
	os.Exit(1)
}
