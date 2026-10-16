package goword

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyOperationsBuildsDiffs(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_1",
		Name:     "demo.docx",
		Revision: 1,
		Paragraphs: []Paragraph{
			{ID: "p1", Runs: []Run{{ID: "r1", Text: "hello world"}}},
			{ID: "p2", Runs: []Run{{ID: "r2", Text: "second paragraph"}}},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{
		{
			Type:        OperationReplaceText,
			ParagraphID: "p1",
			StartOffset: 6,
			EndOffset:   11,
			Text:        "auramate",
		},
		{
			Type:     OperationInsertParagraph,
			AnchorID: "p2",
			Position: "after",
			Paragraph: &ParagraphInput{
				Text: "inserted paragraph",
			},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := result.Document.Paragraphs[0].PlainText(); got != "hello auramate" {
		t.Fatalf("unexpected paragraph text: %s", got)
	}

	if len(result.Diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(result.Diffs))
	}
}

func TestBuildParagraphDiffsIncludesCharacterRangesAndRunSnapshots(t *testing.T) {
	before := &Document{Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{
		ID: "p1",
		Runs: []Run{
			{ID: "r1", Text: "图片前"},
			{ID: "r2", DrawingXML: `<wp:inline><wp:extent cx="1" cy="1"/></wp:inline>`, Image: &RunImage{RelationshipID: "rIdImage1", Target: "media/image1.png", Filename: "image1.png", Inline: true}},
			{ID: "r3", Text: "图片后"},
		},
	}}}}
	before.rebuildParagraphs()
	after := &Document{Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{
		ID: "p1",
		Runs: []Run{
			{ID: "r4", Text: "已编辑"},
			{ID: "r2", DrawingXML: `<wp:inline><wp:extent cx="1" cy="1"/></wp:inline>`, Image: &RunImage{RelationshipID: "rIdImage1", Target: "media/image1.png", Filename: "image1.png", Inline: true}},
			{ID: "r5", Text: "图片后"},
		},
	}}}}
	after.rebuildParagraphs()

	diffs := BuildParagraphDiffs(before, after)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	diff := diffs[0]
	if diff.Type != DiffUpdated {
		t.Fatalf("expected updated diff, got %s", diff.Type)
	}
	if len(diff.CharacterRanges) != 1 {
		t.Fatalf("expected 1 character range, got %d", len(diff.CharacterRanges))
	}
	if diff.CharacterRanges[0].BeforeText != "图片前" || diff.CharacterRanges[0].AfterText != "已编辑" {
		t.Fatalf("unexpected character range payload: %+v", diff.CharacterRanges[0])
	}
	if len(diff.BeforeRuns) != 3 || len(diff.AfterRuns) != 3 {
		t.Fatalf("expected 3 run snapshots before/after, got %d/%d", len(diff.BeforeRuns), len(diff.AfterRuns))
	}
	if diff.BeforeRuns[1].Kind != RunSnapshotImage || diff.AfterRuns[1].Kind != RunSnapshotImage {
		t.Fatalf("expected middle snapshots to be image runs")
	}
	if diff.AfterRuns[1].Image == nil || diff.AfterRuns[1].Image.Target != "media/image1.png" {
		t.Fatalf("expected image snapshot target media/image1.png, got %+v", diff.AfterRuns[1].Image)
	}
}

func TestApplyResolvedNumberingBuildsDisplayText(t *testing.T) {
	document := &Document{
		Meta: DocumentMeta{Numbering: []NumberingDefinition{{
			NumID: "42",
			Levels: []NumberingLevel{
				{Level: 0, Start: 1, Format: "decimal", Text: "%1."},
				{Level: 1, Start: 1, Format: "decimal", Text: "%1.%2"},
			},
		}}},
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "42", Level: 0}}, Runs: []Run{{ID: "r1", Text: "一级"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "42", Level: 1}}, Runs: []Run{{ID: "r2", Text: "二级一"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p3", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "42", Level: 1}}, Runs: []Run{{ID: "r3", Text: "二级二"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p4", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "42", Level: 0}}, Runs: []Run{{ID: "r4", Text: "一级二"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p5", Runs: []Run{{ID: "r5", Text: "正文"}}}},
		},
	}

	applyResolvedNumbering(document)

	if got := document.Paragraphs[0].NumberingText; got != "1." {
		t.Fatalf("expected first numbering text 1., got %q", got)
	}
	if got := document.Paragraphs[1].NumberingText; got != "1.1" {
		t.Fatalf("expected second numbering text 1.1, got %q", got)
	}
	if got := document.Paragraphs[2].NumberingText; got != "1.2" {
		t.Fatalf("expected third numbering text 1.2, got %q", got)
	}
	if got := document.Paragraphs[3].NumberingText; got != "2." {
		t.Fatalf("expected fourth numbering text 2., got %q", got)
	}
	if got := document.Paragraphs[4].NumberingText; got != "" {
		t.Fatalf("expected body paragraph to have empty numbering text, got %q", got)
	}
}

func TestApplyResolvedNumberingSupportsChineseCounting(t *testing.T) {
	document := &Document{
		Meta: DocumentMeta{Numbering: []NumberingDefinition{{
			NumID:  "cn",
			Levels: []NumberingLevel{{Level: 0, Start: 1, Format: "chineseCounting", Text: "%1、"}},
		}}},
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "cn", Level: 0}}, Runs: []Run{{ID: "r1", Text: "现状分析"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "cn", Level: 0}}, Runs: []Run{{ID: "r2", Text: "实施路径"}}}},
		},
	}

	applyResolvedNumbering(document)

	if got := document.Paragraphs[0].NumberingText; got != "一、" {
		t.Fatalf("expected first chinese numbering text 一、, got %q", got)
	}
	if got := document.Paragraphs[1].NumberingText; got != "二、" {
		t.Fatalf("expected second chinese numbering text 二、, got %q", got)
	}
}

func TestApplyUpdateTextStyleBuildsStyleDiffs(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_style_patch",
		Name:     "style-patch.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID: "p1",
			Runs: []Run{{
				ID:   "r1",
				Text: "hello world",
				Style: TextStyle{
					FontFamily: "Calibri",
					FontSizeHP: 22,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationUpdateTextStyle,
		ParagraphID: "p1",
		StartOffset: 6,
		EndOffset:   11,
		StylePatch: TextStyle{
			FontFamily: "Microsoft YaHei",
			FontSizeHP: 28,
			Bold:       true,
			BoldSet:    true,
		},
	}})
	if err != nil {
		t.Fatalf("apply style patch: %v", err)
	}

	paragraph := result.Document.Paragraphs[0]
	if got := paragraph.PlainText(); got != "hello world" {
		t.Fatalf("unexpected paragraph text: %q", got)
	}
	if len(paragraph.Runs) != 2 {
		t.Fatalf("expected 2 runs after style split, got %d", len(paragraph.Runs))
	}
	styledRun := paragraph.Runs[1]
	if styledRun.Text != "world" {
		t.Fatalf("expected styled run text world, got %q", styledRun.Text)
	}
	if styledRun.Style.FontFamily != "Microsoft YaHei" || styledRun.Style.FontSizeHP != 28 || !styledRun.Style.Bold || !styledRun.Style.BoldSet {
		t.Fatalf("unexpected styled run style: %+v", styledRun.Style)
	}
	if len(result.Diffs) != 1 || result.Diffs[0].Type != DiffUpdated {
		t.Fatalf("expected one updated diff, got %+v", result.Diffs)
	}
	if len(result.Diffs[0].CharacterRanges) != 1 {
		t.Fatalf("expected style diff range, got %+v", result.Diffs[0].CharacterRanges)
	}
	if result.Diffs[0].CharacterRanges[0].AfterStart != 6 || result.Diffs[0].CharacterRanges[0].AfterEnd != 11 {
		t.Fatalf("unexpected style diff range: %+v", result.Diffs[0].CharacterRanges[0])
	}
	if len(result.Diffs[0].AfterRuns) != 2 || !result.Diffs[0].AfterRuns[1].Style.Bold {
		t.Fatalf("expected diff snapshots to preserve styled run, got %+v", result.Diffs[0].AfterRuns)
	}
}

func TestApplyUpdateParagraphStyleBuildsDiffs(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_paragraph_style_patch",
		Name:     "paragraph-style-patch.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID: "p1",
			Style: ParagraphStyle{
				StyleID:   "Normal",
				Alignment: "left",
			},
			Runs: []Run{{ID: "r1", Text: "章节标题"}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationUpdateParagraphStyle,
		ParagraphID: "p1",
		ParagraphStylePatch: ParagraphStyle{
			StyleID:     "Heading1",
			Alignment:   "center",
			KeepNext:    true,
			KeepNextSet: true,
		},
	}})
	if err != nil {
		t.Fatalf("apply paragraph style patch: %v", err)
	}

	paragraph := result.Document.Paragraphs[0]
	if paragraph.Style.StyleID != "Heading1" || paragraph.Style.Alignment != "center" || !paragraph.Style.KeepNext || !paragraph.Style.KeepNextSet {
		t.Fatalf("unexpected paragraph style: %+v", paragraph.Style)
	}
	if len(result.Diffs) != 1 || result.Diffs[0].Type != DiffUpdated {
		t.Fatalf("expected one updated diff, got %+v", result.Diffs)
	}
	if result.Diffs[0].BeforeText != "章节标题" || result.Diffs[0].AfterText != "章节标题" {
		t.Fatalf("expected text to remain unchanged, got %+v", result.Diffs[0])
	}
}

func TestApplyInsertLineBreakPreservesWordBreakRun(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_insert_line_break",
		Name:     "insert-line-break.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "第一行第二行"}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationInsertLineBreak,
		ParagraphID: "p1",
		StartOffset: 3,
	}})
	if err != nil {
		t.Fatalf("apply insert line break: %v", err)
	}

	paragraph := result.Document.Paragraphs[0]
	if got := paragraph.PlainText(); got != "第一行\n第二行" {
		t.Fatalf("unexpected paragraph text: %q", got)
	}
	if len(paragraph.Runs) != 3 {
		t.Fatalf("expected 3 runs after line break insert, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[1].BreakType != RunBreakTypeLine {
		t.Fatalf("expected middle run to be line break, got %+v", paragraph.Runs[1])
	}
	if len(result.Diffs) != 1 || len(result.Diffs[0].CharacterRanges) != 1 || result.Diffs[0].CharacterRanges[0].AfterText != "\n" {
		t.Fatalf("expected inserted newline diff, got %+v", result.Diffs)
	}
}

func TestApplyInsertParagraphBreakSplitsParagraph(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_insert_paragraph_break",
		Name:     "insert-paragraph-break.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "第一段第二段"}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationInsertParagraphBreak,
		ParagraphID: "p1",
		StartOffset: 3,
	}})
	if err != nil {
		t.Fatalf("apply insert paragraph break: %v", err)
	}

	if len(result.Document.Paragraphs) != 2 {
		t.Fatalf("expected 2 paragraphs after split, got %d", len(result.Document.Paragraphs))
	}
	if got := result.Document.Paragraphs[0].PlainText(); got != "第一段" {
		t.Fatalf("unexpected first paragraph: %q", got)
	}
	if got := result.Document.Paragraphs[1].PlainText(); got != "第二段" {
		t.Fatalf("unexpected second paragraph: %q", got)
	}
	if len(result.Diffs) < 2 {
		t.Fatalf("expected updated + inserted diffs after paragraph break, got %+v", result.Diffs)
	}
}

func TestApplyReplaceTextTurnsNewlinesIntoWordBreakRunsAndUsesRangeStyle(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_replace_line_breaks",
		Name:     "replace-line-breaks.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID: "p1",
			Runs: []Run{
				{ID: "r1", Text: "甲", Style: TextStyle{FontFamily: "黑体", FontSizeHP: 32}},
				{ID: "r2", Text: "乙", Style: TextStyle{FontFamily: "仿宋", FontSizeHP: 24}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 1,
		EndOffset:   2,
		BeforeText:  "乙",
		Text:        "第一行\n第二行",
	}})
	if err != nil {
		t.Fatalf("apply replace text: %v", err)
	}

	paragraph := result.Document.Paragraphs[0]
	if got := paragraph.PlainText(); got != "甲第一行\n第二行" {
		t.Fatalf("unexpected paragraph text: %q", got)
	}
	if len(paragraph.Runs) != 4 {
		t.Fatalf("expected 4 runs after multiline replace, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[1].Style.FontFamily != "仿宋" || paragraph.Runs[1].Style.FontSizeHP != 24 {
		t.Fatalf("expected replacement to inherit replaced range style, got %+v", paragraph.Runs[1].Style)
	}
	if paragraph.Runs[2].BreakType != RunBreakTypeLine {
		t.Fatalf("expected multiline replacement to emit Word line break, got %+v", paragraph.Runs[2])
	}
	if paragraph.Runs[3].Style.FontFamily != "仿宋" || paragraph.Runs[3].Style.FontSizeHP != 24 {
		t.Fatalf("expected text after break to keep replacement style, got %+v", paragraph.Runs[3].Style)
	}
}

func TestApplyScrollToDoesNotAdvanceRevision(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_scroll_only",
		Name:     "scroll-only.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "仅滚动定位"}}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:        OperationScrollTo,
		ParagraphID: "p1",
	}})
	if err != nil {
		t.Fatalf("apply scroll_to: %v", err)
	}
	if result.Document.Revision != 1 {
		t.Fatalf("expected scroll_to to keep revision 1, got %d", result.Document.Revision)
	}
	if len(result.Diffs) != 0 {
		t.Fatalf("expected scroll_to to keep diffs empty, got %+v", result.Diffs)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after scroll_to: %v", err)
	}
	if snapshot.Document.Revision != 1 {
		t.Fatalf("expected session revision to remain 1, got %d", snapshot.Document.Revision)
	}
}

func TestSaveSnapshotCreatesUniqueFilesWithinSameSecond(t *testing.T) {
	service := NewService(t.TempDir())
	session := &Session{
		ID: "session_snapshot_unique",
		Document: &Document{
			ID:       "doc_snapshot_unique",
			Name:     "snapshot-unique.docx",
			Revision: 1,
			Blocks: []Block{{
				Type:      BlockTypeParagraph,
				Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "快照内容"}}},
			}},
		},
	}

	if err := service.SaveSnapshot(session); err != nil {
		t.Fatalf("save first snapshot: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := service.SaveSnapshot(session); err != nil {
		t.Fatalf("save second snapshot: %v", err)
	}

	entries, err := os.ReadDir(service.snapshotDir)
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), session.ID+"_") && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 unique snapshot files for same session, got %d", count)
	}
}

func TestSaveAfterRestorePersistsEditedDocument(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	sourcePath := filepath.Join(tempDir, "source.docx")
	targetPath := filepath.Join(tempDir, "restored-save.docx")
	document := &Document{
		ID:         "doc_restore_save",
		Name:       "source.docx",
		SourcePath: sourcePath,
		Revision:   1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	}
	if err := SaveDOCX(document.Clone(), sourcePath); err != nil {
		t.Fatalf("write source docx: %v", err)
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 0,
		EndOffset:   len([]rune("原始内容")),
		Text:        "编辑后内容",
	}}); err != nil {
		t.Fatalf("apply edit: %v", err)
	}

	service.Close(session.ID)
	saved, err := service.Save(session.ID, targetPath)
	if err != nil {
		t.Fatalf("save after restore: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen saved docx: %v", err)
	}
	if got := reopened.Paragraphs[0].PlainText(); got != "编辑后内容" {
		t.Fatalf("expected restored save to keep edited text, got %q", got)
	}
}

func TestSnapshotRestoreSupportsLegacyDocumentPayload(t *testing.T) {
	service := NewService(t.TempDir())
	legacyDocument := &Document{
		ID:         "doc_legacy_snapshot",
		Name:       "legacy.docx",
		SourcePath: filepath.Join(t.TempDir(), "legacy.docx"),
		Revision:   3,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "旧快照内容"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	}
	raw, err := json.Marshal(legacyDocument)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	snapshotPath := filepath.Join(service.snapshotDir, "legacy_session_20260510_120000_000000000.json")
	if err := os.WriteFile(snapshotPath, raw, 0o644); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}

	snapshot, err := service.Snapshot("legacy_session")
	if err != nil {
		t.Fatalf("restore legacy snapshot: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "旧快照内容" {
		t.Fatalf("expected legacy snapshot document text, got %q", got)
	}
	if snapshot.Previous == nil || snapshot.Previous.Paragraphs[0].PlainText() != "旧快照内容" {
		t.Fatalf("expected legacy snapshot previous to mirror document, got %+v", snapshot.Previous)
	}
}

func TestCreateSessionReturnsErrorWhenInitialSnapshotPersistFails(t *testing.T) {
	service := NewService(t.TempDir())
	service.snapshotDir = filepath.Join(t.TempDir(), "missing", "snapshots")

	_, err := service.CreateSession(&Document{
		ID:       "doc_snapshot_fail_create",
		Name:     "snapshot-fail-create.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "初始内容"}}},
		}},
	})
	if err == nil {
		t.Fatalf("expected create session to fail when snapshot persist fails")
	}
	if !strings.Contains(err.Error(), "persist initial session snapshot") {
		t.Fatalf("unexpected create session error: %v", err)
	}
	if len(service.sessions) != 0 {
		t.Fatalf("expected no live sessions after create failure, got %d", len(service.sessions))
	}
}

func TestApplyReturnsErrorAndRevertsSessionWhenSnapshotPersistFails(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_snapshot_fail_apply",
		Name:     "snapshot-fail-apply.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 0,
		EndOffset:   4,
		Text:        "中间版本",
	}})
	if err != nil {
		t.Fatalf("apply baseline version: %v", err)
	}

	service.snapshotDir = filepath.Join(t.TempDir(), "missing", "snapshots")
	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 0,
		EndOffset:   len([]rune("中间版本")),
		Text:        "变更内容",
	}})
	if err == nil {
		t.Fatalf("expected apply to fail when snapshot persist fails")
	}
	if !strings.Contains(err.Error(), "persist session snapshot after apply") {
		t.Fatalf("unexpected apply error: %v", err)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after failed apply: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "中间版本" {
		t.Fatalf("expected document text to keep latest committed content after failed apply, got %q", got)
	}
	if snapshot.Document.Revision != 2 {
		t.Fatalf("expected revision to remain 2 after failed apply, got %d", snapshot.Document.Revision)
	}
	if snapshot.Previous == nil || snapshot.Previous.Paragraphs[0].PlainText() != "原始内容" {
		t.Fatalf("expected previous snapshot to remain original content, got %+v", snapshot.Previous)
	}
}

func TestRollbackToPreviousSwapsCurrentAndPrevious(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_rollback_swap",
		Name:     "rollback-swap.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 0,
		EndOffset:   4,
		Text:        "第一版",
	}})
	if err != nil {
		t.Fatalf("apply first version: %v", err)
	}
	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: "p1",
		StartOffset: 0,
		EndOffset:   3,
		Text:        "第二版",
	}})
	if err != nil {
		t.Fatalf("apply second version: %v", err)
	}

	rolledBack, err := service.RollbackToPrevious(session.ID)
	if err != nil {
		t.Fatalf("rollback to previous: %v", err)
	}
	if got := rolledBack.Paragraphs[0].PlainText(); got != "第一版" {
		t.Fatalf("expected rollback result to be first version, got %q", got)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after rollback: %v", err)
	}
	if snapshot.Previous == nil || snapshot.Previous.Paragraphs[0].PlainText() != "第二版" {
		t.Fatalf("expected previous snapshot to hold rolled-back-from content, got %+v", snapshot.Previous)
	}

	rolledForward, err := service.RollbackToPrevious(session.ID)
	if err != nil {
		t.Fatalf("second rollback toggle: %v", err)
	}
	if got := rolledForward.Paragraphs[0].PlainText(); got != "第二版" {
		t.Fatalf("expected second rollback to toggle back to second version, got %q", got)
	}
}

func TestApplyUpdateHeaderFooterAssignsRelationshipMetadataInMemory(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_header_footer_memory",
		Name:     "header-footer-memory.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "正文"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type: OperationUpdateHeaderFooter,
		HeaderFooter: &HeaderFooterInput{
			Kind:    "header",
			Variant: "default",
			Blocks:  []BlockInput{{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "内存页眉"}}},
		},
	}})
	if err != nil {
		t.Fatalf("apply update header footer: %v", err)
	}
	if len(result.Document.Meta.HeaderFooters) != 1 {
		t.Fatalf("expected one header footer part, got %+v", result.Document.Meta.HeaderFooters)
	}
	part := result.Document.Meta.HeaderFooters[0]
	if part.RelationshipID != "rIdHeaderDefault" {
		t.Fatalf("expected in-memory relationship id rIdHeaderDefault, got %q", part.RelationshipID)
	}
	if part.Target != "header-default.xml" {
		t.Fatalf("expected in-memory target header-default.xml, got %q", part.Target)
	}
}

