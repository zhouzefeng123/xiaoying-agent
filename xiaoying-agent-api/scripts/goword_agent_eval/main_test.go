package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/goword"
)

func TestBuildLLMDialoguePromptEnablesWordContext(t *testing.T) {
	prompt := buildLLMDialoguePrompt("C:/tmp/demo.docx", "把表格里的通过率改成92%", nil)

	checks := []string{
		"[WORD_CONTEXT]",
		"editing_policy: interface_only",
		"notes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。",
		"document_path: C:/tmp/demo.docx",
		"只能使用 read、auramate-word-reader 和 auramate-word-copilot 三类工具完成任务。",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", check, prompt)
		}
	}
}

func TestBuildLLMDialogueBudgetAssessmentWarnsOnExpensiveBatch(t *testing.T) {
	scenario := llmDialogueScenario{
		Name:           "llm_e2e_relative_multi_region_batch",
		Stages:         []llmDialogueStage{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		MinActionCount: 3,
		MinReaderCalls: 3,
	}
	llmResult := &llmDialogueRunResult{
		ReaderCallCount:  6,
		CopilotCallCount: 9,
		ToolCalls:        make([]string, 15),
		InputTokens:      267760,
	}
	assessment := buildLLMDialogueBudgetAssessment(scenario, llmResult, 155014)
	if assessment == nil {
		t.Fatal("expected assessment")
	}
	if got := assessment["status"]; got != "warning" {
		t.Fatalf("expected warning status, got %v", got)
	}
	issues, ok := assessment["issues"].([]map[string]any)
	if !ok || len(issues) == 0 {
		t.Fatalf("expected warning issues, got %#v", assessment["issues"])
	}
	metrics, ok := assessment["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics map, got %#v", assessment["metrics"])
	}
	if got := metrics["copilot_call_count"]; got != 9 {
		t.Fatalf("expected copilot_call_count=9, got %#v", got)
	}
}

func TestBuildLLMDialogueBudgetAssessmentPassesWithinBudget(t *testing.T) {
	scenario := llmDialogueScenario{
		Name:           "llm_e2e_cross_region_batch",
		Stages:         []llmDialogueStage{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		MinActionCount: 3,
		MinReaderCalls: 2,
	}
	llmResult := &llmDialogueRunResult{
		ReaderCallCount:  4,
		CopilotCallCount: 5,
		ToolCalls:        make([]string, 11),
		InputTokens:      180000,
	}
	assessment := buildLLMDialogueBudgetAssessment(scenario, llmResult, 120000)
	if assessment == nil {
		t.Fatal("expected assessment")
	}
	if got := assessment["status"]; got != "passed" {
		t.Fatalf("expected passed status, got %v", got)
	}
	if _, exists := assessment["issues"]; exists {
		t.Fatalf("did not expect issues, got %#v", assessment["issues"])
	}
}

func TestHasSectionLayoutPatchRecognizesPageNumberFields(t *testing.T) {
	if !hasSectionLayoutPatch(goword.SectionLayout{PageNumStart: 5, PageNumFmt: "upperRoman"}) {
		t.Fatal("expected page number fields to count as a valid section layout patch")
	}
	if !hasSectionLayoutPatch(goword.SectionLayout{TitlePage: true, TitlePageSet: true}) {
		t.Fatal("expected title page flags to count as a valid section layout patch")
	}
	if hasSectionLayoutPatch(goword.SectionLayout{}) {
		t.Fatal("expected empty section layout patch to be invalid")
	}
}

func TestBuildOfficeAlignmentTodoListPromotesVisualAndDrawingBaselines(t *testing.T) {
	report := &report{Scenarios: []scenarioResult{
		{Name: "word_pdf_visual_baseline_oracle", Passed: true},
		{Name: "llm_e2e_drawing_objects_body_edit_preserves_objects", Passed: true},
	}}
	items := buildOfficeAlignmentTodoList(report)
	visual := findOfficeAlignmentTodoItem(items, "真实 Office / PDF 视觉基线")
	if visual == nil {
		t.Fatal("expected visual baseline item")
	}
	if visual.Status != "done" {
		t.Fatalf("expected visual baseline status done, got %q", visual.Status)
	}
	drawing := findOfficeAlignmentTodoItem(items, "浮动图片、文本框、形状")
	if drawing == nil {
		t.Fatal("expected drawing objects item")
	}
	if drawing.Status != "done" {
		t.Fatalf("expected drawing objects status done, got %q", drawing.Status)
	}
}

func TestBuildDrawingObjectsEditFixturePreservesSummaryAndAnchorText(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "drawing-objects-edit.docx")
	if err := buildDrawingObjectsEditFixture(docPath); err != nil {
		t.Fatalf("build drawing objects fixture: %v", err)
	}
	document, err := goword.OpenDOCX(docPath)
	if err != nil {
		t.Fatalf("open drawing objects fixture: %v", err)
	}
	if findParagraphByText(document, "对象区提示：当前版本仅供内部评审。") == nil {
		t.Fatal("expected editable body paragraph")
	}
	if findParagraphByText(document, "对象区说明：后续文本框和形状对象必须原样保留。") == nil {
		t.Fatal("expected neighbor body paragraph")
	}
	if document.Meta.ObjectSummary == nil {
		t.Fatal("expected object summary")
	}
	summary := document.Meta.ObjectSummary
	if summary.InlineImages != 1 || summary.AnchoredImages != 2 || summary.LegacyImages != 1 || summary.LinkedImages != 1 || summary.TextBoxes != 1 || summary.Shapes != 1 {
		t.Fatalf("unexpected object summary: %+v", *summary)
	}
}

func TestPDFDiffHelpersSummarizeChangedPages(t *testing.T) {
	report := &pdfVisualDiffReport{Pages: []pdfPageDiff{
		{PageIndex: 0, ImagesIdentical: true, PixelDiffRatio: 0},
		{PageIndex: 1, ImagesIdentical: false, PixelDiffRatio: 0.125},
		{PageIndex: 2, ImagesIdentical: false, PixelDiffRatio: 0.05},
	}}
	if got := countIdenticalPDFPages(report); got != 1 {
		t.Fatalf("expected identical page count 1, got %d", got)
	}
	pages := changedPDFPages(report)
	if len(pages) != 2 || pages[0] != 2 || pages[1] != 3 {
		t.Fatalf("unexpected changed pages: %#v", pages)
	}
	if got := maxPDFPixelDiffRatio(report); got != 0.125 {
		t.Fatalf("expected max diff ratio 0.125, got %v", got)
	}
}

func findOfficeAlignmentTodoItem(items []officeAlignmentTodoItem, title string) *officeAlignmentTodoItem {
	for index := range items {
		if items[index].Title == title {
			return &items[index]
		}
	}
	return nil
}