func TestApplyDeleteTableColumnRejectsMergedCellStructure(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_merged_column_delete",
		Name:     "table-merged-column-delete.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID:      "tbl_main",
				GridXML: `<w:gridCol w:w="2000"/><w:gridCol w:w="2000"/><w:gridCol w:w="2000"/>`,
				Rows: []TableRow{
					{
						ID: "row_1",
						Cells: []TableCell{
							{ID: "cell_1_1", PropertiesXML: `<w:gridSpan w:val="2"/>`, Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_1", Runs: []Run{{ID: "r_1_1", Text: "A1-A2"}}}}}},
							{ID: "cell_1_3", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_3", Runs: []Run{{ID: "r_1_3", Text: "A3"}}}}}},
						},
					},
					{
						ID: "row_2",
						Cells: []TableCell{
							{ID: "cell_2_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_2_1", Runs: []Run{{ID: "r_2_1", Text: "B1"}}}}}},
							{ID: "cell_2_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_2_2", Runs: []Run{{ID: "r_2_2", Text: "B2"}}}}}},
							{ID: "cell_2_3", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_2_3", Runs: []Run{{ID: "r_2_3", Text: "B3"}}}}}},
						},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:      OperationDeleteTableColumn,
		TableID:   "tbl_main",
		CellIndex: 1,
	}})
	if err == nil {
		t.Fatalf("expected delete table column to reject merged cell structure")
	}
	if !strings.Contains(err.Error(), "column edits are not supported") {
		t.Fatalf("unexpected merged-cell delete column error: %v", err)
	}
}

func TestSaveReturnsExplicitErrorWhenSnapshotPersistFailsAfterDocumentWrite(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_snapshot_fail_save",
		Name:     "snapshot-fail-save.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "保存内容"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "saved.docx")
	service.snapshotDir = filepath.Join(t.TempDir(), "missing", "snapshots")
	_, err = service.Save(session.ID, outputPath)
	if err == nil {
		t.Fatalf("expected save to fail when snapshot persist fails")
	}
	if !strings.Contains(err.Error(), "document saved to ") || !strings.Contains(err.Error(), "persist session snapshot failed") {
		t.Fatalf("unexpected save error: %v", err)
	}
	if _, statErr := os.Stat(outputPath); statErr != nil {
		t.Fatalf("expected output file to exist after save write succeeds: %v", statErr)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after save with snapshot failure: %v", err)
	}
	if snapshot.Document.SourcePath != outputPath {
		t.Fatalf("expected in-memory session to keep saved source path, got %q", snapshot.Document.SourcePath)
	}
}

func TestApplyInsertTableCreatesTableBlocks(t *testing.T) {
	service := NewService(".")
	session, err := service.CreateSession(&Document{
		ID:       "doc_insert_table",
		Name:     "insert-table.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "表格锚点"}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:     OperationInsertTable,
		AnchorID: "p1",
		Position: "after",
		Table: &TableInput{Rows: []TableRowInput{
			{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "项目"}}}, {Paragraphs: []ParagraphInput{{Text: "金额"}}}}},
			{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "服务器"}}}, {Paragraphs: []ParagraphInput{{Text: "1000"}}}}},
		}},
	}})
	if err != nil {
		t.Fatalf("apply insert table: %v", err)
	}

	if len(result.Document.Blocks) != 2 {
		t.Fatalf("expected 2 top-level blocks, got %d", len(result.Document.Blocks))
	}
	if result.Document.Blocks[1].Type != BlockTypeTable || result.Document.Blocks[1].Table == nil {
		t.Fatalf("expected second block to be table, got %+v", result.Document.Blocks[1])
	}
	table := result.Document.Blocks[1].Table
	if len(table.Rows) != 2 || len(table.Rows[0].Cells) != 2 {
		t.Fatalf("unexpected table shape: %+v", table)
	}
	if got := table.Rows[1].Cells[1].Blocks[0].Paragraph.PlainText(); got != "1000" {
		t.Fatalf("unexpected cell text: %q", got)
	}
	if len(result.Diffs) < 4 {
		t.Fatalf("expected inserted cell paragraphs to produce diffs, got %+v", result.Diffs)
	}
}

func TestApplyTableStructureOperations(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_ops",
		Name:     "table-ops.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{{
						ID:     "cell_1_1",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_1", Runs: []Run{{ID: "r_1_1", Text: "A1"}}}}},
					}, {
						ID:     "cell_1_2",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_2", Runs: []Run{{ID: "r_1_2", Text: "A2"}}}}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:     OperationInsertTableRow,
		TableID:  "tbl_main",
		RowIndex: 0,
		Position: "after",
		TableRow: &TableRowInput{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "B1"}}}, {Paragraphs: []ParagraphInput{{Text: "B2"}}}}},
	}, {
		Type:      OperationInsertTableColumn,
		TableID:   "tbl_main",
		CellIndex: 1,
		Position:  "after",
		TableColumn: &TableColumnInput{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "A3"}}},
			{Paragraphs: []ParagraphInput{{Text: "B3"}}},
		}},
	}, {
		Type:      OperationUpdateTableCell,
		TableID:   "tbl_main",
		RowIndex:  1,
		CellIndex: 0,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "B1-已更新"}}},
	}})
	if err != nil {
		t.Fatalf("apply table operations: %v", err)
	}

	table := result.Document.Blocks[0].Table
	if len(table.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(table.Rows))
	}
	if len(table.Rows[0].Cells) != 3 || len(table.Rows[1].Cells) != 3 {
		t.Fatalf("expected 3 columns after insert, got %+v", table.Rows)
	}
	if got := table.Rows[0].Cells[2].Blocks[0].Paragraph.PlainText(); got != "A3" {
		t.Fatalf("unexpected inserted column cell text: %q", got)
	}
	if got := table.Rows[1].Cells[0].Blocks[0].Paragraph.PlainText(); got != "B1-已更新" {
		t.Fatalf("unexpected updated cell text: %q", got)
	}

	result, err = service.Apply(session.ID, []Operation{{
		Type:      OperationDeleteTableColumn,
		TableID:   "tbl_main",
		CellIndex: 1,
	}, {
		Type:     OperationDeleteTableRow,
		TableID:  "tbl_main",
		RowIndex: 0,
	}})
	if err != nil {
		t.Fatalf("apply table delete operations: %v", err)
	}

	table = result.Document.Blocks[0].Table
	if len(table.Rows) != 1 {
		t.Fatalf("expected 1 row after delete, got %d", len(table.Rows))
	}
	if len(table.Rows[0].Cells) != 2 {
		t.Fatalf("expected 2 columns after delete, got %d", len(table.Rows[0].Cells))
	}
	if got := table.Rows[0].Cells[1].Blocks[0].Paragraph.PlainText(); got != "B3" {
		t.Fatalf("unexpected remaining cell text: %q", got)
	}
	if len(result.Diffs) == 0 {
		t.Fatalf("expected diffs for table structure changes")
	}
}

func TestApplyTableStructureOperationsAcceptAppendPositionIndices(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_ops_append",
		Name:     "table-ops-append.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_append",
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{{
						ID:     "cell_1_1",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_1", Runs: []Run{{ID: "r_1_1", Text: "A1"}}}}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:     OperationInsertTableRow,
		TableID:  "tbl_append",
		RowIndex: 1,
		Position: "after",
		TableRow: &TableRowInput{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "B1"}}}}},
	}, {
		Type:      OperationInsertTableColumn,
		TableID:   "tbl_append",
		CellIndex: 1,
		Position:  "after",
		TableColumn: &TableColumnInput{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "A2"}}},
			{Paragraphs: []ParagraphInput{{Text: "B2"}}},
		}},
	}})
	if err != nil {
		t.Fatalf("apply append-position table operations: %v", err)
	}

	table := result.Document.Blocks[0].Table
	if len(table.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(table.Rows))
	}
	if len(table.Rows[0].Cells) != 2 || len(table.Rows[1].Cells) != 2 {
		t.Fatalf("expected 2 columns after append-position insert, got %+v", table.Rows)
	}
	if got := table.Rows[1].Cells[0].Blocks[0].Paragraph.PlainText(); got != "B1" {
		t.Fatalf("unexpected appended row text: %q", got)
	}
	if got := table.Rows[0].Cells[1].Blocks[0].Paragraph.PlainText(); got != "A2" {
		t.Fatalf("unexpected appended column text: %q", got)
	}
	if got := table.Rows[1].Cells[1].Blocks[0].Paragraph.PlainText(); got != "B2" {
		t.Fatalf("unexpected appended column bottom text: %q", got)
	}
}

func TestApplyInsertTableRowInheritsTemplateCellStyles(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_row_style_inherit",
		Name:     "table-row-style-inherit.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{
					{
						ID:              "row_header",
						RepeatHeader:    true,
						RepeatHeaderSet: true,
						Cells: []TableCell{
							{ID: "cell_h_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_h_1", Style: ParagraphStyle{StyleID: "TableHeader"}, Runs: []Run{{ID: "r_h_1", Text: "维度", Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true}}}}}}},
							{ID: "cell_h_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_h_2", Style: ParagraphStyle{StyleID: "TableHeader"}, Runs: []Run{{ID: "r_h_2", Text: "权重", Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true}}}}}}},
						},
					},
					{
						ID: "row_body",
						Cells: []TableCell{
							{ID: "cell_b_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_b_1", Style: ParagraphStyle{StyleID: "TableBody", SpacingBefore: 120, SpacingAfter: 80}, Runs: []Run{{ID: "r_b_1", Text: "工作业绩", Style: TextStyle{FontFamily: "SimSun", FontSizeHP: 24, Color: "1F1F1F"}}}}}}},
							{ID: "cell_b_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_b_2", Style: ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, Runs: []Run{{ID: "r_b_2", Text: "50%", Style: TextStyle{FontFamily: "Calibri", FontSizeHP: 22, Color: "333333"}}}}}}},
						},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:     OperationInsertTableRow,
		TableID:  "tbl_main",
		RowIndex: 0,
		Position: "after",
		TableRow: &TableRowInput{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "团队贡献"}}},
			{Paragraphs: []ParagraphInput{{Text: "10%"}}},
		}},
	}})
	if err != nil {
		t.Fatalf("apply insert table row: %v", err)
	}

	table := result.Document.Blocks[0].Table
	if len(table.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(table.Rows))
	}
	inserted := table.Rows[1]
	cell0 := inserted.Cells[0].Blocks[0].Paragraph
	cell1 := inserted.Cells[1].Blocks[0].Paragraph
	if cell0 == nil || cell1 == nil {
		t.Fatalf("expected inserted row paragraphs, got %+v", inserted.Cells)
	}
	if cell0.Style.StyleID != "TableBody" || cell0.Style.SpacingBefore != 120 || cell0.Style.SpacingAfter != 80 {
		t.Fatalf("expected first inserted cell paragraph style to inherit body template, got %+v", cell0.Style)
	}
	if cell0.Runs[0].Style.FontFamily != "SimSun" || cell0.Runs[0].Style.FontSizeHP != 24 || cell0.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected first inserted cell run style to inherit body template, got %+v", cell0.Runs[0].Style)
	}
	if cell1.Style.StyleID != "TableBody" || cell1.Style.Alignment != "center" {
		t.Fatalf("expected second inserted cell paragraph style to inherit body template, got %+v", cell1.Style)
	}
	if cell1.Runs[0].Style.FontFamily != "Calibri" || cell1.Runs[0].Style.FontSizeHP != 22 || cell1.Runs[0].Style.Color != "333333" {
		t.Fatalf("expected second inserted cell run style to inherit body template, got %+v", cell1.Runs[0].Style)
	}
}

func TestApplyInsertTableColumnInheritsTemplateCellStyles(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_column_style_inherit",
		Name:     "table-column-style-inherit.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{
					{
						ID: "row_header",
						Cells: []TableCell{
							{ID: "cell_h_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_h_1", Style: ParagraphStyle{StyleID: "TableHeader", Alignment: "center"}, Runs: []Run{{ID: "r_h_1", Text: "考核维度", Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true}}}}}}},
						},
					},
					{
						ID: "row_body",
						Cells: []TableCell{
							{ID: "cell_b_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_b_1", Style: ParagraphStyle{StyleID: "TableBody", FirstLineIndent: 210}, Runs: []Run{{ID: "r_b_1", Text: "工作业绩", Style: TextStyle{FontFamily: "SimSun", FontSizeHP: 24, Color: "1F1F1F"}}}}}}},
						},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:      OperationInsertTableColumn,
		TableID:   "tbl_main",
		CellIndex: 0,
		Position:  "after",
		TableColumn: &TableColumnInput{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "新增表头"}}},
			{Paragraphs: []ParagraphInput{{Text: "新增内容"}}},
		}},
	}})
	if err != nil {
		t.Fatalf("apply insert table column: %v", err)
	}

	table := result.Document.Blocks[0].Table
	if len(table.Rows[0].Cells) != 2 || len(table.Rows[1].Cells) != 2 {
		t.Fatalf("expected 2 columns after insert, got %+v", table.Rows)
	}
	headerInserted := table.Rows[0].Cells[1].Blocks[0].Paragraph
	bodyInserted := table.Rows[1].Cells[1].Blocks[0].Paragraph
	if headerInserted == nil || bodyInserted == nil {
		t.Fatalf("expected inserted column paragraphs, got %+v", table.Rows)
	}
	if headerInserted.Style.StyleID != "TableHeader" || headerInserted.Style.Alignment != "center" {
		t.Fatalf("expected inserted header cell paragraph style to inherit, got %+v", headerInserted.Style)
	}
	if headerInserted.Runs[0].Style.FontFamily != "Microsoft YaHei" || headerInserted.Runs[0].Style.FontSizeHP != 28 || !headerInserted.Runs[0].Style.Bold || !headerInserted.Runs[0].Style.BoldSet {
		t.Fatalf("expected inserted header cell run style to inherit, got %+v", headerInserted.Runs[0].Style)
	}
	if bodyInserted.Style.StyleID != "TableBody" || bodyInserted.Style.FirstLineIndent != 210 {
		t.Fatalf("expected inserted body cell paragraph style to inherit, got %+v", bodyInserted.Style)
	}
	if bodyInserted.Runs[0].Style.FontFamily != "SimSun" || bodyInserted.Runs[0].Style.FontSizeHP != 24 || bodyInserted.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected inserted body cell run style to inherit, got %+v", bodyInserted.Runs[0].Style)
	}
}

func TestApplyUpdateTableCellReturnsTableNotFoundError(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_missing",
		Name:     "table-missing.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{{
						ID:     "cell_1_1",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_1", Runs: []Run{{ID: "r_1_1", Text: "A1"}}}}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:      OperationUpdateTableCell,
		TableID:   "tbl_missing",
		RowIndex:  0,
		CellIndex: 0,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "新值"}}},
	}})
	if err == nil {
		t.Fatalf("expected table not found error")
	}
	if got := err.Error(); got != "未找到目标表格: tbl_missing" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestApplyUpdateTableCellResolvesPlaceholderTableIDWhenDocumentHasSingleTable(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_alias_single",
		Name:     "table-alias-single.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_real_metrics",
				Rows: []TableRow{
					{
						ID: "row_0",
						Cells: []TableCell{{
							ID:     "cell_0_0",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_0_0", Runs: []Run{{ID: "r_0_0", Text: "指标"}}}}},
						}, {
							ID:     "cell_0_1",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_0_1", Runs: []Run{{ID: "r_0_1", Text: "当前值"}}}}},
						}},
					},
					{
						ID: "row_1",
						Cells: []TableCell{{
							ID:     "cell_1_0",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_0", Runs: []Run{{ID: "r_1_0", Text: "通过率"}}}}},
						}, {
							ID:     "cell_1_1",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_1_1", Runs: []Run{{ID: "r_1_1", Text: "85%"}}}}},
						}},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.Apply(session.ID, []Operation{{
		Type:      OperationUpdateTableCell,
		TableID:   "t1",
		RowIndex:  1,
		CellIndex: 1,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "92%"}}},
	}})
	if err != nil {
		t.Fatalf("expected placeholder table id to resolve, got %v", err)
	}
	if got := result.Document.Blocks[0].Table.Rows[1].Cells[1].Blocks[0].Paragraph.Runs[0].Text; got != "92%" {
		t.Fatalf("expected cell updated via placeholder table id, got %q", got)
	}
}

func TestApplyUpdateTableCellPreservesExistingCellFormatting(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	document := &Document{
		ID:       "doc_table_cell_style_preserve",
		Name:     "table-cell-style-preserve.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID:            "tbl_main",
				PropertiesXML: `<w:tblStyle w:val="TableGrid"/><w:tblW w:type="auto" w:w="0"/>`,
				GridXML:       `<w:gridCol w:w="2400"/>`,
				Rows: []TableRow{{
					ID:            "row_1",
					PropertiesXML: `<w:trHeight w:val="320"/>`,
					Cells: []TableCell{{
						ID:            "cell_1_1",
						PropertiesXML: `<w:tcW w:w="2400" w:type="dxa"/><w:shd w:fill="D9EAF7"/>`,
						Blocks: []Block{{
							Type: BlockTypeParagraph,
							Paragraph: &Paragraph{
								ID: "p_1_1",
								Style: ParagraphStyle{
									Alignment:       "center",
									SpacingBefore:   120,
									SpacingAfter:    80,
									FirstLineIndent: 210,
								},
								Runs: []Run{{
									ID:   "r_1_1",
									Text: "原始值",
									Style: TextStyle{
										FontFamily: "Microsoft YaHei",
										FontSizeHP: 24,
										Bold:       true,
										BoldSet:    true,
										Color:      "1F1F1F",
									},
								}},
							},
						}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:      OperationUpdateTableCell,
		TableID:   "tbl_main",
		RowIndex:  0,
		CellIndex: 0,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "更新后内容"}}},
	}})
	if err != nil {
		t.Fatalf("apply update table cell: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "table-cell-style-preserve.docx"))
	if err != nil {
		t.Fatalf("save docx: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen docx: %v", err)
	}

	cell := reopened.Blocks[0].Table.Rows[0].Cells[0]
	if got := cell.PropertiesXML; got != `<w:tcW w:w="2400" w:type="dxa"/><w:shd w:fill="D9EAF7"/>` {
		t.Fatalf("expected cell properties to survive, got %q", got)
	}
	paragraph := cell.Blocks[0].Paragraph
	if paragraph == nil {
		t.Fatalf("expected paragraph block in reopened cell")
	}
	if got := paragraph.PlainText(); got != "更新后内容" {
		t.Fatalf("unexpected updated cell text: %q", got)
	}
	if paragraph.Style.Alignment != "center" || paragraph.Style.SpacingBefore != 120 || paragraph.Style.SpacingAfter != 80 || paragraph.Style.FirstLineIndent != 210 {
		t.Fatalf("expected paragraph style to survive, got %+v", paragraph.Style)
	}
	if len(paragraph.Runs) != 1 {
		t.Fatalf("expected single run after cell update, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[0].Style.FontFamily != "Microsoft YaHei" || paragraph.Runs[0].Style.FontSizeHP != 24 || !paragraph.Runs[0].Style.Bold || paragraph.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected run style to survive, got %+v", paragraph.Runs[0].Style)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:tcW w:w="2400" w:type="dxa"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:shd w:fill="D9EAF7"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:sz w:val="24"/>`)
}

func TestExecutePlannedActionInsertInsideTableCellInheritsTableBodyAndRunStyle(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	document := &Document{
		ID:       "doc_table_cell_insert_inherit",
		Name:     "table-cell-insert-inherit.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID:            "tbl_main",
				PropertiesXML: `<w:tblStyle w:val="TableGrid"/><w:tblW w:type="auto" w:w="0"/>`,
				GridXML:       `<w:gridCol w:w="2400"/>`,
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{{
						ID:            "cell_1_1",
						PropertiesXML: `<w:tcW w:w="2400" w:type="dxa"/><w:shd w:fill="D9EAF7"/>`,
						Blocks: []Block{{
							Type: BlockTypeParagraph,
							Paragraph: &Paragraph{
								ID: "p_1_1",
								Style: ParagraphStyle{
									StyleID:         "TableBody",
									SpacingBefore:   120,
									SpacingAfter:    80,
									FirstLineIndent: 210,
								},
								Runs: []Run{{
									ID:   "r_1_1",
									Text: "原始值",
									Style: TextStyle{
										FontFamily: "Microsoft YaHei",
										FontSizeHP: 24,
										Bold:       true,
										BoldSet:    true,
										Color:      "1F1F1F",
									},
								}},
							},
						}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "原始值",
		NewText:              "补充说明",
		Position:             "after",
		PreferredParagraphID: "p_1_1",
		ReaderContextSummary: "在当前表格单元格正文后新增一句说明",
		FormatBasis:          "沿用同格 TableBody 正文样式和当前句子的字体字号",
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" || !result.Validation.OK {
		t.Fatalf("expected applied table-cell insert result, got %+v", result)
	}

	cell := result.Document.Blocks[0].Table.Rows[0].Cells[0]
	if len(cell.Blocks) != 2 {
		t.Fatalf("expected 2 paragraphs in cell after insert, got %+v", cell.Blocks)
	}
	inserted := cell.Blocks[1].Paragraph
	if inserted == nil {
		t.Fatalf("expected inserted paragraph in cell")
	}
	if got := inserted.PlainText(); got != "补充说明" {
		t.Fatalf("unexpected inserted cell text: %q", got)
	}
	if inserted.Style.StyleID != "TableBody" || inserted.Style.SpacingBefore != 120 || inserted.Style.SpacingAfter != 80 || inserted.Style.FirstLineIndent != 210 {
		t.Fatalf("expected inserted cell paragraph style to inherit TableBody layout, got %+v", inserted.Style)
	}
	if len(inserted.Runs) != 1 || inserted.Runs[0].Style.FontFamily != "Microsoft YaHei" || inserted.Runs[0].Style.FontSizeHP != 24 || !inserted.Runs[0].Style.Bold || !inserted.Runs[0].Style.BoldSet || inserted.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected inserted cell run style to inherit, got %+v", inserted.Runs)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "table-cell-insert-inherit.docx"))
	if err != nil {
		t.Fatalf("save docx: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen docx: %v", err)
	}
	reopenedCell := reopened.Blocks[0].Table.Rows[0].Cells[0]
	if len(reopenedCell.Blocks) != 2 {
		t.Fatalf("expected reopened cell to keep 2 paragraphs, got %+v", reopenedCell.Blocks)
	}
	reopenedInserted := reopenedCell.Blocks[1].Paragraph
	if reopenedInserted == nil {
		t.Fatalf("expected reopened inserted paragraph in cell")
	}
	if reopenedInserted.Style.StyleID != "TableBody" || reopenedInserted.Style.SpacingBefore != 120 || reopenedInserted.Style.SpacingAfter != 80 || reopenedInserted.Style.FirstLineIndent != 210 {
		t.Fatalf("expected reopened inserted TableBody paragraph style to survive, got %+v", reopenedInserted.Style)
	}
	if len(reopenedInserted.Runs) != 1 || reopenedInserted.Runs[0].Style.FontFamily != "Microsoft YaHei" || reopenedInserted.Runs[0].Style.FontSizeHP != 24 || !reopenedInserted.Runs[0].Style.Bold || !reopenedInserted.Runs[0].Style.BoldSet || reopenedInserted.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected reopened inserted cell run style to survive, got %+v", reopenedInserted.Runs)
	}
}

func TestPlanActionInsertAfterTableCellTitleUsesBodyTemplate(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_cell_title_body_template",
		Name:     "table-cell-title-body-template.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID:            "tbl_main",
				PropertiesXML: `<w:tblStyle w:val="TableGrid"/>`,
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{{
						ID: "cell_1_1",
						Blocks: []Block{
							{
								Type: BlockTypeParagraph,
								Paragraph: &Paragraph{
									ID: "p_title",
									Style: ParagraphStyle{
										StyleID:       "TableHeader",
										SpacingBefore: 80,
										SpacingAfter:  40,
									},
									Runs: []Run{{
										ID:    "r_title",
										Text:  "风险提示",
										Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true},
									}},
								},
							},
							{
								Type: BlockTypeParagraph,
								Paragraph: &Paragraph{
									ID: "p_body",
									Style: ParagraphStyle{
										StyleID:         "TableBody",
										SpacingBefore:   120,
										SpacingAfter:    80,
										FirstLineIndent: 210,
									},
									Runs: []Run{{
										ID:    "r_body",
										Text:  "当前为灰度验证阶段。",
										Style: TextStyle{FontFamily: "SimSun", FontSizeHP: 24, Color: "1F1F1F"},
									}},
								},
							},
						},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "风险提示",
		NewText:              "上线前先确认灰度范围。",
		Position:             "after",
		PreferredParagraphID: "p_title",
		ReaderContextSummary: "在当前表格单元格标题后新增一句正文说明。",
		FormatBasis:          "沿用同格 TableBody 正文样式和下方正文的字体字号。",
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" || len(plan.Operations) != 1 || plan.Operations[0].Paragraph == nil {
		t.Fatalf("unexpected insert plan: %+v", plan)
	}

	insertParagraph := plan.Operations[0].Paragraph
	if insertParagraph.Style.StyleID != "TableBody" || insertParagraph.Style.SpacingBefore != 120 || insertParagraph.Style.SpacingAfter != 80 || insertParagraph.Style.FirstLineIndent != 210 {
		t.Fatalf("expected insert paragraph to use same-cell body template, got %+v", insertParagraph.Style)
	}
	if len(insertParagraph.Runs) != 1 || insertParagraph.Runs[0].Style.FontFamily != "SimSun" || insertParagraph.Runs[0].Style.FontSizeHP != 24 || insertParagraph.Runs[0].Style.BoldSet || insertParagraph.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected insert run style to use same-cell body run style, got %+v", insertParagraph.Runs)
	}
}

func TestApplyUpdateTableCellDoesNotResolvePlaceholderTableIDWhenDocumentHasMultipleTables(t *testing.T) {
	service := NewService(".")
	document := &Document{
		ID:       "doc_table_alias_multiple",
		Name:     "table-alias-multiple.docx",
		Revision: 1,
		Blocks: []Block{
			{
				Type: BlockTypeTable,
				Table: &Table{
					ID: "tbl_a",
					Rows: []TableRow{{
						ID: "row_a",
						Cells: []TableCell{{
							ID:     "cell_a",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_a", Runs: []Run{{ID: "r_a", Text: "A1"}}}}},
						}},
					}},
				},
			},
			{
				Type: BlockTypeTable,
				Table: &Table{
					ID: "tbl_b",
					Rows: []TableRow{{
						ID: "row_b",
						Cells: []TableCell{{
							ID:     "cell_b",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_b", Runs: []Run{{ID: "r_b", Text: "B1"}}}}},
						}},
					}},
				},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:      OperationUpdateTableCell,
		TableID:   "t1",
		RowIndex:  0,
		CellIndex: 0,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "新值"}}},
	}})
	if err == nil {
		t.Fatalf("expected unresolved placeholder table id to fail in multi-table document")
	}
	if got := err.Error(); got != "未找到目标表格: t1" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestPlanActionInsertCanDisableStyleAndNumberingInheritance(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_insert_constraints",
		Name:     "insert-constraints.docx",
		Revision: 1,
		Meta:     DocumentMeta{Format: "docx"},
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID:            "h1",
				NumberingText: "一、",
				Style: ParagraphStyle{
					StyleID:   "Heading1",
					Alignment: "center",
					Numbering: &NumberingRef{NumID: "n1", Level: 0},
				},
				Runs: []Run{{ID: "r1", Text: "现状分析", Style: TextStyle{FontFamily: "Microsoft YaHei", Bold: true, BoldSet: true}}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	preserveStyleFalse := false
	preserveNumberingFalse := false
	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "insert",
		TargetText: "现状分析",
		NewText:    "新增正文",
		Constraints: &ActionConstraints{
			RawText:           "不要继承标题样式和编号",
			PreserveStyle:     &preserveStyleFalse,
			PreserveNumbering: &preserveNumberingFalse,
		},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" || len(plan.Operations) != 1 || plan.Operations[0].Paragraph == nil {
		t.Fatalf("unexpected insert plan: %+v", plan)
	}
	insertParagraph := plan.Operations[0].Paragraph
	if insertParagraph.Style.StyleID != "" || insertParagraph.Style.Alignment != "" {
		t.Fatalf("expected insert paragraph style not to inherit heading style, got %+v", insertParagraph.Style)
	}
	if insertParagraph.Style.Numbering != nil {
		t.Fatalf("expected insert paragraph numbering not to inherit heading numbering, got %+v", insertParagraph.Style)
	}
	if len(insertParagraph.Runs) != 1 || insertParagraph.Runs[0].Style.FontFamily != "" || insertParagraph.Runs[0].Style.BoldSet {
		t.Fatalf("expected insert run style not to inherit heading run style, got %+v", insertParagraph.Runs)
	}
	if plan.ReplayAction.Constraints == nil || plan.ReplayAction.Constraints.PreserveStyle == nil || *plan.ReplayAction.Constraints.PreserveStyle {
		t.Fatalf("expected replay action to preserve structured constraints, got %+v", plan.ReplayAction.Constraints)
	}
}

func TestPlanActionInsertAfterBoldLabelUsesFollowingBodyTemplate(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_insert_after_bold_label",
		Name:     "insert-after-bold-label.docx",
		Revision: 1,
		Meta:     DocumentMeta{Format: "docx"},
		Blocks: []Block{
			{
				Type: BlockTypeParagraph,
				Paragraph: &Paragraph{
					ID: "p_label",
					Style: ParagraphStyle{
						StyleID:       "Normal",
						SpacingBefore: 80,
						SpacingAfter:  40,
					},
					Runs: []Run{{
						ID:    "r_label",
						Text:  "基本原则",
						Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true},
					}},
				},
			},
			{
				Type: BlockTypeParagraph,
				Paragraph: &Paragraph{
					ID: "p_body",
					Style: ParagraphStyle{
						StyleID:         "Normal",
						SpacingBefore:   120,
						SpacingAfter:    80,
						FirstLineIndent: 420,
					},
					Runs: []Run{{
						ID:    "r_body",
						Text:  "坚持问题导向，确保制度可执行。",
						Style: TextStyle{FontFamily: "SimSun", FontSizeHP: 24, Color: "1F1F1F"},
					}},
				},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "基本原则",
		NewText:              "新增一条：坚持闭环验收。",
		Position:             "after",
		PreferredParagraphID: "p_label",
		ReaderContextSummary: "在“基本原则”标题标签后新增一条正文说明。",
		FormatBasis:          "沿用后续正文段落的字体字号、缩进和非加粗格式。",
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" || len(plan.Operations) != 1 || plan.Operations[0].Paragraph == nil {
		t.Fatalf("unexpected insert plan: %+v", plan)
	}

	insertParagraph := plan.Operations[0].Paragraph
	if insertParagraph.Style.StyleID != "Normal" || insertParagraph.Style.SpacingBefore != 120 || insertParagraph.Style.SpacingAfter != 80 || insertParagraph.Style.FirstLineIndent != 420 {
		t.Fatalf("expected insert paragraph to use following body template, got %+v", insertParagraph.Style)
	}
	if len(insertParagraph.Runs) != 1 || insertParagraph.Runs[0].Style.FontFamily != "SimSun" || insertParagraph.Runs[0].Style.FontSizeHP != 24 || insertParagraph.Runs[0].Style.BoldSet || insertParagraph.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected insert run style to use following body template, got %+v", insertParagraph.Runs)
	}
}

func TestExecutePlannedActionInsertPreservesLeadingSpaceInNewText(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	document := &Document{
		ID:       "doc_insert_preserve_leading_space",
		Name:     "insert-preserve-leading-space.docx",
		Revision: 1,
		Meta:     DocumentMeta{Format: "docx"},
		Blocks: []Block{
			{
				Type: BlockTypeParagraph,
				Paragraph: &Paragraph{
					ID:    "p_label",
					Style: ParagraphStyle{StyleID: "Normal", SpacingBefore: 80, SpacingAfter: 40},
					Runs:  []Run{{ID: "r_label", Text: "基本原则", Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true}}},
				},
			},
			{
				Type: BlockTypeParagraph,
				Paragraph: &Paragraph{
					ID:    "p_body",
					Style: ParagraphStyle{StyleID: "Normal", SpacingBefore: 120, SpacingAfter: 80, FirstLineIndent: 420},
					Runs:  []Run{{ID: "r_body", Text: "坚持问题导向，确保制度可执行。", Style: TextStyle{FontFamily: "SimSun", FontSizeHP: 24, Color: "1F1F1F"}}},
				},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "基本原则",
		NewText:              " 新增一条：坚持闭环验收。",
		Position:             "after",
		PreferredParagraphID: "p_label",
		ReaderContextSummary: "在“基本原则”标题标签后新增一条正文说明。",
		FormatBasis:          "沿用后续正文段落的字体字号、缩进和非加粗格式，并保留文本前导空格。",
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" || !result.Validation.OK {
		t.Fatalf("expected applied insert result, got %+v", result)
	}
	insertedFound := false
	for _, paragraph := range result.Document.Paragraphs {
		if paragraph.PlainText() == " 新增一条：坚持闭环验收。" {
			insertedFound = true
			break
		}
	}
	if !insertedFound {
		t.Fatalf("expected inserted text to preserve leading space, got paragraphs %+v", result.Document.Paragraphs)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "insert-preserve-leading-space.docx"))
	if err != nil {
		t.Fatalf("save docx: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen docx: %v", err)
	}
	reopenedFound := false
	for _, paragraph := range reopened.Paragraphs {
		if paragraph.PlainText() == " 新增一条：坚持闭环验收。" {
			reopenedFound = true
			break
		}
	}
	if !reopenedFound {
		t.Fatalf("expected reopened inserted text to preserve leading space, got paragraphs %+v", reopened.Paragraphs)
	}
}

func TestPlanActionInsertUsesMatchedRunStyleWhenTemplateIsAnchor(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_insert_matched_run_style",
		Name:     "insert-matched-run-style.docx",
		Revision: 1,
		Meta:     DocumentMeta{Format: "docx"},
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID: "p1",
				Style: ParagraphStyle{
					StyleID:         "Normal",
					LeftIndent:      480,
					FirstLineIndent: 420,
				},
				Runs: []Run{
					{ID: "r1", Text: "前缀：", Style: TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
					{ID: "r2", Text: "重点内容", Style: TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 32, Bold: true, BoldSet: true}},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "重点内容",
		NewText:              "新增说明",
		Position:             "after",
		PreferredParagraphID: "p1",
		ReaderContextSummary: "在当前正文段后新增一句说明",
		FormatBasis:          "沿用命中正文段中‘重点内容’的字体字号和本段缩进",
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" || len(plan.Operations) != 1 || plan.Operations[0].Paragraph == nil {
		t.Fatalf("unexpected insert plan: %+v", plan)
	}

	insertParagraph := plan.Operations[0].Paragraph
	if insertParagraph.Style.StyleID != "Normal" || insertParagraph.Style.LeftIndent != 480 || insertParagraph.Style.FirstLineIndent != 420 {
		t.Fatalf("expected insert paragraph style to inherit anchor layout, got %+v", insertParagraph.Style)
	}
	if len(insertParagraph.Runs) != 1 {
		t.Fatalf("expected single insert run, got %+v", insertParagraph.Runs)
	}
	if insertParagraph.Runs[0].Style.FontFamily != "Microsoft YaHei" || insertParagraph.Runs[0].Style.FontSizeHP != 32 || !insertParagraph.Runs[0].Style.Bold || !insertParagraph.Runs[0].Style.BoldSet {
		t.Fatalf("expected insert run style to inherit matched run style, got %+v", insertParagraph.Runs[0].Style)
	}
}

func TestExecutePlannedActionInsertAfterListItemInheritsNumberingLayoutAndRunStyle(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	document := &Document{
		ID:       "doc_insert_list_item_inherit",
		Name:     "insert-list-item-inherit.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID:            "p1",
				NumberingText: "一、",
				Style: ParagraphStyle{
					StyleID:         "ListParagraph",
					LeftIndent:      720,
					FirstLineIndent: -360,
					Numbering:       &NumberingRef{NumID: "41", Level: 0},
				},
				Runs: []Run{{
					ID:   "r1",
					Text: "现状分析",
					Style: TextStyle{
						FontFamily: "Microsoft YaHei",
						FontSizeHP: 28,
						Color:      "1F1F1F",
					},
				}},
			},
		}},
		Meta: DocumentMeta{
			Format: "docx",
			Numbering: []NumberingDefinition{{
				NumID:      "41",
				AbstractID: "41",
				Levels: []NumberingLevel{{
					Level:  0,
					Start:  1,
					Format: "chineseCounting",
					Text:   "%1、",
				}},
			}},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:               "insert",
		TargetText:           "现状分析",
		NewText:              "实施路径",
		Position:             "after",
		PreferredParagraphID: "p1",
		ReaderContextSummary: "在当前列表项后新增一条同级内容",
		FormatBasis:          "沿用当前列表项的编号、缩进和字体",
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" || !result.Validation.OK {
		t.Fatalf("expected applied insert result, got %+v", result)
	}
	if len(result.Document.Paragraphs) != 2 {
		t.Fatalf("expected 2 paragraphs after list insert, got %d", len(result.Document.Paragraphs))
	}

	inserted := result.Document.Paragraphs[1]
	if got := inserted.PlainText(); got != "实施路径" {
		t.Fatalf("unexpected inserted list item text: %q", got)
	}
	if inserted.NumberingText != "二、" {
		t.Fatalf("expected inserted numbering text 二、, got %q", inserted.NumberingText)
	}
	if inserted.Style.Numbering == nil || inserted.Style.Numbering.NumID != "41" || inserted.Style.Numbering.Level != 0 {
		t.Fatalf("expected inserted numbering ref to inherit, got %+v", inserted.Style.Numbering)
	}
	if inserted.Style.LeftIndent != 720 || inserted.Style.FirstLineIndent != -360 {
		t.Fatalf("expected inserted paragraph indents to inherit, got %+v", inserted.Style)
	}
	if len(inserted.Runs) != 1 {
		t.Fatalf("expected single inserted run, got %+v", inserted.Runs)
	}
	if inserted.Runs[0].Style.FontFamily != "Microsoft YaHei" || inserted.Runs[0].Style.FontSizeHP != 28 || inserted.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected inserted run style to inherit, got %+v", inserted.Runs[0].Style)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "insert-list-item-inherit.docx"))
	if err != nil {
		t.Fatalf("save docx: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen docx: %v", err)
	}
	applyResolvedNumbering(reopened)
	if len(reopened.Paragraphs) != 2 {
		t.Fatalf("expected reopened document to contain 2 paragraphs, got %d", len(reopened.Paragraphs))
	}
	reopenedInserted := reopened.Paragraphs[1]
	if reopenedInserted.NumberingText != "二、" {
		t.Fatalf("expected reopened inserted numbering text 二、, got %q", reopenedInserted.NumberingText)
	}
	if reopenedInserted.Style.Numbering == nil || reopenedInserted.Style.Numbering.NumID != "41" || reopenedInserted.Style.LeftIndent != 720 || reopenedInserted.Style.FirstLineIndent != -360 {
		t.Fatalf("expected reopened inserted paragraph numbering/layout to survive, got %+v", reopenedInserted.Style)
	}
	if len(reopenedInserted.Runs) != 1 || reopenedInserted.Runs[0].Style.FontFamily != "Microsoft YaHei" || reopenedInserted.Runs[0].Style.FontSizeHP != 28 || reopenedInserted.Runs[0].Style.Color != "1F1F1F" {
		t.Fatalf("expected reopened inserted run style to survive, got %+v", reopenedInserted.Runs)
	}
}

func TestPlanActionAmbiguousCanUseBestCandidateWhenPolicyAllows(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_ambiguous_best_candidate",
		Name:     "ambiguous-best-candidate.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "现状分析"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "现状分析"}}}},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mustBeUniqueFalse := false
	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "replace",
		TargetText: "现状分析",
		NewText:    "风险分析",
		Constraints: &ActionConstraints{
			MustBeUnique: &mustBeUniqueFalse,
		},
		FailurePolicy: &ActionFailurePolicy{OnAmbiguous: "use_best_candidate"},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" || len(plan.Operations) != 1 {
		t.Fatalf("expected ready plan with one operation, got %+v", plan)
	}
	if plan.ReplayAction.PreferredParagraphID != "p1" {
		t.Fatalf("expected best candidate policy to lock first paragraph, got %+v", plan.ReplayAction)
	}
	if plan.Diagnostics != nil {
		t.Fatalf("expected ambiguity to be resolved by policy, got diagnostics %+v", plan.Diagnostics)
	}
}

func TestPlanActionMustBeUniqueOverridesBestCandidatePolicy(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_ambiguous_must_be_unique",
		Name:     "ambiguous-must-be-unique.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "现状分析"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "现状分析"}}}},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mustBeUniqueTrue := true
	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "replace",
		TargetText: "现状分析",
		NewText:    "风险分析",
		Constraints: &ActionConstraints{
			MustBeUnique: &mustBeUniqueTrue,
		},
		FailurePolicy: &ActionFailurePolicy{OnAmbiguous: "use_best_candidate"},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "AMBIGUOUS_TARGET" {
		t.Fatalf("expected must_be_unique to keep ambiguity failure, got %+v", plan)
	}
	if plan.Diagnostics.CandidateCount != 2 {
		t.Fatalf("expected ambiguity diagnostics to retain candidate count, got %+v", plan.Diagnostics)
	}
}

func TestExecutePlannedActionRollbackAndReportOnValidationFailure(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_validation_rollback",
		Name:     "validation-rollback.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:         "delete",
		RetrievalQuery: "原始内容",
		FailurePolicy: &ActionFailurePolicy{
			OnValidationFailed: "rollback_and_report",
		},
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed status after rollback, got %+v", result)
	}
	if result.Validation.OK {
		t.Fatalf("expected validation failure, got %+v", result.Validation)
	}
	if !strings.Contains(result.StatusText, "已按 failure_policy 回滚到执行前版本") {
		t.Fatalf("expected rollback status text, got %q", result.StatusText)
	}
	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after rollback: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "原始内容" {
		t.Fatalf("expected document to be rolled back, got %q", got)
	}
}

func TestExecutePlannedActionWarningStatusTextOnValidationFailure(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_validation_warning",
		Name:     "validation-warning.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:         "delete",
		RetrievalQuery: "原始内容",
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied_with_warning" {
		t.Fatalf("expected applied_with_warning status, got %+v", result)
	}
	if result.Validation.OK {
		t.Fatalf("expected validation failure, got %+v", result.Validation)
	}
	if strings.Contains(result.StatusText, "已删除") {
		t.Fatalf("expected warning status text to avoid delete-success wording, got %q", result.StatusText)
	}
	if !strings.Contains(result.StatusText, "自动验证未通过") {
		t.Fatalf("expected warning status text to mention validation failure, got %q", result.StatusText)
	}
	if result.Document == nil {
		t.Fatal("expected result document to be present")
	}
}

func TestExecutePlannedActionDeleteWholeParagraphRemovesBlock(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_delete_whole_paragraph",
		Name:     "delete-whole-paragraph.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "第一段整段删除"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "第二段保留"}}}},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{
		Action:     "delete",
		TargetText: "第一段整段删除",
		TargetSelector: &ActionTargetSelector{
			Type:        "paragraph_id_selector",
			ParagraphID: "p1",
		},
	})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" {
		t.Fatalf("expected applied status, got %+v", result)
	}
	if !result.Validation.OK {
		t.Fatalf("expected validation ok, got %+v", result.Validation)
	}
	if result.Document == nil {
		t.Fatalf("expected updated document")
	}
	if len(result.Document.Blocks) != 1 {
		t.Fatalf("expected 1 block after deleting whole paragraph, got %d", len(result.Document.Blocks))
	}
	remaining := result.Document.Blocks[0].Paragraph
	if remaining == nil || remaining.PlainText() != "第二段保留" {
		t.Fatalf("expected second paragraph to remain, got %+v", remaining)
	}
	if len(result.Diffs) == 0 || result.Diffs[0].Type != DiffDeleted || result.Diffs[0].ParagraphID != "p1" {
		t.Fatalf("expected deleted diff for p1, got %+v", result.Diffs)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Document.Blocks) != 1 || snapshot.Document.Blocks[0].Paragraph == nil || snapshot.Document.Blocks[0].Paragraph.PlainText() != "第二段保留" {
		t.Fatalf("expected persisted snapshot to remove first paragraph, got %+v", snapshot.Document.Blocks)
	}
}

func TestExecutePlannedActionReplaceWholeParagraphExpandsIntoStyledParagraphs(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_replace_multi_paragraph",
		Name:     "replace-multi-paragraph.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID: "p1",
				Style: ParagraphStyle{
					StyleID:         "Normal",
					LeftIndent:      480,
					FirstLineIndent: 420,
					LineSpacing:     360,
					LineSpacingRule: "auto",
				},
				Runs: []Run{{ID: "r1", Text: "原段内容", Style: TextStyle{FontFamily: "宋体", FontSizeHP: 24}}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{Action: "replace", TargetText: "原段内容", NewText: "第一段\n\n第二段"})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" {
		t.Fatalf("expected applied status, got %+v", result)
	}
	if len(result.Document.Paragraphs) != 2 {
		t.Fatalf("expected replace to expand into 2 paragraphs, got %d", len(result.Document.Paragraphs))
	}
	if got := result.Document.Paragraphs[0].PlainText(); got != "第一段" {
		t.Fatalf("unexpected first paragraph text: %q", got)
	}
	if got := result.Document.Paragraphs[1].PlainText(); got != "第二段" {
		t.Fatalf("unexpected second paragraph text: %q", got)
	}
	for index, paragraph := range result.Document.Paragraphs {
		if paragraph.Style.StyleID != "Normal" || paragraph.Style.LeftIndent != 480 || paragraph.Style.FirstLineIndent != 420 || paragraph.Style.LineSpacing != 360 || paragraph.Style.LineSpacingRule != "auto" {
			t.Fatalf("paragraph %d style not preserved: %+v", index, paragraph.Style)
		}
		if len(paragraph.Runs) == 0 || paragraph.Runs[0].Style.FontFamily != "宋体" || paragraph.Runs[0].Style.FontSizeHP != 24 {
			t.Fatalf("paragraph %d text style not preserved: %+v", index, paragraph.Runs)
		}
	}
}

func TestValidateActionResultDeleteUsesRangeEvidenceWhenTextStillExists(t *testing.T) {
	result := &ApplyResult{
		Document: &Document{Paragraphs: []Paragraph{{ID: "p1", Runs: []Run{{ID: "r_after", Text: "甲"}}}}},
		Diffs: []ParagraphDiff{{
			ParagraphID: "p1",
			Type:        DiffUpdated,
			BeforeText:  "甲甲",
			AfterText:   "甲",
			CharacterRanges: []CharacterRangeDiff{{
				Type:        DiffDeleted,
				BeforeStart: 0,
				BeforeEnd:   1,
				AfterStart:  0,
				AfterEnd:    0,
				BeforeText:  "甲",
				AfterText:   "",
			}},
		}},
	}

	validation := ValidateActionResult(result, EditorAction{Action: "delete", TargetText: "甲"}, "p1")
	if !validation.OK {
		t.Fatalf("expected range-aware delete validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultReplaceUsesRangeEvidence(t *testing.T) {
	result := &ApplyResult{
		Document: &Document{Paragraphs: []Paragraph{{ID: "p1", Runs: []Run{{ID: "r_after", Text: "100 和 200"}}}}},
		Diffs: []ParagraphDiff{{
			ParagraphID: "p1",
			Type:        DiffUpdated,
			BeforeText:  "100 和 100",
			AfterText:   "100 和 200",
			CharacterRanges: []CharacterRangeDiff{{
				Type:        DiffUpdated,
				BeforeStart: 4,
				BeforeEnd:   7,
				AfterStart:  4,
				AfterEnd:    7,
				BeforeText:  "100",
				AfterText:   "200",
			}},
		}},
	}

	validation := ValidateActionResult(result, EditorAction{Action: "replace", TargetText: "100", NewText: "200"}, "p1")
	if !validation.OK {
		t.Fatalf("expected range-aware replace validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultUpdateTableCellUsesStructureEvidence(t *testing.T) {
	document := &Document{
		ID:       "doc_table_validate",
		Name:     "table-validate.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{{
					ID: "row1",
					Cells: []TableCell{{
						ID: "cell1",
						Blocks: []Block{{
							Type:      BlockTypeParagraph,
							Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "更新后内容"}}},
						}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()

	validation := ValidateActionResult(&ApplyResult{Document: document}, EditorAction{
		Action:         "update_table_cell",
		TableID:        "tbl_main",
		RowIndex:       0,
		CellIndex:      0,
		TableCell:      &TableCellInput{Paragraphs: []ParagraphInput{{Text: "更新后内容"}}},
		TargetSelector: &ActionTargetSelector{Type: "table_cell_selector", TableID: "tbl_main", RowIndex: 0, CellIndex: 0},
	}, "p1")
	if !validation.OK {
		t.Fatalf("expected structure-aware table-cell validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultInsertUsesAdjacentParagraphStructure(t *testing.T) {
	document := &Document{
		ID:       "doc_insert_validate",
		Name:     "insert-validate.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_anchor", Runs: []Run{{ID: "r1", Text: "锚点段落"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_inserted", Runs: []Run{{ID: "r2", Text: "新增正文"}}}},
		},
	}
	document.rebuildParagraphs()

	validation := ValidateActionResult(&ApplyResult{Document: document}, EditorAction{Action: "insert", NewText: "新增正文", Position: "after"}, "p_anchor")
	if !validation.OK {
		t.Fatalf("expected structure-aware insert validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultInsertTableUsesAdjacentStructure(t *testing.T) {
	document := &Document{
		ID:       "doc_insert_table_validate",
		Name:     "insert-table-validate.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_anchor", Runs: []Run{{ID: "r1", Text: "表格锚点"}}}},
			{Type: BlockTypeTable, Table: &Table{ID: "tbl1", Rows: []TableRow{{
				ID: "row1",
				Cells: []TableCell{
					{ID: "cell11", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p11", Runs: []Run{{ID: "r11", Text: "项目"}}}}}},
					{ID: "cell12", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p12", Runs: []Run{{ID: "r12", Text: "金额"}}}}}},
				},
			}}}},
		},
	}
	document.rebuildParagraphs()

	validation := ValidateActionResult(&ApplyResult{Document: document}, EditorAction{
		Action:   "insert_table",
		Position: "after",
		TableSpec: &TableInput{Rows: []TableRowInput{{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "项目"}}},
			{Paragraphs: []ParagraphInput{{Text: "金额"}}},
		}}}},
	}, "p_anchor")
	if !validation.OK {
		t.Fatalf("expected structure-aware insert_table validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultFormatSectionUsesLayoutStructure(t *testing.T) {
	document := &Document{Meta: DocumentMeta{SectionLayout: SectionLayout{PageWidth: 16838, PageHeight: 11906, Orientation: "landscape", MarginLeft: 1200}}}
	validation := ValidateActionResult(&ApplyResult{Document: document}, EditorAction{Action: "format_section", SectionLayoutPatch: SectionLayout{Orientation: "landscape", MarginLeft: 1200}}, "")
	if !validation.OK {
		t.Fatalf("expected section layout validation to pass, got %+v", validation)
	}
}

func TestValidateActionResultUpdateHeaderFooterUsesStructureEvidence(t *testing.T) {
	document := &Document{Meta: DocumentMeta{HeaderFooters: []HeaderFooterPart{{Kind: "header", Variant: "default", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "hp1", Runs: []Run{{ID: "hr1", Text: "新页眉"}}}}}}}}}
	document.Meta.HeaderFooters[0].rebuildParagraphs()
	validation := ValidateActionResult(&ApplyResult{Document: document}, EditorAction{Action: "update_header_footer", HeaderFooter: &HeaderFooterInput{Kind: "header", Blocks: []BlockInput{{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "新页眉"}}}}}, "")
	if !validation.OK {
		t.Fatalf("expected header/footer structure validation to pass, got %+v", validation)
	}
}

func TestPlanActionDeleteTableColumnRejectsLastColumnWithTableStructureConflict(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_last_column_conflict",
		Name:     "table-last-column-conflict.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_single_col",
				Rows: []TableRow{{
					ID: "row1",
					Cells: []TableCell{{
						ID:     "cell1",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "预算"}}}}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:    "delete_table_column",
		TableID:   "tbl_single_col",
		CellIndex: 0,
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "TABLE_STRUCTURE_CONFLICT" {
		t.Fatalf("expected table structure conflict, got %+v", plan)
	}
	if plan.Diagnostics.Retryable {
		t.Fatalf("expected non-retryable delete_table_column conflict, got %+v", plan.Diagnostics)
	}
}

func TestPlanActionInsertTableColumnRejectsMismatchedTemplateCells(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_column_cells_conflict",
		Name:     "table-column-cells-conflict.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{
					{
						ID: "row1",
						Cells: []TableCell{{
							ID:     "cell11",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p11", Runs: []Run{{ID: "r11", Text: "A1"}}}}},
						}},
					},
					{
						ID: "row2",
						Cells: []TableCell{{
							ID:     "cell21",
							Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p21", Runs: []Run{{ID: "r21", Text: "A2"}}}}},
						}},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:    "insert_table_column",
		TableID:   "tbl_main",
		CellIndex: 0,
		TableColumn: &TableColumnInput{Cells: []TableCellInput{
			{Paragraphs: []ParagraphInput{{Text: "X1"}}},
			{Paragraphs: []ParagraphInput{{Text: "X2"}}},
			{Paragraphs: []ParagraphInput{{Text: "X3"}}},
		}},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "TABLE_STRUCTURE_CONFLICT" {
		t.Fatalf("expected table structure conflict, got %+v", plan)
	}
	if !plan.Diagnostics.Retryable {
		t.Fatalf("expected retryable insert_table_column conflict, got %+v", plan.Diagnostics)
	}
}

func TestPlanActionUpdateTableCellMarksOutOfRangeConflictRetryable(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_cell_conflict_retryable",
		Name:     "table-cell-conflict-retryable.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{{
					ID: "row1",
					Cells: []TableCell{{
						ID:     "cell11",
						Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p11", Runs: []Run{{ID: "r11", Text: "A1"}}}}},
					}},
				}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:    "update_table_cell",
		TableID:   "tbl_main",
		RowIndex:  0,
		CellIndex: 3,
		TableCell: &TableCellInput{Paragraphs: []ParagraphInput{{Text: "更新值"}}},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "TABLE_STRUCTURE_CONFLICT" {
		t.Fatalf("expected table structure conflict, got %+v", plan)
	}
	if !plan.Diagnostics.Retryable {
		t.Fatalf("expected retryable update_table_cell conflict, got %+v", plan.Diagnostics)
	}
}

func TestPlanActionFallsBackWhenPreferredParagraphIDMissing(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_preferred_paragraph_fallback",
		Name:     "preferred-paragraph-fallback.docx",
		Revision: 1,
		Paragraphs: []Paragraph{
			{ID: "p1", Runs: []Run{{ID: "r1", Text: "当前通过率为 90%"}}},
			{ID: "p2", Runs: []Run{{ID: "r2", Text: "其他说明"}}},
		},
	}
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:               "replace",
		TargetText:           "通过率为 90%",
		NewText:              "通过率为 92%",
		PreferredParagraphID: "p_missing",
		ContextScope:         "paragraph",
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" {
		t.Fatalf("expected ready plan, got %+v", plan)
	}
	if plan.ResolvedTarget == nil || plan.ResolvedTarget.ParagraphID != "p1" {
		t.Fatalf("expected resolved paragraph p1, got %+v", plan.ResolvedTarget)
	}
	if plan.ReplayAction.PreferredParagraphID != "p1" {
		t.Fatalf("expected replay preferred paragraph id p1, got %+v", plan.ReplayAction)
	}
	if plan.ReplayAction.TargetSelector == nil || plan.ReplayAction.TargetSelector.Type != "paragraph_id_selector" || plan.ReplayAction.TargetSelector.ParagraphID != "p1" {
		t.Fatalf("expected replay selector to be canonicalized to paragraph_id_selector p1, got %+v", plan.ReplayAction.TargetSelector)
	}
}

func TestPlanActionParagraphSelectorFallbackUpdatesReplayTarget(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_selector_fallback",
		Name:     "selector-fallback.docx",
		Revision: 1,
		Paragraphs: []Paragraph{
			{ID: "p1", Runs: []Run{{ID: "r1", Text: "结论：继续推进后端解析"}}},
		},
	}
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "replace",
		TargetText: "继续推进后端解析",
		NewText:    "继续推进后端权威解析",
		TargetSelector: &ActionTargetSelector{
			Type:        "paragraph_id_selector",
			ParagraphID: "p_missing",
		},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" {
		t.Fatalf("expected ready plan, got %+v", plan)
	}
	if plan.ReplayAction.TargetSelector == nil || plan.ReplayAction.TargetSelector.Type != "paragraph_id_selector" || plan.ReplayAction.TargetSelector.ParagraphID != "p1" {
		t.Fatalf("expected replay selector to be canonicalized to p1, got %+v", plan.ReplayAction.TargetSelector)
	}
	if plan.ReplayAction.PreferredParagraphID != "p1" {
		t.Fatalf("expected replay preferred paragraph id p1, got %+v", plan.ReplayAction)
	}
	if plan.ReplayAction.TargetSelector.HeadingLevel != 0 || plan.ReplayAction.TargetSelector.Ordinal != 0 || plan.ReplayAction.TargetSelector.Chapter != "" || plan.ReplayAction.TargetSelector.TextEquals != "" || plan.ReplayAction.TargetSelector.Region != "" {
		t.Fatalf("expected replay selector to clear heading selector fields, got %+v", plan.ReplayAction.TargetSelector)
	}
}

func TestNormalizeEditorActionSelectorOverridesLegacyAnchors(t *testing.T) {
	action := normalizeEditorAction(EditorAction{
		Action:               "replace",
		PreferredParagraphID: "p_stale",
		TableID:              "tbl_stale",
		RowIndex:             9,
		CellIndex:            7,
		TargetSelector: &ActionTargetSelector{
			Type:         " Paragraph_ID_Selector ",
			ParagraphID:  " p_live ",
			TableID:      "tbl_noise",
			RowIndex:     4,
			CellIndex:    3,
			HeadingLevel: 2,
			Ordinal:      1,
			Chapter:      "第一章",
			TextEquals:   "结论",
			Region:       "Body",
		},
	})

	if action.PreferredParagraphID != "p_live" {
		t.Fatalf("expected selector to override preferred paragraph id, got %+v", action)
	}
	if action.TableID != "" || action.RowIndex != 0 || action.CellIndex != 0 {
		t.Fatalf("expected paragraph selector to clear stale table anchors, got %+v", action)
	}
	if action.TargetSelector == nil || action.TargetSelector.Type != "paragraph_id_selector" || action.TargetSelector.ParagraphID != "p_live" {
		t.Fatalf("expected normalized paragraph selector, got %+v", action.TargetSelector)
	}
	if action.TargetSelector.TableID != "" || action.TargetSelector.RowIndex != 0 || action.TargetSelector.CellIndex != 0 || action.TargetSelector.HeadingLevel != 0 || action.TargetSelector.Ordinal != 0 || action.TargetSelector.Chapter != "" || action.TargetSelector.TextEquals != "" || action.TargetSelector.Region != "" {
		t.Fatalf("expected paragraph selector contract to clear irrelevant fields, got %+v", action.TargetSelector)
	}
}

func TestNormalizeEditorActionTableCellSelectorOverridesLegacyIndices(t *testing.T) {
	action := normalizeEditorAction(EditorAction{
		Action:               "update_table_cell",
		PreferredParagraphID: "p_stale",
		TableID:              "tbl_stale",
		RowIndex:             5,
		CellIndex:            4,
		TargetSelector: &ActionTargetSelector{
			Type:         "TABLE_CELL_SELECTOR",
			ParagraphID:  "p_noise",
			TableID:      "tbl_live",
			RowIndex:     0,
			CellIndex:    0,
			HeadingLevel: 3,
			Ordinal:      2,
			Chapter:      "第二章",
			TextEquals:   "预算",
			Region:       "header",
		},
	})

	if action.PreferredParagraphID != "" {
		t.Fatalf("expected table selector to clear stale paragraph anchor, got %+v", action)
	}
	if action.TableID != "tbl_live" || action.RowIndex != 0 || action.CellIndex != 0 {
		t.Fatalf("expected table cell selector to override legacy indices exactly, got %+v", action)
	}
	if action.TargetSelector == nil || action.TargetSelector.Type != "table_cell_selector" || action.TargetSelector.TableID != "tbl_live" || action.TargetSelector.RowIndex != 0 || action.TargetSelector.CellIndex != 0 {
		t.Fatalf("expected normalized table cell selector, got %+v", action.TargetSelector)
	}
	if action.TargetSelector.ParagraphID != "" || action.TargetSelector.HeadingLevel != 0 || action.TargetSelector.Ordinal != 0 || action.TargetSelector.Chapter != "" || action.TargetSelector.TextEquals != "" || action.TargetSelector.Region != "" {
		t.Fatalf("expected table cell selector contract to clear irrelevant fields, got %+v", action.TargetSelector)
	}
}

func TestPlanActionFallsBackWhenTableIDMissing(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_id_fallback",
		Name:     "table-id-fallback.docx",
		Revision: 1,
		Blocks: []Block{
			{
				Type: BlockTypeTable,
				Table: &Table{
					ID: "tbl_real_metrics",
					Rows: []TableRow{
						{
							ID: "row0",
							Cells: []TableCell{
								{ID: "cell00", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p00", Runs: []Run{{ID: "r00", Text: "指标"}}}}}},
								{ID: "cell01", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p01", Runs: []Run{{ID: "r01", Text: "当前值"}}}}}},
							},
						},
						{
							ID: "row1",
							Cells: []TableCell{
								{ID: "cell10", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p10", Runs: []Run{{ID: "r10", Text: "通过率"}}}}}},
								{ID: "cell11", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p11", Runs: []Run{{ID: "r11", Text: "90%"}}}}}},
							},
						},
					},
				},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "update_table_cell",
		TargetText: "通过率",
		TableID:    "tbl_missing",
		RowIndex:   1,
		CellIndex:  1,
		TableCell:  &TableCellInput{Paragraphs: []ParagraphInput{{Text: "92%"}}},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" {
		t.Fatalf("expected ready plan, got %+v", plan)
	}
	if plan.ResolvedTarget == nil || plan.ResolvedTarget.TableID != "tbl_real_metrics" {
		t.Fatalf("expected resolved table tbl_real_metrics, got %+v", plan.ResolvedTarget)
	}
	if plan.ReplayAction.TableID != "tbl_real_metrics" {
		t.Fatalf("expected replay table id tbl_real_metrics, got %+v", plan.ReplayAction)
	}
	if plan.ReplayAction.TargetSelector == nil || plan.ReplayAction.TargetSelector.Type != "table_cell_selector" || plan.ReplayAction.TargetSelector.TableID != "tbl_real_metrics" || plan.ReplayAction.TargetSelector.RowIndex != 1 || plan.ReplayAction.TargetSelector.CellIndex != 1 {
		t.Fatalf("expected replay selector to be canonicalized to table_cell_selector, got %+v", plan.ReplayAction.TargetSelector)
	}
}

func TestPlanActionReturnsNoopWhenReplaceAlreadyApplied(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:         "doc_replace_already_applied",
		Name:       "replace-already-applied.docx",
		Revision:   1,
		Paragraphs: []Paragraph{{ID: "p1", Runs: []Run{{ID: "r1", Text: "当前通过率为 92%"}}}},
	}
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "replace",
		TargetText: "当前通过率为 90%",
		NewText:    "当前通过率为 92%",
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "noop" {
		t.Fatalf("expected noop plan, got %+v", plan)
	}
	if plan.ActiveParagraphID != "p1" {
		t.Fatalf("expected noop active paragraph p1, got %+v", plan)
	}
}

func TestPlanActionResolvesTableCellCoordinatesFromSearchHit(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_table_search_resolve",
		Name:     "table-search-resolve.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_main",
				Rows: []TableRow{
					{
						ID: "row0",
						Cells: []TableCell{
							{ID: "cell00", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p00", Runs: []Run{{ID: "r00", Text: "设备"}}}}}},
							{ID: "cell01", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p01", Runs: []Run{{ID: "r01", Text: "数量"}}}}}},
						},
					},
					{
						ID: "row1",
						Cells: []TableCell{
							{ID: "cell10", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p10", Runs: []Run{{ID: "r10", Text: "内存"}}}}}},
							{ID: "cell11", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p11", Runs: []Run{{ID: "r11", Text: "4"}}}}}},
						},
					},
				},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "update_table_cell",
		TargetText: "4",
		TableCell:  &TableCellInput{Paragraphs: []ParagraphInput{{Text: "8"}}},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "ready" {
		t.Fatalf("expected ready plan, got %+v", plan)
	}
	if plan.ReplayAction.TargetSelector == nil || plan.ReplayAction.TargetSelector.Type != "table_cell_selector" {
		t.Fatalf("expected table_cell_selector, got %+v", plan.ReplayAction.TargetSelector)
	}
	if plan.ReplayAction.TargetSelector.TableID != "tbl_main" || plan.ReplayAction.TargetSelector.RowIndex != 1 || plan.ReplayAction.TargetSelector.CellIndex != 1 {
		t.Fatalf("expected resolved table cell (tbl_main,1,1), got %+v", plan.ReplayAction.TargetSelector)
	}
}

func TestPlanActionFormatSectionRejectsInsufficientScopeOnMultiSectionDocument(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_multi_section_scope_conflict",
		Name:     "multi-section-scope-conflict.docx",
		Revision: 1,
		Blocks:   []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "正文"}}}}},
		Meta: DocumentMeta{
			SectionLayout: SectionLayout{PageWidth: 11906, PageHeight: 16838, Orientation: "portrait", MarginLeft: 1800},
			Sections: []DocumentSection{
				{StartBlockIndex: 0, EndBlockIndex: 0, Layout: SectionLayout{PageWidth: 11906, PageHeight: 16838, Orientation: "portrait", MarginLeft: 1800}},
				{StartBlockIndex: 1, EndBlockIndex: 1, Layout: SectionLayout{PageWidth: 16838, PageHeight: 11906, Orientation: "landscape", MarginLeft: 1200}},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action: "format_section",
		SectionLayoutPatch: SectionLayout{
			MarginLeft: 1000,
		},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "INSUFFICIENT_SCOPE" {
		t.Fatalf("expected insufficient scope conflict, got %+v", plan)
	}
}

func TestPlanActionUpdateHeaderFooterRejectsConflictingSectionBindings(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_header_footer_conflict",
		Name:     "header-footer-conflict.docx",
		Revision: 1,
		Blocks:   []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "正文"}}}}},
		Meta: DocumentMeta{
			HeaderFooters: []HeaderFooterPart{{Kind: "header", Variant: "default", RelationshipID: "rIdHeader1", Target: "header1.xml", Paragraphs: []Paragraph{{ID: "hp1", Runs: []Run{{ID: "hr1", Text: "页眉一"}}}}}},
			Sections: []DocumentSection{
				{HeaderFooters: []HeaderFooterPart{{Kind: "header", Variant: "default", RelationshipID: "rIdHeader1", Target: "header1.xml", Paragraphs: []Paragraph{{ID: "hp1", Runs: []Run{{ID: "hr1", Text: "页眉一"}}}}}}},
				{HeaderFooters: []HeaderFooterPart{{Kind: "header", Variant: "default", RelationshipID: "rIdHeader2", Target: "header2.xml", Paragraphs: []Paragraph{{ID: "hp2", Runs: []Run{{ID: "hr2", Text: "页眉二"}}}}}}},
			},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{
		Action: "update_header_footer",
		HeaderFooter: &HeaderFooterInput{
			Kind:   "header",
			Blocks: []BlockInput{{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "新页眉"}}},
		},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "HEADER_FOOTER_CONFLICT" {
		t.Fatalf("expected header/footer conflict, got %+v", plan)
	}
}

func TestPlanActionInsertRejectsNumberingConflictWhenPreservingInheritedNumbering(t *testing.T) {
	service := NewService(t.TempDir())
	document := &Document{
		ID:       "doc_insert_numbering_conflict",
		Name:     "insert-numbering-conflict.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID:            "p1",
				NumberingText: "一、",
				Style:         ParagraphStyle{Numbering: &NumberingRef{NumID: "n1", Level: 0}},
				Runs:          []Run{{ID: "r1", Text: "现状分析"}},
			},
		}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	preserveNumberingTrue := true
	plan, err := service.PlanAction(session.ID, EditorAction{
		Action:     "insert",
		TargetText: "现状分析",
		NewText:    "新增正文",
		ParagraphStylePatch: ParagraphStyle{
			Numbering: &NumberingRef{NumID: "n2", Level: 1},
		},
		Constraints: &ActionConstraints{PreserveNumbering: &preserveNumberingTrue},
	})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "NUMBERING_CONFLICT" {
		t.Fatalf("expected numbering conflict, got %+v", plan)
	}
}

func TestPreflightCrossChapterScopeRejectsBestCandidateAcrossChapters(t *testing.T) {
	document := &Document{
		ID:       "doc_cross_chapter_scope",
		Name:     "cross-chapter.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "h1", Style: ParagraphStyle{StyleID: "Heading1"}, Runs: []Run{{ID: "r1", Text: "第一章 总则"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r2", Text: "适用范围"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "h2", Style: ParagraphStyle{StyleID: "Heading1"}, Runs: []Run{{ID: "r3", Text: "第二章 预算"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r4", Text: "适用范围"}}}},
		},
	}
	document.rebuildParagraphs()
	diagnostics := preflightCrossChapterScope(document, EditorAction{Action: "replace", TargetText: "适用范围", NewText: "适用边界", FailurePolicy: &ActionFailurePolicy{OnAmbiguous: "use_best_candidate"}})
	if diagnostics == nil || diagnostics.ErrorCode != "INSUFFICIENT_SCOPE" {
		t.Fatalf("expected insufficient scope conflict, got %+v", diagnostics)
	}
}

func TestPlanActionInsertRejectsComplexNumberingChain(t *testing.T) {
	service := NewService(t.TempDir())
	preserveNumberingTrue := true
	document := &Document{
		ID:       "doc_complex_numbering_chain",
		Name:     "complex-numbering.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p0", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "n0", Level: 0}}, NumberingText: "1.", Runs: []Run{{ID: "r0", Text: "上一项"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "n1", Level: 0}}, NumberingText: "一、", Runs: []Run{{ID: "r1", Text: "现状分析"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "n2", Level: 1}}, NumberingText: "(1)", Runs: []Run{{ID: "r2", Text: "子项"}}}},
		},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{Action: "insert", TargetText: "现状分析", NewText: "新增条目", Constraints: &ActionConstraints{PreserveNumbering: &preserveNumberingTrue}})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "NUMBERING_CONFLICT" {
		t.Fatalf("expected complex numbering conflict, got %+v", plan)
	}
}

func TestPlanActionRejectsCrossPageTableLayoutConflict(t *testing.T) {
	service := NewService(t.TempDir())
	rows := make([]TableRow, 0, 40)
	for index := 0; index < 40; index++ {
		rows = append(rows, TableRow{
			ID: nextID("row"),
			Cells: []TableCell{{
				ID: nextID("cell"),
				Blocks: []Block{{
					Type: BlockTypeParagraph,
					Paragraph: &Paragraph{
						ID:   nextID("para"),
						Runs: []Run{{ID: nextID("run"), Text: strings.Repeat("内容", 20)}},
					},
				}},
			}},
		})
	}
	document := &Document{
		ID:       "doc_cross_page_table",
		Name:     "cross-page-table.docx",
		Revision: 1,
		Blocks:   []Block{{Type: BlockTypeTable, Table: &Table{ID: "tbl_big", Rows: rows}}},
		Meta:     DocumentMeta{SectionLayout: SectionLayout{PageWidth: 11906, PageHeight: 6000, MarginTop: 720, MarginBottom: 720}},
	}
	document.rebuildParagraphs()
	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{Action: "delete_table_row", TableID: "tbl_big", RowIndex: 1})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.ErrorCode != "TABLE_LAYOUT_CONFLICT" {
		t.Fatalf("expected table layout conflict, got %+v", plan)
	}
}

func TestCreateDocumentSessionCanBuildDocumentFromScratchAndSave(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateDocumentSession(CreateDocumentRequest{
		Name: "created-from-scratch.docx",
		Blocks: []BlockInput{
			{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "项目方案", Style: ParagraphStyle{StyleID: "Title", Alignment: "center"}}},
			{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "这是正文段落。", Style: ParagraphStyle{SpacingAfter: 120}}},
			{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "第一项", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "1", Level: 0}}}},
			{Type: BlockTypeTable, Table: &TableInput{Rows: []TableRowInput{
				{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "项目"}}}, {Paragraphs: []ParagraphInput{{Text: "金额"}}}}},
				{Cells: []TableCellInput{{Paragraphs: []ParagraphInput{{Text: "服务器"}}}, {Paragraphs: []ParagraphInput{{Text: "1000"}}}}},
			}}},
		},
		HeaderFooters: []HeaderFooterInput{
			{Kind: "header", Blocks: []BlockInput{{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "Auramate 内部资料"}}}},
			{Kind: "footer", Blocks: []BlockInput{{Type: BlockTypeParagraph, Paragraph: &ParagraphInput{Text: "页脚说明"}}}},
		},
		SectionLayout: SectionLayout{Orientation: "landscape", MarginLeft: 1200, MarginRight: 1200},
		Numbering:     []NumberingDefinition{{NumID: "1", Levels: []NumberingLevel{{Level: 0, Start: 1, Format: "decimal", Text: "%1."}}}},
	})
	if err != nil {
		t.Fatalf("create document session: %v", err)
	}
	if session.Document == nil || len(session.Document.Blocks) < 4 {
		t.Fatalf("expected created document blocks, got %+v", session)
	}

	path := filepath.Join(t.TempDir(), "created-from-scratch.docx")
	saved, err := service.Save(session.ID, path)
	if err != nil {
		t.Fatalf("save created document: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen created document: %v", err)
	}
	if reopened.Meta.SectionLayout.Orientation != "landscape" {
		t.Fatalf("expected landscape layout, got %+v", reopened.Meta.SectionLayout)
	}
	if got := reopened.Paragraphs[0].PlainText(); got != "项目方案" {
		t.Fatalf("unexpected title paragraph: %q", got)
	}
	if len(reopened.Blocks) < 4 || reopened.Blocks[3].Type != BlockTypeTable {
		t.Fatalf("expected created table block, got %+v", reopened.Blocks)
	}
	assertZipEntryContains(t, saved.Path, "word/header-default.xml", "Auramate 内部资料")
	assertZipEntryContains(t, saved.Path, "word/footer-default.xml", "页脚说明")
}

func TestPlanActionFailureIncludesRetryProtocol(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_retry_protocol_plan",
		Name:     "retry-protocol-plan.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "通过率 90%"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "通过率 88%"}}}},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{Action: "replace", TargetText: "通过率", NewText: "92%"})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil {
		t.Fatalf("expected failed plan with diagnostics, got %+v", plan)
	}
	if plan.Diagnostics.RetryAction == nil || plan.Diagnostics.RetryAction.Action != "replace" {
		t.Fatalf("expected retry action in diagnostics, got %+v", plan.Diagnostics)
	}
	if !strings.Contains(plan.Diagnostics.RetryPrompt, "[WORD_AUTO_RELOCATE]") || !strings.Contains(plan.Diagnostics.RetryPrompt, "retry_action:") {
		t.Fatalf("expected retry prompt in diagnostics, got %q", plan.Diagnostics.RetryPrompt)
	}
}

func TestExecutePlannedActionFailureCarriesRetryProtocol(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_retry_protocol_execute",
		Name:     "retry-protocol-execute.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "通过率 90%"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "通过率 88%"}}}},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{Action: "replace", TargetText: "通过率", NewText: "92%"})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "failed" || result.Diagnostics == nil || result.Validation.Diagnostics == nil {
		t.Fatalf("expected failed execute result with diagnostics, got %+v", result)
	}
	if result.Diagnostics.RetryAction == nil || result.Validation.Diagnostics.RetryAction == nil {
		t.Fatalf("expected retry action on execute diagnostics, got %+v / %+v", result.Diagnostics, result.Validation.Diagnostics)
	}
	if !strings.Contains(result.Diagnostics.RetryPrompt, "WORD_AUTO_RELOCATE") || !strings.Contains(result.Validation.Diagnostics.RetryPrompt, "WORD_AUTO_RELOCATE") {
		t.Fatalf("expected retry prompt on execute result, got %+v", result.Diagnostics)
	}
}

func TestSaveDOCXRoundTripPreservesStylesNumberingAndRelationships(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_style_numbering_roundtrip",
		Name:     "style-numbering-roundtrip.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Style: ParagraphStyle{StyleID: "Heading1"}, Runs: []Run{{ID: "r1", Text: "项目方案", Style: TextStyle{StyleID: "AccentRun"}}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "42", Level: 0}}, Runs: []Run{{ID: "r2", Text: "第一项"}}}},
		},
		Meta: DocumentMeta{
			Format: "docx",
			Styles: []DocumentStyle{
				{ID: "BaseParagraph", Name: "Base Paragraph", Type: "paragraph", IsDefault: true, Paragraph: ParagraphStyle{SpacingAfter: 160}},
				{ID: "Heading1", Name: "Heading 1", Type: "paragraph", BasedOn: "BaseParagraph", Paragraph: ParagraphStyle{Alignment: "center"}},
				{ID: "AccentRun", Name: "Accent Run", Type: "character", Text: TextStyle{Bold: true, BoldSet: true, Color: "C0504D"}},
			},
			Numbering:             []NumberingDefinition{{NumID: "42", AbstractID: "42", Levels: []NumberingLevel{{Level: 0, Start: 1, Format: "decimal", Text: "%1."}}}},
			DefaultParagraphStyle: &ParagraphStyle{SpacingAfter: 200},
			DefaultTextStyle:      &TextStyle{FontFamily: "Calibri", FontSizeHP: 22},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "style-numbering-roundtrip.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.Meta.Styles) < 3 {
		t.Fatalf("expected styles to survive round-trip, got %+v", reopened.Meta.Styles)
	}
	if len(reopened.Meta.Numbering) != 1 || reopened.Meta.Numbering[0].NumID != "42" {
		t.Fatalf("expected numbering definition 42 to survive, got %+v", reopened.Meta.Numbering)
	}
	if reopened.Meta.DefaultParagraphStyle == nil || reopened.Meta.DefaultParagraphStyle.SpacingAfter != 200 {
		t.Fatalf("expected default paragraph style to survive, got %+v", reopened.Meta.DefaultParagraphStyle)
	}
	if reopened.Meta.DefaultTextStyle == nil || reopened.Meta.DefaultTextStyle.FontFamily != "Calibri" {
		t.Fatalf("expected default text style to survive, got %+v", reopened.Meta.DefaultTextStyle)
	}
	if reopened.Paragraphs[0].Style.StyleID != "Heading1" {
		t.Fatalf("expected heading style to survive, got %+v", reopened.Paragraphs[0].Style)
	}
	if reopened.Paragraphs[0].Runs[0].Style.StyleID != "AccentRun" {
		t.Fatalf("expected character style to survive, got %+v", reopened.Paragraphs[0].Runs[0].Style)
	}
	if reopened.Paragraphs[1].Style.Numbering == nil || reopened.Paragraphs[1].Style.Numbering.NumID != "42" {
		t.Fatalf("expected numbering ref to survive, got %+v", reopened.Paragraphs[1].Style)
	}
	assertZipEntryContains(t, saved.Path, "word/styles.xml", `w:styleId="Heading1"`)
	assertZipEntryContains(t, saved.Path, "word/styles.xml", `w:styleId="AccentRun"`)
	assertZipEntryContains(t, saved.Path, "word/numbering.xml", `w:numId="42"`)
	assertZipEntryContains(t, saved.Path, "word/_rels/document.xml.rels", `Target="styles.xml"`)
	assertZipEntryContains(t, saved.Path, "word/_rels/document.xml.rels", `Target="numbering.xml"`)
	assertZipEntryContains(t, saved.Path, "[Content_Types].xml", `/word/styles.xml`)
	assertZipEntryContains(t, saved.Path, "[Content_Types].xml", `/word/numbering.xml`)
}

func TestSaveDOCXRoundTripPreservesHeaderFooterVariantsAndTitlePage(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_header_footer_variants_roundtrip",
		Name:     "header-footer-variants-roundtrip.docx",
		Revision: 1,
		Blocks:   []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "body_p1", Runs: []Run{{ID: "body_r1", Text: "正文内容"}}}}},
		Meta: DocumentMeta{
			Format: "docx",
			SectionLayout: SectionLayout{
				TitlePage:    true,
				TitlePageSet: true,
			},
			HeaderFooters: []HeaderFooterPart{
				{Kind: "header", Variant: "default", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "h_default", Runs: []Run{{ID: "r_h_default", Text: "默认页眉"}}}}}},
				{Kind: "header", Variant: "first", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "h_first", Runs: []Run{{ID: "r_h_first", Text: "首页页眉"}}}}}},
				{Kind: "footer", Variant: "even", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "f_even", Runs: []Run{{ID: "r_f_even", Text: "偶数页页脚"}}}}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "header-footer-variants-roundtrip.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.Meta.SectionLayout.TitlePage || !reopened.Meta.SectionLayout.TitlePageSet {
		t.Fatalf("expected title page setting to survive, got %+v", reopened.Meta.SectionLayout)
	}
	parts := map[string]string{}
	for _, part := range reopened.Meta.HeaderFooters {
		text := ""
		if len(part.Blocks) > 0 && part.Blocks[0].Paragraph != nil {
			text = part.Blocks[0].Paragraph.PlainText()
		} else if len(part.Paragraphs) > 0 {
			text = part.Paragraphs[0].PlainText()
		}
		parts[part.Kind+":"+part.Variant] = text
	}
	if parts["header:default"] != "默认页眉" || parts["header:first"] != "首页页眉" || parts["footer:even"] != "偶数页页脚" {
		t.Fatalf("expected header/footer variants to survive, got %+v", parts)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:titlePg/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:headerReference w:type="first" r:id="rIdHeaderFirst"`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:footerReference w:type="even" r:id="rIdFooterEven"`)
	assertZipEntryContains(t, saved.Path, "word/header-first.xml", `首页页眉`)
	assertZipEntryContains(t, saved.Path, "word/footer-even.xml", `偶数页页脚`)
}

func TestSaveDOCXRoundTripPreservesExplicitTableGridWidths(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_table_grid_roundtrip",
		Name:     "table-grid-roundtrip.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID:      "tbl_grid",
				GridXML: `<w:gridCol w:w="1800"/><w:gridCol w:w="3600"/>`,
				Rows: []TableRow{{
					ID: "row_1",
					Cells: []TableCell{
						{ID: "cell_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_cell_1", Runs: []Run{{ID: "r_cell_1", Text: "A1"}}}}}},
						{ID: "cell_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_cell_2", Runs: []Run{{ID: "r_cell_2", Text: "A2"}}}}}},
					},
				}},
			},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "table-grid-roundtrip.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.Blocks) != 1 || reopened.Blocks[0].Table == nil {
		t.Fatalf("expected one table block after reopen, got %+v", reopened.Blocks)
	}
	if !strings.Contains(reopened.Blocks[0].Table.GridXML, `w:w="1800"`) || !strings.Contains(reopened.Blocks[0].Table.GridXML, `w:w="3600"`) {
		t.Fatalf("expected explicit grid widths to survive, got %q", reopened.Blocks[0].Table.GridXML)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:gridCol w:w="1800"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:gridCol w:w="3600"/>`)
}

func TestLayoutSnapshotBuildsSemanticPages(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_layout_stage1",
		Name:     "layout-stage1.docx",
		Revision: 3,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "第一段"}}}},
			{Type: BlockTypeTable, Table: &Table{ID: "t1", Rows: []TableRow{{
				ID: "tr1",
				Cells: []TableCell{{
					ID:     "tc1",
					Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "单元格内容"}}}}},
				}},
			}}}},
		},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	layout, err := service.LayoutSnapshot(session.ID, LayoutOptions{})
	if err != nil {
		t.Fatalf("layout snapshot: %v", err)
	}
	if layout.SessionID != session.ID {
		t.Fatalf("unexpected session id: %s", layout.SessionID)
	}
	if layout.Engine.Name != "semantic-layout" {
		t.Fatalf("unexpected engine: %+v", layout.Engine)
	}
	if len(layout.Pages) == 0 {
		t.Fatalf("expected at least one page")
	}
	if len(layout.Pages[0].Blocks) != 2 {
		t.Fatalf("expected 2 layout blocks, got %d", len(layout.Pages[0].Blocks))
	}
	if layout.Pages[0].Blocks[0].Paragraph == nil || layout.Pages[0].Blocks[1].Table == nil {
		t.Fatalf("unexpected block payload: %+v", layout.Pages[0].Blocks)
	}
	if got := layout.Pages[0].Blocks[0].Paragraph.Lines[0].Text; got != "第一段" {
		t.Fatalf("unexpected first line text: %s", got)
	}
	if got := layout.Pages[0].Blocks[1].Table.Rows[0].Cells[0].Paragraphs[0].Lines[0].Text; got != "单元格内容" {
		t.Fatalf("unexpected cell line text: %s", got)
	}
	if layout.PageSpec.WidthTwips == 0 || layout.PageSpec.HeightTwips == 0 {
		t.Fatalf("expected normalized page spec, got %+v", layout.PageSpec)
	}
}

func TestLayoutSnapshotSplitsCrossPageTableWithRepeatHeader(t *testing.T) {
	service := NewService(t.TempDir())
	rows := []TableRow{{
		ID:              "tr_header",
		RepeatHeader:    true,
		RepeatHeaderSet: true,
		CantSplit:       true,
		CantSplitSet:    true,
		Cells: []TableCell{{
			ID:     "tc_header_1",
			Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_header_1", Runs: []Run{{ID: "r_header_1", Text: "发布批次"}}}}},
		}, {
			ID:     "tc_header_2",
			Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_header_2", Runs: []Run{{ID: "r_header_2", Text: "要求"}}}}},
		}},
	}}
	for index := 1; index <= 6; index++ {
		rows = append(rows, TableRow{
			ID:           fmt.Sprintf("tr_%d", index),
			CantSplit:    true,
			CantSplitSet: true,
			Cells: []TableCell{{
				ID:     fmt.Sprintf("tc_%d_1", index),
				Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: fmt.Sprintf("p_%d_1", index), Runs: []Run{{ID: fmt.Sprintf("r_%d_1", index), Text: fmt.Sprintf("批次%d", index)}}}}},
			}, {
				ID:     fmt.Sprintf("tc_%d_2", index),
				Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: fmt.Sprintf("p_%d_2", index), Runs: []Run{{ID: fmt.Sprintf("r_%d_2", index), Text: fmt.Sprintf("要求%d", index)}}}}},
			}},
		})
	}
	session, err := service.CreateSession(&Document{
		ID:       "doc_layout_cross_page_table",
		Name:     "layout-cross-page-table.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p_intro", Runs: []Run{{ID: "r_intro", Text: "跨页表格测试"}}}},
			{Type: BlockTypeTable, Table: &Table{ID: "tbl_release_batches", Rows: rows}},
		},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	layout, err := service.LayoutSnapshot(session.ID, LayoutOptions{Page: LayoutPageSpec{
		HeightTwips:       3200,
		MarginTopTwips:    240,
		MarginBottomTwips: 240,
	}})
	if err != nil {
		t.Fatalf("layout snapshot: %v", err)
	}
	if len(layout.Pages) < 2 {
		t.Fatalf("expected cross-page table layout, got %d pages", len(layout.Pages))
	}
	if len(layout.Pages[0].Blocks) < 2 {
		t.Fatalf("expected first page to include intro paragraph and table chunk, got %+v", layout.Pages[0].Blocks)
	}
	firstPageTable := layout.Pages[0].Blocks[1].Table
	if firstPageTable == nil || firstPageTable.TableID != "tbl_release_batches" {
		t.Fatalf("expected first page table chunk, got %+v", layout.Pages[0].Blocks[1])
	}
	secondPageTable := layout.Pages[1].Blocks[0].Table
	if secondPageTable == nil || secondPageTable.TableID != "tbl_release_batches" {
		t.Fatalf("expected second page table continuation, got %+v", layout.Pages[1].Blocks)
	}
	if len(secondPageTable.Rows) < 2 {
		t.Fatalf("expected repeated header plus body rows on second page, got %+v", secondPageTable.Rows)
	}
	if !secondPageTable.Rows[0].RepeatHeader {
		t.Fatalf("expected second page first row to preserve repeat_header")
	}
	if got := secondPageTable.Rows[0].Cells[0].Paragraphs[0].Lines[0].Text; got != "发布批次" {
		t.Fatalf("unexpected repeated header text: %s", got)
	}
	if got := secondPageTable.Rows[1].Cells[0].Paragraphs[0].Lines[0].Text; got == "发布批次" {
		t.Fatalf("expected body row after repeated header, got repeated header again")
	}
}

func TestApplyUpdateSectionLayoutPersistsToSavedDOCX(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_section_layout",
		Name:     "section-layout.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "页面设置测试"}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type: OperationUpdateSectionLayout,
		SectionLayoutPatch: SectionLayout{
			Orientation:  "landscape",
			MarginLeft:   1200,
			MarginRight:  1200,
			MarginHeader: 480,
			MarginFooter: 480,
		},
	}})
	if err != nil {
		t.Fatalf("apply section layout: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "section-layout.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !strings.Contains(reopened.Meta.SectionXML, `w:orient="landscape"`) {
		t.Fatalf("expected landscape orientation, got %q", reopened.Meta.SectionXML)
	}
	if !strings.Contains(reopened.Meta.SectionXML, `w:left="1200"`) || !strings.Contains(reopened.Meta.SectionXML, `w:right="1200"`) {
		t.Fatalf("expected updated page margins, got %q", reopened.Meta.SectionXML)
	}
	if !strings.Contains(reopened.Meta.SectionXML, `w:header="480"`) || !strings.Contains(reopened.Meta.SectionXML, `w:footer="480"`) {
		t.Fatalf("expected updated header/footer margins, got %q", reopened.Meta.SectionXML)
	}
}

func TestSaveDOCXRoundTripPreservesPaginationSemantics(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_pagination_semantics",
		Name:     "pagination-semantics.docx",
		Revision: 1,
		Paragraphs: []Paragraph{
			{
				ID: "p1",
				Style: ParagraphStyle{
					StyleID:            "Heading1",
					PageBreakBefore:    true,
					PageBreakBeforeSet: true,
				},
				Runs: []Run{{ID: "r1", Text: "新章节"}},
			},
			{
				ID: "p2",
				Runs: []Run{
					{ID: "r2", Text: "第一页内容"},
					{ID: "r3", BreakType: RunBreakTypePage},
					{ID: "r4", Text: "第二页继续"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "pagination-semantics.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.Paragraphs[0].Style.PageBreakBefore {
		t.Fatalf("expected page_break_before to survive round-trip")
	}
	if len(reopened.Paragraphs[1].Runs) < 3 {
		t.Fatalf("expected break run to survive round-trip, got %+v", reopened.Paragraphs[1].Runs)
	}
	if reopened.Paragraphs[1].Runs[1].BreakType != RunBreakTypePage {
		t.Fatalf("expected second run to be page break, got %+v", reopened.Paragraphs[1].Runs[1])
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:pageBreakBefore/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:br w:type="page"/>`)
}

func TestOpenDOCXParsesLastRenderedPageBreak(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "last-rendered-page-break.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>第一页</w:t><w:lastRenderedPageBreak/><w:t>第二页</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	if len(opened.Paragraphs) != 1 || len(opened.Paragraphs[0].Runs) != 3 {
		t.Fatalf("expected text/break/text runs, got %+v", opened.Paragraphs)
	}
	if opened.Paragraphs[0].Runs[0].Text != "第一页" {
		t.Fatalf("expected first text before rendered page break, got %+v", opened.Paragraphs[0].Runs[0])
	}
	if opened.Paragraphs[0].Runs[1].BreakType != RunBreakTypeRenderedPage {
		t.Fatalf("expected rendered page break run, got %+v", opened.Paragraphs[0].Runs[1])
	}
	if opened.Paragraphs[0].Runs[2].Text != "第二页" {
		t.Fatalf("expected second text after rendered page break, got %+v", opened.Paragraphs[0].Runs[2])
	}
}

func TestSaveDOCXRoundTripPreservesLastRenderedPageBreak(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_rendered_page_break",
		Name:     "rendered-page-break.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID: "p1",
			Runs: []Run{
				{ID: "r1", Text: "第一页"},
				{ID: "r2", BreakType: RunBreakTypeRenderedPage},
				{ID: "r3", Text: "第二页"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "rendered-page-break.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.Paragraphs) != 1 || len(reopened.Paragraphs[0].Runs) != 3 {
		t.Fatalf("expected reopened text/break/text runs, got %+v", reopened.Paragraphs)
	}
	if reopened.Paragraphs[0].Runs[1].BreakType != RunBreakTypeRenderedPage {
		t.Fatalf("expected rendered page break after reopen, got %+v", reopened.Paragraphs[0].Runs[1])
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:lastRenderedPageBreak/>`)
}

func TestOpenDOCXParsesParagraphSectionBreak(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "paragraph-section-break.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:sectPr><w:type w:val="continuous"/><w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/></w:sectPr></w:pPr><w:r><w:t>第一节最后一段</w:t></w:r></w:p><w:p><w:r><w:t>第二节第一段</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	if len(opened.Paragraphs) < 1 || opened.Paragraphs[0].SectionBreakLayout == nil {
		t.Fatalf("expected paragraph section break metadata, got %+v", opened.Paragraphs)
	}
	if opened.Paragraphs[0].SectionBreakLayout.BreakType != "continuous" {
		t.Fatalf("expected continuous section break, got %+v", opened.Paragraphs[0].SectionBreakLayout)
	}
	if opened.Paragraphs[0].SectionBreakLayout.Orientation != "landscape" {
		t.Fatalf("expected paragraph section landscape layout, got %+v", opened.Paragraphs[0].SectionBreakLayout)
	}
	if opened.Paragraphs[0].SectionBreakLayout.PageWidth != 16838 || opened.Paragraphs[0].SectionBreakLayout.PageHeight != 11906 {
		t.Fatalf("expected paragraph section page size to parse, got %+v", opened.Paragraphs[0].SectionBreakLayout)
	}
}

func TestOpenDOCXBuildsDocumentSections(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "document-sections.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/word/header2.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId10" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/><Relationship Id="rId11" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header2.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId10"/><w:type w:val="nextPage"/><w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/></w:sectPr></w:pPr><w:r><w:t>第一节末段</w:t></w:r></w:p><w:p><w:r><w:t>第二节正文</w:t></w:r></w:p><w:sectPr><w:headerReference w:type="default" r:id="rId11"/><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
		"word/header1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>第一节页眉</w:t></w:r></w:p></w:hdr>`,
		"word/header2.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>第二节页眉</w:t></w:r></w:p></w:hdr>`,
	})

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	headerText := func(parts []HeaderFooterPart) string {
		if len(parts) == 0 || len(parts[0].Paragraphs) == 0 || len(parts[0].Paragraphs[0].Runs) == 0 {
			return ""
		}
		return parts[0].Paragraphs[0].Runs[0].Text
	}
	if len(opened.Meta.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %+v", opened.Meta.Sections)
	}
	if opened.Meta.Sections[0].StartBlockIndex != 0 || opened.Meta.Sections[0].EndBlockIndex != 0 {
		t.Fatalf("expected first section to cover first block, got %+v", opened.Meta.Sections[0])
	}
	if opened.Meta.Sections[0].Layout.Orientation != "landscape" {
		t.Fatalf("expected first section landscape layout, got %+v", opened.Meta.Sections[0].Layout)
	}
	if len(opened.Meta.Sections[0].HeaderFooters) != 1 || headerText(opened.Meta.Sections[0].HeaderFooters) != "第一节页眉" {
		t.Fatalf("expected first section header footer content, got %+v", opened.Meta.Sections[0].HeaderFooters)
	}
	if opened.Meta.Sections[1].StartBlockIndex != 1 || opened.Meta.Sections[1].EndBlockIndex != 1 {
		t.Fatalf("expected second section to cover second block, got %+v", opened.Meta.Sections[1])
	}
	if len(opened.Meta.Sections[1].HeaderFooters) != 1 || headerText(opened.Meta.Sections[1].HeaderFooters) != "第二节页眉" {
		t.Fatalf("expected second section header footer content, got %+v", opened.Meta.Sections[1].HeaderFooters)
	}
}

func TestSaveDOCXRoundTripPreservesParagraphSectionBreak(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "paragraph-section-break-roundtrip.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:sectPr><w:type w:val="nextPage"/><w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/></w:sectPr></w:pPr><w:r><w:t>第一节最后一段</w:t></w:r></w:p><w:p><w:r><w:t>第二节第一段</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	service := NewService(tempDir)
	session, err := service.CreateSession(opened)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	saved, err := service.Save(session.ID, filepath.Join(tempDir, "paragraph-section-break-roundtrip-saved.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Paragraphs[0].SectionBreakLayout == nil || reopened.Paragraphs[0].SectionBreakLayout.BreakType != "nextpage" {
		t.Fatalf("expected nextPage section break after reopen, got %+v", reopened.Paragraphs[0].SectionBreakLayout)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:sectPr>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:type w:val="nextPage"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:orient="landscape"`)
}

func TestSaveDOCXRoundTripPreservesParagraphPaginationControls(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_paragraph_pagination_controls",
		Name:     "paragraph-pagination-controls.docx",
		Revision: 1,
		Paragraphs: []Paragraph{
			{
				ID: "p1",
				Style: ParagraphStyle{
					KeepNext:        true,
					KeepNextSet:     true,
					WidowControl:    false,
					WidowControlSet: true,
				},
				Runs: []Run{{ID: "r1", Text: "段落一"}},
			},
			{
				ID: "p2",
				Style: ParagraphStyle{
					KeepLines:    true,
					KeepLinesSet: true,
				},
				Runs: []Run{{ID: "r2", Text: "段落二"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "paragraph-pagination-controls.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.Paragraphs[0].Style.KeepNextSet || !reopened.Paragraphs[0].Style.KeepNext {
		t.Fatalf("expected keep_next to survive round-trip, got %+v", reopened.Paragraphs[0].Style)
	}
	if !reopened.Paragraphs[0].Style.WidowControlSet || reopened.Paragraphs[0].Style.WidowControl {
		t.Fatalf("expected explicit widow_control=false to survive round-trip, got %+v", reopened.Paragraphs[0].Style)
	}
	if !reopened.Paragraphs[1].Style.KeepLinesSet || !reopened.Paragraphs[1].Style.KeepLines {
		t.Fatalf("expected keep_lines to survive round-trip, got %+v", reopened.Paragraphs[1].Style)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:keepNext/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:keepLines/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:widowControl w:val="0"/>`)
}

func TestOpenDOCXLoadsHeaderFooterParts(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "header-footer.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdHeaderDefault" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/><Relationship Id="rIdFooterDefault" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:t>正文</w:t></w:r></w:p><w:sectPr><w:headerReference w:type="default" r:id="rIdHeaderDefault"/><w:footerReference w:type="default" r:id="rIdFooterDefault"/><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`,
		"word/header1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>公司机密</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>第 1 页</w:t></w:r></w:p></w:ftr>`,
	})

	document, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	if len(document.Meta.HeaderFooters) != 2 {
		t.Fatalf("expected 2 header/footer parts, got %+v", document.Meta.HeaderFooters)
	}
	if document.Meta.HeaderFooters[0].Paragraphs[0].PlainText() != "公司机密" {
		t.Fatalf("unexpected header text: %+v", document.Meta.HeaderFooters[0].Paragraphs)
	}
	if document.Meta.HeaderFooters[1].Paragraphs[0].PlainText() != "第 1 页" {
		t.Fatalf("unexpected footer text: %+v", document.Meta.HeaderFooters[1].Paragraphs)
	}
}

func TestApplyUpdateHeaderFooterPersistsToSavedDOCX(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_header_footer",
		Name:     "header-footer-edit.docx",
		Revision: 1,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "正文内容"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type: OperationUpdateHeaderFooter,
		HeaderFooter: &HeaderFooterInput{
			Kind:    "header",
			Variant: "default",
			Blocks: []BlockInput{{
				Type:      BlockTypeParagraph,
				Paragraph: &ParagraphInput{Text: "Auramate 内部资料"},
			}},
		},
	}, {
		Type: OperationUpdateHeaderFooter,
		HeaderFooter: &HeaderFooterInput{
			Kind:    "footer",
			Variant: "default",
			Blocks: []BlockInput{{
				Type:      BlockTypeParagraph,
				Paragraph: &ParagraphInput{Text: "页脚说明"},
			}},
		},
	}})
	if err != nil {
		t.Fatalf("apply header/footer update: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "header-footer-edit.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.Meta.HeaderFooters) != 2 {
		t.Fatalf("expected 2 header/footer parts after reopen, got %+v", reopened.Meta.HeaderFooters)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:headerReference w:type="default" r:id="rIdHeaderDefault"`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:footerReference w:type="default" r:id="rIdFooterDefault"`)
	assertZipEntryContains(t, saved.Path, "word/header-default.xml", `Auramate 内部资料`)
	assertZipEntryContains(t, saved.Path, "word/footer-default.xml", `页脚说明`)
}

func TestSaveDOCXRoundTripPreservesStyles(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_style",
		Name:     "styled-report",
		Revision: 1,
		Paragraphs: []Paragraph{
			{
				ID: "p1",
				Style: ParagraphStyle{
					Alignment:       "center",
					SpacingBefore:   120,
					SpacingAfter:    240,
					FirstLineIndent: 420,
				},
				Runs: []Run{{
					ID:   "r1",
					Text: "  标题文本  ",
					Style: TextStyle{
						FontFamily: "Microsoft YaHei",
						FontSizeHP: 28,
						Bold:       true,
						Color:      "1F4E79",
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	firstSave, err := service.Save(session.ID, filepath.Join(tempDir, "styled-report.docx"))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	opened, err := OpenDOCX(firstSave.Path)
	if err != nil {
		t.Fatalf("open first save: %v", err)
	}

	if got := opened.Paragraphs[0].PlainText(); got != "  标题文本  " {
		t.Fatalf("unexpected round-trip text: %q", got)
	}
	if opened.Paragraphs[0].Style.Alignment != "center" {
		t.Fatalf("expected alignment center, got %q", opened.Paragraphs[0].Style.Alignment)
	}
	if !opened.Paragraphs[0].Runs[0].Style.Bold {
		t.Fatalf("expected bold style to survive round-trip")
	}
	if opened.Paragraphs[0].Runs[0].Style.FontFamily != "Microsoft YaHei" {
		t.Fatalf("unexpected font family: %q", opened.Paragraphs[0].Runs[0].Style.FontFamily)
	}

	reopenedSession, err := service.OpenDocument(firstSave.Path)
	if err != nil {
		t.Fatalf("open document into session: %v", err)
	}

	_, err = service.Apply(reopenedSession.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: reopenedSession.Document.Paragraphs[0].ID,
		StartOffset: 2,
		EndOffset:   6,
		Text:        "新标题",
	}})
	if err != nil {
		t.Fatalf("apply replace after reopen: %v", err)
	}

	secondSave, err := service.Save(reopenedSession.ID, "")
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	reopened, err := OpenDOCX(secondSave.Path)
	if err != nil {
		t.Fatalf("reopen second save: %v", err)
	}

	if got := reopened.Paragraphs[0].PlainText(); got != "  新标题  " {
		t.Fatalf("unexpected text after rewrite save: %q", got)
	}
}

func TestOpenDOCXModifyPreservesLineSpacingRule(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "line-spacing-rule.docx")
	createDOCXFromParts(t, docPath, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:spacing w:line="480" w:lineRule="exact"/></w:pPr><w:r><w:t>原始内容</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`,
	})

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open document: %v", err)
	}

	paragraph := session.Document.Paragraphs[0]
	if paragraph.Style.LineSpacing != 480 {
		t.Fatalf("expected line spacing 480, got %d", paragraph.Style.LineSpacing)
	}
	if paragraph.Style.LineSpacingRule != "exact" {
		t.Fatalf("expected line spacing rule exact, got %q", paragraph.Style.LineSpacingRule)
	}

	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: paragraph.ID,
		StartOffset: 0,
		EndOffset:   len([]rune(paragraph.PlainText())),
		Text:        "修改后内容",
	}})
	if err != nil {
		t.Fatalf("apply replace text: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "line-spacing-rule-edited.docx"))
	if err != nil {
		t.Fatalf("save edited docx: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen edited docx: %v", err)
	}

	if reopened.Paragraphs[0].Style.LineSpacing != 480 {
		t.Fatalf("expected reopened line spacing 480, got %d", reopened.Paragraphs[0].Style.LineSpacing)
	}
	if reopened.Paragraphs[0].Style.LineSpacingRule != "exact" {
		t.Fatalf("expected reopened line spacing rule exact, got %q", reopened.Paragraphs[0].Style.LineSpacingRule)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `w:spacing w:line="480" w:lineRule="exact"`)
}

func TestSaveDOCXRoundTripPreservesStylesXMLAndNumberingXML(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_meta",
		Name:     "style-numbering.docx",
		Revision: 1,
		Meta: DocumentMeta{
			Format: "docx",
			Styles: []DocumentStyle{
				{
					ID:   "TitleStyle",
					Name: "Title Style",
					Type: "paragraph",
					Paragraph: ParagraphStyle{
						Alignment: "center",
					},
					Text: TextStyle{
						FontFamily: "Calibri",
						FontSizeHP: 32,
						Bold:       true,
					},
				},
				{
					ID:   "AccentChar",
					Name: "Accent Char",
					Type: "character",
					Text: TextStyle{
						Color:  "C00000",
						Italic: true,
					},
				},
			},
			Numbering: []NumberingDefinition{{
				NumID:      "7",
				AbstractID: "42",
				Levels: []NumberingLevel{{
					Level:  0,
					Start:  1,
					Format: "decimal",
					Text:   "%1.",
					Paragraph: ParagraphStyle{
						LeftIndent:      720,
						FirstLineIndent: -360,
					},
					TextStyle: TextStyle{Bold: true},
				}},
			}},
		},
		Paragraphs: []Paragraph{
			{
				ID:    "p1",
				Style: ParagraphStyle{StyleID: "TitleStyle"},
				Runs:  []Run{{ID: "r1", Text: "标题", Style: TextStyle{StyleID: "AccentChar"}}},
			},
			{
				ID:    "p2",
				Style: ParagraphStyle{Numbering: &NumberingRef{NumID: "7", Level: 0}},
				Runs:  []Run{{ID: "r2", Text: "第一项"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "style-numbering.docx"))
	if err != nil {
		t.Fatalf("save docx with styles/numbering: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen saved docx: %v", err)
	}

	if reopened.Paragraphs[0].Style.StyleID != "TitleStyle" {
		t.Fatalf("expected paragraph style id TitleStyle, got %q", reopened.Paragraphs[0].Style.StyleID)
	}
	if reopened.Paragraphs[0].Runs[0].Style.StyleID != "AccentChar" {
		t.Fatalf("expected run style id AccentChar, got %q", reopened.Paragraphs[0].Runs[0].Style.StyleID)
	}
	if reopened.Paragraphs[1].Style.Numbering == nil || reopened.Paragraphs[1].Style.Numbering.NumID != "7" || reopened.Paragraphs[1].Style.Numbering.Level != 0 {
		t.Fatalf("expected numbering ref 7/0, got %+v", reopened.Paragraphs[1].Style.Numbering)
	}
	if len(reopened.Meta.Styles) != 2 {
		t.Fatalf("expected 2 styles, got %d", len(reopened.Meta.Styles))
	}
	if len(reopened.Meta.Numbering) != 1 {
		t.Fatalf("expected 1 numbering definition, got %d", len(reopened.Meta.Numbering))
	}
	if reopened.Meta.Numbering[0].Levels[0].Format != "decimal" {
		t.Fatalf("expected decimal numbering format, got %q", reopened.Meta.Numbering[0].Levels[0].Format)
	}
}

func TestOpenDOCXResolvesStyleInheritanceAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)

	session, err := service.CreateSession(&Document{
		ID:       "doc_inherit",
		Name:     "inheritance.docx",
		Revision: 1,
		Meta: DocumentMeta{
			Format: "docx",
			DefaultParagraphStyle: &ParagraphStyle{
				SpacingAfter: 160,
			},
			DefaultTextStyle: &TextStyle{
				FontFamily: "Calibri",
				FontSizeHP: 22,
			},
			Styles: []DocumentStyle{
				{
					ID:        "BaseParagraph",
					Name:      "Base Paragraph",
					Type:      "paragraph",
					IsDefault: true,
					Paragraph: ParagraphStyle{
						Alignment:       "center",
						FirstLineIndent: 420,
					},
					Text: TextStyle{
						FontFamily: "Cambria",
						Bold:       true,
					},
				},
				{
					ID:      "Heading1",
					Name:    "Heading 1",
					Type:    "paragraph",
					BasedOn: "BaseParagraph",
					Paragraph: ParagraphStyle{
						SpacingBefore: 240,
					},
					Text: TextStyle{
						FontSizeHP: 32,
					},
				},
				{
					ID:   "Emphasis",
					Name: "Emphasis",
					Type: "character",
					Text: TextStyle{
						Italic: true,
						Color:  "C00000",
					},
				},
			},
		},
		Paragraphs: []Paragraph{{
			ID: "p1",
			Style: ParagraphStyle{
				StyleID: "Heading1",
			},
			Runs: []Run{{
				ID:   "r1",
				Text: "继承样式",
				Style: TextStyle{
					StyleID: "Emphasis",
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "inheritance.docx"))
	if err != nil {
		t.Fatalf("save inheritance docx: %v", err)
	}

	opened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("open inheritance docx: %v", err)
	}

	paragraph := opened.Paragraphs[0]
	if paragraph.Style.StyleID != "Heading1" {
		t.Fatalf("expected paragraph style Heading1, got %q", paragraph.Style.StyleID)
	}
	if paragraph.Style.Alignment != "center" {
		t.Fatalf("expected inherited alignment center, got %q", paragraph.Style.Alignment)
	}
	if paragraph.Style.FirstLineIndent != 420 {
		t.Fatalf("expected inherited first line indent 420, got %d", paragraph.Style.FirstLineIndent)
	}
	if paragraph.Style.SpacingBefore != 240 {
		t.Fatalf("expected direct spacing before 240, got %d", paragraph.Style.SpacingBefore)
	}
	if paragraph.Style.SpacingAfter != 160 {
		t.Fatalf("expected default spacing after 160, got %d", paragraph.Style.SpacingAfter)
	}

	run := paragraph.Runs[0]
	if run.Style.StyleID != "Emphasis" {
		t.Fatalf("expected run style Emphasis, got %q", run.Style.StyleID)
	}
	if run.Style.FontFamily != "Cambria" {
		t.Fatalf("expected inherited run font Cambria, got %q", run.Style.FontFamily)
	}
	if run.Style.FontSizeHP != 32 {
		t.Fatalf("expected inherited run font size 32, got %d", run.Style.FontSizeHP)
	}
	if !run.Style.Bold {
		t.Fatalf("expected inherited bold from paragraph style")
	}
	if !run.Style.Italic {
		t.Fatalf("expected run italic from character style")
	}
	if run.Style.Color != "C00000" {
		t.Fatalf("expected run color C00000, got %q", run.Style.Color)
	}

	if opened.Meta.DefaultParagraphStyle == nil || opened.Meta.DefaultParagraphStyle.SpacingAfter != 160 {
		t.Fatalf("expected default paragraph style to survive round-trip")
	}
	if opened.Meta.DefaultTextStyle == nil || opened.Meta.DefaultTextStyle.FontFamily != "Calibri" {
		t.Fatalf("expected default text style to survive round-trip")
	}
	if opened.Meta.Styles[1].BasedOn != "BaseParagraph" {
		t.Fatalf("expected Heading1 basedOn BaseParagraph, got %q", opened.Meta.Styles[1].BasedOn)
	}
}

func TestOpenDOCXResolvesThemeMappingAndExplicitOffState(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "theme-explicit-off.docx")
	parts := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:rPr><w:rStyle w:val="ThemeRun"/></w:rPr><w:t>主题样式</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`,
		"word/styles.xml":              `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:b/><w:rFonts w:asciiTheme="minorHAnsi" w:eastAsiaTheme="minorEastAsia"/><w:color w:themeColor="accent2"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="character" w:styleId="BaseChar"><w:name w:val="Base Char"/><w:rPr><w:b/><w:i/><w:color w:themeColor="accent1"/><w:rFonts w:asciiTheme="majorHAnsi" w:eastAsiaTheme="majorEastAsia"/></w:rPr></w:style><w:style w:type="character" w:styleId="ThemeRun"><w:name w:val="Theme Run"/><w:basedOn w:val="BaseChar"/><w:rPr><w:b w:val="0"/><w:u w:val="none"/></w:rPr></w:style></w:styles>`,
		"word/theme/theme1.xml":        `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme"><a:themeElements><a:clrScheme name="Office"><a:dk1><a:srgbClr val="000000"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2></a:clrScheme><a:fontScheme name="Office"><a:majorFont><a:latin typeface="Aptos Display"/><a:ea typeface="等线"/><a:cs typeface="Times New Roman"/></a:majorFont><a:minorFont><a:latin typeface="Aptos"/><a:ea typeface="等线"/><a:cs typeface="Arial"/></a:minorFont></a:fontScheme></a:themeElements></a:theme>`,
	}
	createDOCXFromParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open theme docx: %v", err)
	}

	if opened.Meta.Theme == nil {
		t.Fatalf("expected parsed theme metadata")
	}
	run := opened.Paragraphs[0].Runs[0]
	if run.Style.StyleID != "ThemeRun" {
		t.Fatalf("expected run style ThemeRun, got %q", run.Style.StyleID)
	}
	if run.Style.Bold {
		t.Fatalf("expected explicit off state to disable inherited bold")
	}
	if !run.Style.BoldSet {
		t.Fatalf("expected bold set flag for explicit off state")
	}
	if run.Style.Underline {
		t.Fatalf("expected underline none to disable underline")
	}
	if !run.Style.UnderlineSet {
		t.Fatalf("expected underline set flag for explicit off state")
	}
	if !run.Style.Italic {
		t.Fatalf("expected inherited italic from base character style")
	}
	if run.Style.ThemeColor != "accent1" {
		t.Fatalf("expected theme color accent1, got %q", run.Style.ThemeColor)
	}
	if run.Style.Color != "4472C4" {
		t.Fatalf("expected resolved theme color 4472C4, got %q", run.Style.Color)
	}
	if run.Style.ThemeFontASCII != "majorHAnsi" {
		t.Fatalf("expected theme font ascii majorHAnsi, got %q", run.Style.ThemeFontASCII)
	}
	if run.Style.ThemeFontEastAsia != "majorEastAsia" {
		t.Fatalf("expected theme font eastAsia majorEastAsia, got %q", run.Style.ThemeFontEastAsia)
	}
	if run.Style.FontFamily != "等线" {
		t.Fatalf("expected resolved theme eastAsia font 等线, got %q", run.Style.FontFamily)
	}
	if opened.Meta.DefaultTextStyle == nil || opened.Meta.DefaultTextStyle.ThemeColor != "accent2" {
		t.Fatalf("expected default text style theme color accent2")
	}
	if opened.Meta.Theme.Colors["accent1"] != "4472C4" {
		t.Fatalf("expected parsed theme accent1 color 4472C4, got %q", opened.Meta.Theme.Colors["accent1"])
	}
	if opened.Meta.Theme.Fonts["majorEastAsia"] != "等线" {
		t.Fatalf("expected parsed theme majorEastAsia font 等线, got %q", opened.Meta.Theme.Fonts["majorEastAsia"])
	}
}

func TestOpenDOCXParsesTablesAndEditsCellParagraphs(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "table-structure.docx")
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>表格前文本</w:t></w:r></w:p><w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid><w:tr><w:trPr><w:trHeight w:val="320"/></w:trPr><w:tc><w:tcPr><w:tcW w:w="2500" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>A1</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:tcW w:w="2500" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>A2</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:tcPr><w:tcW w:w="2500" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>B1</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:tcW w:w="2500" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>B2</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:p><w:r><w:t>表格后文本</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`,
	}
	createDOCXFromParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open table docx: %v", err)
	}

	if len(opened.Blocks) != 3 {
		t.Fatalf("expected 3 top-level blocks, got %d", len(opened.Blocks))
	}
	tableBlock := opened.Blocks[1]
	if tableBlock.Type != BlockTypeTable || tableBlock.Table == nil {
		t.Fatalf("expected middle block to be table, got %+v", tableBlock.Type)
	}
	if len(tableBlock.Table.Rows) != 2 {
		t.Fatalf("expected 2 table rows, got %d", len(tableBlock.Table.Rows))
	}
	if tableBlock.Table.GridXML == "" || tableBlock.Table.PropertiesXML == "" {
		t.Fatalf("expected table properties and grid to be preserved")
	}
	if tableBlock.Table.Rows[0].PropertiesXML == "" {
		t.Fatalf("expected row properties to be preserved")
	}
	if tableBlock.Table.Rows[0].Cells[0].PropertiesXML == "" {
		t.Fatalf("expected cell properties to be preserved")
	}
	if got := opened.Paragraphs[1].PlainText(); got != "A1" {
		t.Fatalf("expected flattened first cell paragraph A1, got %q", got)
	}

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open table document into session: %v", err)
	}
	cellParagraph := session.Document.Blocks[1].Table.Rows[0].Cells[0].Blocks[0].Paragraph
	if cellParagraph == nil {
		t.Fatalf("expected first table cell paragraph")
	}

	_, err = service.Apply(session.ID, []Operation{
		{
			Type:        OperationReplaceText,
			ParagraphID: cellParagraph.ID,
			StartOffset: 0,
			EndOffset:   2,
			Text:        "单元格一",
		},
		{
			Type:     OperationInsertParagraph,
			AnchorID: cellParagraph.ID,
			Position: "after",
			Paragraph: &ParagraphInput{
				Text: "追加说明",
			},
		},
	})
	if err != nil {
		t.Fatalf("apply cell edits: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "table-structure-edited.docx"))
	if err != nil {
		t.Fatalf("save edited table docx: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen edited table docx: %v", err)
	}
	if len(reopened.Blocks) != 3 {
		t.Fatalf("expected table structure to survive save, got %d blocks", len(reopened.Blocks))
	}
	reopenedTable := reopened.Blocks[1].Table
	if reopenedTable == nil {
		t.Fatalf("expected reopened middle block table")
	}
	if got := reopenedTable.Rows[0].Cells[0].Blocks[0].Paragraph.PlainText(); got != "单元格一" {
		t.Fatalf("expected edited cell text to survive, got %q", got)
	}
	if len(reopenedTable.Rows[0].Cells[0].Blocks) != 2 {
		t.Fatalf("expected inserted paragraph to stay in same cell, got %d blocks", len(reopenedTable.Rows[0].Cells[0].Blocks))
	}
	if got := reopenedTable.Rows[0].Cells[0].Blocks[1].Paragraph.PlainText(); got != "追加说明" {
		t.Fatalf("expected inserted cell paragraph to survive, got %q", got)
	}
}

func TestOpenDOCXParsesTableRowPaginationProperties(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "table-row-pagination.docx")
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid><w:tr><w:trPr><w:tblHeader/><w:cantSplit/></w:trPr><w:tc><w:p><w:r><w:t>表头1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>表头2</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:trPr><w:cantSplit w:val="0"/></w:trPr><w:tc><w:p><w:r><w:t>数据1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>数据2</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	}
	createDOCXFromParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open table row pagination docx: %v", err)
	}
	if len(opened.Blocks) != 1 || opened.Blocks[0].Table == nil {
		t.Fatalf("expected one top-level table block, got %+v", opened.Blocks)
	}
	rows := opened.Blocks[0].Table.Rows
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].RepeatHeaderSet || !rows[0].RepeatHeader || !rows[0].CantSplitSet || !rows[0].CantSplit {
		t.Fatalf("expected first row pagination properties, got %+v", rows[0])
	}
	if !rows[1].CantSplitSet || rows[1].CantSplit {
		t.Fatalf("expected explicit cant_split=false on second row, got %+v", rows[1])
	}
}

func TestSaveDOCXRoundTripPreservesTableRowPaginationProperties(t *testing.T) {
	tempDir := t.TempDir()
	service := NewService(tempDir)
	session, err := service.CreateSession(&Document{
		ID:       "doc_table_row_pagination",
		Name:     "table-row-pagination.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeTable,
			Table: &Table{
				ID: "tbl_1",
				Rows: []TableRow{
					{
						ID:              "row_1",
						RepeatHeader:    true,
						RepeatHeaderSet: true,
						CantSplit:       true,
						CantSplitSet:    true,
						Cells:           []TableCell{{ID: "cell_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "表头"}}}}}}},
					},
					{
						ID:           "row_2",
						CantSplit:    false,
						CantSplitSet: true,
						Cells:        []TableCell{{ID: "cell_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "数据"}}}}}}},
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "table-row-pagination.docx"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rows := reopened.Blocks[0].Table.Rows
	if !rows[0].RepeatHeaderSet || !rows[0].RepeatHeader || !rows[0].CantSplitSet || !rows[0].CantSplit {
		t.Fatalf("expected first row pagination properties after reopen, got %+v", rows[0])
	}
	if !rows[1].CantSplitSet || rows[1].CantSplit {
		t.Fatalf("expected explicit cant_split=false after reopen, got %+v", rows[1])
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:tblHeader/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:cantSplit/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:cantSplit w:val="0"/>`)
}

func TestOpenDOCXPreservesInlineImagesDuringTextEdit(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-inline.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:t>图片前</w:t></w:r><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><wp:extent cx="914400" cy="457200"/><wp:docPr id="1" name="回归图片"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r><w:r><w:t>图片后</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open image docx: %v", err)
	}
	paragraph := opened.Paragraphs[0]
	if len(paragraph.Runs) != 3 {
		t.Fatalf("expected 3 runs including image, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[1].Image == nil {
		t.Fatalf("expected middle run to be parsed as image")
	}
	if paragraph.Runs[1].Image.RelationshipID != "rIdImage1" {
		t.Fatalf("expected image relationship rIdImage1, got %q", paragraph.Runs[1].Image.RelationshipID)
	}
	if paragraph.Runs[1].Image.Target != "media/image1.png" {
		t.Fatalf("expected image target media/image1.png, got %q", paragraph.Runs[1].Image.Target)
	}

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open image document into session: %v", err)
	}
	_, err = service.Apply(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: session.Document.Paragraphs[0].ID,
		StartOffset: 0,
		EndOffset:   len([]rune("图片前")),
		Text:        "已编辑前文",
	}})
	if err != nil {
		t.Fatalf("apply text edit with image: %v", err)
	}

	saved, err := service.Save(session.ID, filepath.Join(tempDir, "image-inline-edited.docx"))
	if err != nil {
		t.Fatalf("save edited image docx: %v", err)
	}

	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen edited image docx: %v", err)
	}
	reopenedParagraph := reopened.Paragraphs[0]
	if reopenedParagraph.PlainText() != "已编辑前文图片后" {
		t.Fatalf("expected edited visible text to survive, got %q", reopenedParagraph.PlainText())
	}
	if len(reopenedParagraph.Runs) != 3 {
		t.Fatalf("expected edited paragraph to keep image run, got %d runs", len(reopenedParagraph.Runs))
	}
	if reopenedParagraph.Runs[1].Image == nil {
		t.Fatalf("expected image run to survive save")
	}
	assertZipEntryEquals(t, saved.Path, "word/media/image1.png", minimalPNG())
	assertZipEntryContains(t, saved.Path, "word/_rels/document.xml.rels", `Target="media/image1.png"`)
}

func TestOpenDOCXParsesLinkedDrawingImage(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-linked.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><wp:docPr id="1" name="链接图片"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill><a:blip r:link="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open linked image docx: %v", err)
	}
	paragraph := opened.Paragraphs[0]
	if len(paragraph.Runs) != 1 {
		t.Fatalf("expected 1 linked image run, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[0].Image == nil {
		t.Fatalf("expected linked drawing to be parsed as image")
	}
	if paragraph.Runs[0].Image.RelationshipID != "rIdImage1" {
		t.Fatalf("expected linked image relationship rIdImage1, got %q", paragraph.Runs[0].Image.RelationshipID)
	}
	if paragraph.Runs[0].Image.Target != "media/image1.png" {
		t.Fatalf("expected linked image target media/image1.png, got %q", paragraph.Runs[0].Image.Target)
	}
}

func TestOpenDOCXParsesLegacyPictImage(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-legacy-pict.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><w:body><w:p><w:r><w:pict><v:shape type="#_x0000_t75"><v:imagedata r:id="rIdImage1" o:title="旧式图片"/></v:shape></w:pict></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open legacy pict image docx: %v", err)
	}
	paragraph := opened.Paragraphs[0]
	if len(paragraph.Runs) != 1 {
		t.Fatalf("expected 1 legacy image run, got %d", len(paragraph.Runs))
	}
	if paragraph.Runs[0].Image == nil {
		t.Fatalf("expected legacy pict to be parsed as image")
	}
	if paragraph.Runs[0].Image.RelationshipID != "rIdImage1" {
		t.Fatalf("expected legacy image relationship rIdImage1, got %q", paragraph.Runs[0].Image.RelationshipID)
	}
	if paragraph.Runs[0].Image.Target != "media/image1.png" {
		t.Fatalf("expected legacy image target media/image1.png, got %q", paragraph.Runs[0].Image.Target)
	}
	if paragraph.Runs[0].Image.Name != "旧式图片" {
		t.Fatalf("expected legacy image title to be parsed, got %q", paragraph.Runs[0].Image.Name)
	}
}

func TestOpenDOCXBuildsOOXMLObjectSummaryForComplexStructures(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "object-summary-complex.docx")
	parts := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/footnotes.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"/><Override PartName="/word/endnotes.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"/><Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdHyper1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com" TargetMode="External"/><Relationship Id="rIdFootnotes" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footnotes" Target="footnotes.xml"/><Relationship Id="rIdEndnotes" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/endnotes" Target="endnotes.xml"/><Relationship Id="rIdComments" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:hyperlink r:id="rIdHyper1"><w:r><w:t>链接</w:t></w:r></w:hyperlink><w:bookmarkStart w:id="0" w:name="bm1"/><w:r><w:t>书签文本</w:t></w:r><w:bookmarkEnd w:id="0"/></w:p><w:p><w:fldSimple w:instr="DATE"><w:r><w:t>2026-01-01</w:t></w:r></w:fldSimple></w:p><w:p><w:r><w:fldChar w:fldCharType="begin"/><w:instrText xml:space="preserve"> REF bm1 </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>书签引用</w:t><w:fldChar w:fldCharType="end"/></w:r></w:p><w:p><w:r><w:t>脚注</w:t><w:footnoteReference w:id="2"/></w:r><w:r><w:t>尾注</w:t><w:endnoteReference w:id="2"/></w:r><w:commentRangeStart w:id="0"/><w:r><w:t>批注锚点</w:t><w:commentReference w:id="0"/></w:r><w:commentRangeEnd w:id="0"/></w:p><w:p><w:ins w:id="1" w:author="tester"><w:r><w:t>新增文本</w:t></w:r></w:ins><w:del w:id="2" w:author="tester"><w:r><w:delText>删除文本</w:delText></w:r></w:del></w:p><w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>跨列标题</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>外层单元格</w:t></w:r></w:p><w:tbl><w:tblPr><w:tblW w:w="3000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="3000"/></w:tblGrid><w:tr><w:tc><w:p><w:r><w:t>内层表格</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc><w:tc><w:p><w:r><w:t>右侧单元格</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
		"word/footnotes.xml":           `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote><w:footnote w:id="2"><w:p><w:r><w:t>脚注内容</w:t></w:r></w:p></w:footnote></w:footnotes>`,
		"word/endnotes.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:endnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:endnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:endnote><w:endnote w:id="2"><w:p><w:r><w:t>尾注内容</w:t></w:r></w:p></w:endnote></w:endnotes>`,
		"word/comments.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:comment w:id="0" w:author="tester"><w:p><w:r><w:t>批注内容</w:t></w:r></w:p></w:comment></w:comments>`,
	}
	createDOCXFromParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open object summary docx: %v", err)
	}
	if opened.Meta.ObjectSummary == nil {
		t.Fatalf("expected object summary to be populated")
	}
	summary := opened.Meta.ObjectSummary
	if summary.ComplexTables != 1 || summary.MergedTableCells != 1 || summary.NestedTables != 1 {
		t.Fatalf("unexpected complex table summary: %+v", *summary)
	}
	if summary.Hyperlinks != 1 || summary.Bookmarks != 1 || summary.SimpleFields != 1 || summary.ComplexFields != 1 {
		t.Fatalf("unexpected field summary: %+v", *summary)
	}
	if summary.FootnoteReferences != 1 || summary.EndnoteReferences != 1 || summary.CommentReferences != 1 {
		t.Fatalf("unexpected review summary: %+v", *summary)
	}
	if summary.Insertions != 1 || summary.Deletions != 1 {
		t.Fatalf("unexpected revision summary: %+v", *summary)
	}

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open object summary docx into session: %v", err)
	}
	saved, err := service.Save(session.ID, filepath.Join(tempDir, "object-summary-complex-saved.docx"))
	if err != nil {
		t.Fatalf("save object summary docx: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen object summary docx: %v", err)
	}
	if reopened.Meta.ObjectSummary == nil || *reopened.Meta.ObjectSummary != *summary {
		t.Fatalf("expected object summary to survive save/reopen, got before=%+v after=%+v", summary, reopened.Meta.ObjectSummary)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:gridSpan w:val="2"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:hyperlink r:id="rIdHyper1">`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:fldSimple w:instr="DATE">`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:footnoteReference w:id="2"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:endnoteReference w:id="2"/>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:commentReference w:id="0"/>`)
	assertZipEntryContains(t, saved.Path, "word/footnotes.xml", `<w:footnote w:id="2">`)
	assertZipEntryContains(t, saved.Path, "word/endnotes.xml", `<w:endnote w:id="2">`)
	assertZipEntryContains(t, saved.Path, "word/comments.xml", `<w:comment w:id="0"`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:ins w:id="1"`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:del w:id="2"`)
}

func TestOpenDOCXBuildsOOXMLObjectSummaryForImageVariants(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "object-summary-images.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/><Relationship Id="rIdImage2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><w:body><w:p><w:r><w:drawing><wp:inline><wp:docPr id="1" name="内联图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="2" name="浮动图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:link="rIdImage2"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r><w:r><w:pict><v:shape type="#_x0000_t75"><v:imagedata r:id="rIdImage1" o:title="旧式图片"/></v:shape></w:pict></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="3" name="文本框形状"/><a:graphic><a:graphicData uri="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"><wps:wsp><wps:txbx><w:txbxContent><w:p><w:r><w:t>文本框内容</w:t></w:r></w:p></w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
		"word/media/image2.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	opened, err := OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open image summary docx: %v", err)
	}
	if opened.Meta.ObjectSummary == nil {
		t.Fatalf("expected image object summary to be populated")
	}
	summary := opened.Meta.ObjectSummary
	if summary.InlineImages != 1 || summary.AnchoredImages != 2 || summary.LegacyImages != 1 || summary.LinkedImages != 1 || summary.TextBoxes != 1 || summary.Shapes != 1 {
		t.Fatalf("unexpected image summary: %+v", *summary)
	}

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open image summary docx into session: %v", err)
	}
	saved, err := service.Save(session.ID, filepath.Join(tempDir, "object-summary-images-saved.docx"))
	if err != nil {
		t.Fatalf("save image summary docx: %v", err)
	}
	reopened, err := OpenDOCX(saved.Path)
	if err != nil {
		t.Fatalf("reopen image summary docx: %v", err)
	}
	if reopened.Meta.ObjectSummary == nil || *reopened.Meta.ObjectSummary != *summary {
		t.Fatalf("expected image summary to survive save/reopen, got before=%+v after=%+v", summary, reopened.Meta.ObjectSummary)
	}
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<wp:inline>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<wp:anchor>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:pict>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<wps:wsp>`)
	assertZipEntryContains(t, saved.Path, "word/document.xml", `<w:txbxContent>`)
	assertZipEntryContains(t, saved.Path, "word/_rels/document.xml.rels", `Target="media/image2.png"`)
}

func TestServiceReadImageByRelationshipID(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-read.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open image docx into session: %v", err)
	}

	content, contentType, err := service.ReadImage(session.ID, "rIdImage1", "")
	if err != nil {
		t.Fatalf("ReadImage by relationship id: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	if !bytes.Equal(content, minimalPNG()) {
		t.Fatalf("unexpected image bytes")
	}
}

func TestServiceReadImageFallsBackToTargetWhenRelationshipLookupMisses(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-read-target-fallback.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>无正文图片引用</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNG(),
	}
	createDOCXFromBinaryParts(t, docPath, parts)

	service := NewService(tempDir)
	session, err := service.OpenDocument(docPath)
	if err != nil {
		t.Fatalf("open image docx into session: %v", err)
	}

	content, contentType, err := service.ReadImage(session.ID, "missingRelationship", "media/image1.png")
	if err != nil {
		t.Fatalf("ReadImage with target fallback: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	if !bytes.Equal(content, minimalPNG()) {
		t.Fatalf("unexpected image bytes")
	}
}

func TestServiceSearchSupportsLineParagraphAndTable(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_search",
		Name:     "search.docx",
		Revision: 1,
		Blocks: []Block{
			{
				Type: BlockTypeParagraph,
				Paragraph: &Paragraph{
					ID:   "p1",
					Runs: []Run{{ID: "r1", Text: "第一段第一行\n第一段第二行"}},
				},
			},
			{
				Type: BlockTypeTable,
				Table: &Table{
					ID: "tbl_1",
					Rows: []TableRow{
						{
							ID: "row_1",
							Cells: []TableCell{
								{ID: "cell_1", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "单元格A1"}}}}}},
								{ID: "cell_2", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p3", Runs: []Run{{ID: "r3", Text: "单元格B1"}}}}}},
							},
						},
						{
							ID: "row_2",
							Cells: []TableCell{
								{ID: "cell_3", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p4", Runs: []Run{{ID: "r4", Text: "单元格A2"}}}}}},
								{ID: "cell_4", Blocks: []Block{{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p5", Runs: []Run{{ID: "r5", Text: "单元格B2"}}}}}},
							},
						},
					},
				},
			},
		},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	lineResult, err := service.Search(session.ID, SearchRequest{Query: "第一段第二行", Scope: "line"})
	if err != nil {
		t.Fatalf("search line: %v", err)
	}
	if len(lineResult.Hits) == 0 || lineResult.Hits[0].Type != "line" {
		t.Fatalf("expected line hit, got %+v", lineResult.Hits)
	}

	paragraphResult, err := service.Search(session.ID, SearchRequest{Query: "第一段第一行", Scope: "paragraph"})
	if err != nil {
		t.Fatalf("search paragraph: %v", err)
	}
	if len(paragraphResult.Hits) == 0 || paragraphResult.Hits[0].Type != "paragraph" {
		t.Fatalf("expected paragraph hit, got %+v", paragraphResult.Hits)
	}

	tableResult, err := service.Search(session.ID, SearchRequest{Query: "单元格B2", Scope: "table"})
	if err != nil {
		t.Fatalf("search table: %v", err)
	}
	if len(tableResult.Hits) == 0 || tableResult.Hits[0].Type != "table_cell" {
		t.Fatalf("expected precise table cell hit, got %+v", tableResult.Hits)
	}
	if tableResult.Hits[0].TableID != "tbl_1" || tableResult.Hits[0].RowIndex != 1 || tableResult.Hits[0].CellIndex != 1 {
		t.Fatalf("expected table cell coordinates (tbl_1,1,1), got %+v", tableResult.Hits[0])
	}
}

func TestServiceSearchIncludesHeaderFooterHits(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_header_footer_search",
		Name:     "header-footer-search.docx",
		Revision: 1,
		Blocks: []Block{{
			Type: BlockTypeParagraph,
			Paragraph: &Paragraph{
				ID:   "body_p1",
				Runs: []Run{{ID: "body_r1", Text: "正文内容"}},
			},
		}},
		Meta: DocumentMeta{
			Format: "docx",
			HeaderFooters: []HeaderFooterPart{
				{
					Kind:    "header",
					Variant: "default",
					Blocks: []Block{{
						Type: BlockTypeParagraph,
						Paragraph: &Paragraph{
							ID:   "header_p1",
							Runs: []Run{{ID: "header_r1", Text: "Auramate 内部资料"}},
						},
					}},
				},
				{
					Kind:    "footer",
					Variant: "first",
					Blocks: []Block{{
						Type: BlockTypeParagraph,
						Paragraph: &Paragraph{
							ID:   "footer_p1",
							Runs: []Run{{ID: "footer_r1", Text: "第 1 页"}},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	headerResult, err := service.Search(session.ID, SearchRequest{Query: "内部资料", Scope: "paragraph"})
	if err != nil {
		t.Fatalf("search header footer paragraph: %v", err)
	}
	if len(headerResult.Hits) == 0 {
		t.Fatalf("expected header hit, got none")
	}
	if headerResult.Hits[0].Region != "header" {
		t.Fatalf("expected header region, got %+v", headerResult.Hits[0])
	}
	if headerResult.Hits[0].HeaderFooterKind != "header" || headerResult.Hits[0].HeaderFooterVariant != "default" {
		t.Fatalf("expected header/default metadata, got %+v", headerResult.Hits[0])
	}

	footerResult, err := service.Search(session.ID, SearchRequest{Query: "第 1 页", Scope: "line"})
	if err != nil {
		t.Fatalf("search footer line: %v", err)
	}
	if len(footerResult.Hits) == 0 {
		t.Fatalf("expected footer hit, got none")
	}
	if footerResult.Hits[0].Region != "footer" {
		t.Fatalf("expected footer region, got %+v", footerResult.Hits[0])
	}
	if footerResult.Hits[0].HeaderFooterKind != "footer" || footerResult.Hits[0].HeaderFooterVariant != "first" {
		t.Fatalf("expected footer/first metadata, got %+v", footerResult.Hits[0])
	}
}

func TestServiceApplyWithOptionsRejectsStaleRevision(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_revision_guard",
		Name:     "apply-revision-guard.docx",
		Revision: 3,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: session.Document.Paragraphs[0].ID,
		StartOffset: 0,
		EndOffset:   len([]rune("原始内容")),
		BeforeText:  "原始内容",
		Text:        "不会生效",
	}}, ApplyOptions{ExpectedRevision: session.Document.Revision - 1})
	if err == nil || !strings.Contains(err.Error(), "document revision changed") {
		t.Fatalf("expected stale revision error, got %v", err)
	}
}

func TestServiceApplyRejectsStaleBeforeText(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_before_text_guard",
		Name:     "apply-before-text-guard.docx",
		Revision: 5,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: session.Document.Paragraphs[0].ID,
		StartOffset: 0,
		EndOffset:   len([]rune("原始内容")),
		BeforeText:  "旧内容",
		Text:        "不会生效",
	}}, ApplyOptions{ExpectedRevision: session.Document.Revision})
	if err == nil || !strings.Contains(err.Error(), "stale replace_text target") {
		t.Fatalf("expected before_text guard error, got %v", err)
	}
}

func TestServiceApplyRejectsMissingExpectedRevision(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_missing_revision_guard",
		Name:     "apply-missing-revision-guard.docx",
		Revision: 5,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{{
		Type:        OperationReplaceText,
		ParagraphID: session.Document.Paragraphs[0].ID,
		StartOffset: 0,
		EndOffset:   len([]rune("原始内容")),
		BeforeText:  "原始内容",
		Text:        "不会生效",
	}}, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "expected_revision is required") {
		t.Fatalf("expected missing expected_revision error, got %v", err)
	}
}

func TestServiceApplyRejectsStaleParagraphBreakTarget(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_break_before_text_guard",
		Name:     "apply-break-before-text-guard.docx",
		Revision: 5,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "第一段第二段"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{{
		Type:        OperationInsertParagraphBreak,
		ParagraphID: session.Document.Paragraphs[0].ID,
		Position:    "before",
		StartOffset: len([]rune("第一段")),
		EndOffset:   len([]rune("第一段第二段")),
		BeforeText:  "旧第二段",
	}}, ApplyOptions{ExpectedRevision: session.Document.Revision})
	if err == nil || !strings.Contains(err.Error(), "stale insert_paragraph_break target") {
		t.Fatalf("expected paragraph break before_text guard error, got %v", err)
	}
}

func TestServiceApplyRejectsStaleLineBreakTarget(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_line_break_before_text_guard",
		Name:     "apply-line-break-before-text-guard.docx",
		Revision: 5,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "第一段第二段"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{{
		Type:        OperationInsertLineBreak,
		ParagraphID: session.Document.Paragraphs[0].ID,
		Position:    "after",
		StartOffset: 0,
		EndOffset:   len([]rune("第一段")),
		BeforeText:  "旧第一段",
	}}, ApplyOptions{ExpectedRevision: session.Document.Revision})
	if err == nil || !strings.Contains(err.Error(), "stale insert_line_break target") {
		t.Fatalf("expected line break before_text guard error, got %v", err)
	}
}

func TestServiceApplyBatchFailureDoesNotMutateSession(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_apply_transaction_guard",
		Name:     "apply-transaction-guard.docx",
		Revision: 9,
		Paragraphs: []Paragraph{{
			ID:   "p1",
			Runs: []Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = service.ApplyWithOptions(session.ID, []Operation{
		{
			Type:        OperationReplaceText,
			ParagraphID: session.Document.Paragraphs[0].ID,
			StartOffset: 0,
			EndOffset:   len([]rune("原始内容")),
			BeforeText:  "原始内容",
			Text:        "第一步已改",
		},
		{
			Type:        OperationReplaceText,
			ParagraphID: session.Document.Paragraphs[0].ID,
			StartOffset: 99,
			EndOffset:   100,
			BeforeText:  "",
			Text:        "第二步失败",
		},
	}, ApplyOptions{ExpectedRevision: session.Document.Revision})
	if err == nil || !strings.Contains(err.Error(), "invalid replace offsets") {
		t.Fatalf("expected batch apply failure, got %v", err)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after failed apply: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "原始内容" {
		t.Fatalf("expected session document to remain unchanged after failed batch, got %q", got)
	}
	if snapshot.Document.Revision != session.Document.Revision {
		t.Fatalf("expected revision to remain %d after failed batch, got %d", session.Document.Revision, snapshot.Document.Revision)
	}

	diffs, err := service.Diff(session.ID)
	if err != nil {
		t.Fatalf("diff after failed apply: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs after failed batch apply, got %+v", diffs)
	}
}

func TestServiceCheckpointSaveListRestore(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_checkpoint_restore",
		Name:     "checkpoint-restore.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "原始内容"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	initialCheckpoint, err := service.SaveCheckpoint(session.ID, "initial")
	if err != nil {
		t.Fatalf("save initial checkpoint: %v", err)
	}
	if initialCheckpoint.Revision != 1 {
		t.Fatalf("expected checkpoint revision 1, got %+v", initialCheckpoint)
	}

	if _, err := service.ExecutePlannedAction(session.ID, EditorAction{Action: "replace", TargetText: "原始内容", NewText: "阶段一内容"}); err != nil {
		t.Fatalf("execute first stage: %v", err)
	}
	phaseOneCheckpoint, err := service.SaveCheckpoint(session.ID, "phase-one")
	if err != nil {
		t.Fatalf("save phase-one checkpoint: %v", err)
	}

	if _, err := service.ExecutePlannedAction(session.ID, EditorAction{Action: "replace", TargetText: "阶段一内容", NewText: "阶段二内容"}); err != nil {
		t.Fatalf("execute second stage: %v", err)
	}

	checkpoints, err := service.ListCheckpoints(session.ID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("expected 2 checkpoints, got %+v", checkpoints)
	}

	restored, err := service.RestoreCheckpoint(session.ID, phaseOneCheckpoint.ID)
	if err != nil {
		t.Fatalf("restore checkpoint: %v", err)
	}
	if got := restored.Paragraphs[0].PlainText(); got != "阶段一内容" {
		t.Fatalf("expected restored text 阶段一内容, got %q", got)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after restore: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "阶段一内容" {
		t.Fatalf("expected session text 阶段一内容 after restore, got %q", got)
	}
	if len(snapshot.Checkpoints) != 2 {
		t.Fatalf("expected session checkpoints to remain addressable, got %+v", snapshot.Checkpoints)
	}
}

func TestPlanActionFailureIncludesRecoveryPlan(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_recovery_plan_failed_plan",
		Name:     "recovery-plan-failed-plan.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "一、现状分析"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "二、现状分析"}}}},
		},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	plan, err := service.PlanAction(session.ID, EditorAction{Action: "replace", TargetText: "现状分析", NewText: "方案分析"})
	if err != nil {
		t.Fatalf("plan action: %v", err)
	}
	if plan.Status != "failed" || plan.Diagnostics == nil || plan.Diagnostics.RecoveryPlan == nil {
		t.Fatalf("expected failed plan with recovery plan, got %+v", plan)
	}
	if plan.Diagnostics.RecoveryPlan.PrimaryStrategy != "return_candidates" {
		t.Fatalf("expected ambiguous recovery strategy return_candidates, got %+v", plan.Diagnostics.RecoveryPlan)
	}
}

func TestExecutePlannedActionReportsAnchorHealthForReplannedTarget(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_anchor_health",
		Name:     "anchor-health.docx",
		Revision: 1,
		Blocks: []Block{{
			Type:      BlockTypeParagraph,
			Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "一、现状分析"}}},
		}},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := service.ExecutePlannedAction(session.ID, EditorAction{Action: "replace", TargetText: "现状分析", NewText: "方案分析", PreferredParagraphID: "p_missing"})
	if err != nil {
		t.Fatalf("execute planned action: %v", err)
	}
	if result.Status != "applied" {
		t.Fatalf("expected applied result, got %+v", result)
	}
	if result.AnchorHealth == nil || !result.AnchorHealth.IsStale || !result.AnchorHealth.ReplanApplied {
		t.Fatalf("expected stale anchor health with replan, got %+v", result.AnchorHealth)
	}
	if result.Plan == nil || result.Plan.AnchorHealth == nil || result.Plan.AnchorHealth.Status != "replanned" {
		t.Fatalf("expected replanned anchor health on plan, got %+v", result.Plan)
	}
	if got := result.Document.Paragraphs[0].PlainText(); got != "一、方案分析" {
		t.Fatalf("expected updated paragraph after replan, got %q", got)
	}
}

func TestExecuteActionBatchRollsBackOnMidBatchRevisionConflict(t *testing.T) {
	service := NewService(t.TempDir())
	session, err := service.CreateSession(&Document{
		ID:       "doc_batch_rollback",
		Name:     "batch-rollback.docx",
		Revision: 1,
		Blocks: []Block{
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p1", Runs: []Run{{ID: "r1", Text: "第一段"}}}},
			{Type: BlockTypeParagraph, Paragraph: &Paragraph{ID: "p2", Runs: []Run{{ID: "r2", Text: "第二段"}}}},
		},
		Meta: DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	batch, err := service.PlanActionBatch(session.ID, []EditorAction{
		{Action: "replace", TargetText: "第一段", NewText: "第一段-已改"},
		{Action: "replace", TargetText: "第二段", NewText: "第二段-已改"},
	})
	if err != nil {
		t.Fatalf("plan batch: %v", err)
	}
	if batch.Status != "ready" || len(batch.Plans) != 2 {
		t.Fatalf("expected ready batch plan, got %+v", batch)
	}

	service.mu.Lock()
	service.batches[batch.ID].Plans[1].ExpectedRevision++
	service.mu.Unlock()

	executed, err := service.ExecuteActionBatch(session.ID, batch.ID)
	if err != nil {
		t.Fatalf("execute batch: %v", err)
	}
	if executed.Status != "rolled_back" {
		t.Fatalf("expected rolled_back batch, got %+v", executed)
	}
	if executed.FailureStep != 1 || executed.CheckpointID == "" {
		t.Fatalf("expected failure on second step with checkpoint, got %+v", executed)
	}
	if executed.RecoveryPlan == nil || executed.RecoveryPlan.CheckpointID == "" {
		t.Fatalf("expected recovery plan with checkpoint id, got %+v", executed.RecoveryPlan)
	}

	snapshot, err := service.Snapshot(session.ID)
	if err != nil {
		t.Fatalf("snapshot after batch rollback: %v", err)
	}
	if got := snapshot.Document.Paragraphs[0].PlainText(); got != "第一段" {
		t.Fatalf("expected first paragraph restored after rollback, got %q", got)
	}
	if got := snapshot.Document.Paragraphs[1].PlainText(); got != "第二段" {
		t.Fatalf("expected second paragraph restored after rollback, got %q", got)
	}
}

func createDOCXFromParts(t *testing.T, path string, parts map[string]string) {
	t.Helper()
	binaryParts := make(map[string][]byte, len(parts))
	for name, content := range parts {
		binaryParts[name] = []byte(content)
	}
	createDOCXFromBinaryParts(t, path, binaryParts)
}

func createDOCXFromBinaryParts(t *testing.T, path string, parts map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create docx dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docx file: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for name, content := range parts {
		entry, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write(content); err != nil {
			_ = writer.Close()
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

func minimalPNG() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D, 0xB1, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}
}

func assertZipEntryEquals(t *testing.T, path, entryName string, expected []byte) {
	t.Helper()
	content := readZipEntry(t, path, entryName)
	if !bytes.Equal(content, expected) {
		t.Fatalf("zip entry %s content mismatch", entryName)
	}
}

func assertZipEntryContains(t *testing.T, path, entryName, expected string) {
	t.Helper()
	content := readZipEntry(t, path, entryName)
	if !bytes.Contains(content, []byte(expected)) {
		t.Fatalf("zip entry %s does not contain %q", entryName, expected)
	}
}

func readZipEntry(t *testing.T, path, entryName string) []byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != entryName {
			continue
		}
		content, err := readZipFile(file)
		if err != nil {
			t.Fatalf("read zip entry %s: %v", entryName, err)
		}
		return content
	}
	t.Fatalf("zip entry not found: %s", entryName)
	return nil
}
