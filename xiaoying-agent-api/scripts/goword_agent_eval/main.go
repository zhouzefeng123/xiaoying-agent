package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	auraapp "github.com/auramate/auramate/internal/app"
	appconfig "github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/goword"
	llmagent "github.com/auramate/auramate/internal/llm/agent"
	toolruntime "github.com/auramate/auramate/internal/llm/tools"
	wordserver "github.com/auramate/auramate/internal/server"
)

type config struct {
	RepoRoot     string
	Artifacts    string
	ReportPath   string
	PythonPath   string
	KeepOutputs  bool
	E2EModel     string
	E2ETimeout   time.Duration
	LLMScenario  string
	OnlyLLME2E   bool
	ExternalDocx string
}

type report struct {
	StartedAt         time.Time        `json:"started_at"`
	FinishedAt        time.Time        `json:"finished_at"`
	DurationMillis    int64            `json:"duration_ms"`
	InputDocumentPath string           `json:"input_document_path"`
	ModelE2E          *modelE2ESummary `json:"model_e2e,omitempty"`
	Scenarios         []scenarioResult `json:"scenarios"`
	Passed            int              `json:"passed"`
	Failed            int              `json:"failed"`
	AutoTuningApplied int              `json:"auto_tuning_applied"`
	TuningActions     []string         `json:"tuning_actions"`
	Summary           string           `json:"summary"`
}

type scenarioResult struct {
	Name              string         `json:"name"`
	Passed            bool           `json:"passed"`
	AutoTuned         bool           `json:"auto_tuned,omitempty"`
	DurationMillis    int64          `json:"duration_ms"`
	Message           string         `json:"message"`
	ArtifactPath      string         `json:"artifact_path,omitempty"`
	Details           map[string]any `json:"details,omitempty"`
	Suggestions       []string       `json:"suggestions,omitempty"`
	TuningAdjustments []string       `json:"tuning_adjustments,omitempty"`
	Error             string         `json:"error,omitempty"`
}

type pdfPageRender struct {
	PageIndex int    `json:"page_index"`
	PNGPath   string `json:"png_path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SHA256    string `json:"sha256"`
}

type pdfPageDiff struct {
	PageIndex       int     `json:"page_index"`
	LeftPNGPath     string  `json:"left_png_path"`
	RightPNGPath    string  `json:"right_png_path"`
	DiffPNGPath     string  `json:"diff_png_path,omitempty"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	LeftSHA256      string  `json:"left_sha256"`
	RightSHA256     string  `json:"right_sha256"`
	PixelDiffCount  int64   `json:"pixel_diff_count"`
	PixelDiffRatio  float64 `json:"pixel_diff_ratio"`
	ImagesIdentical bool    `json:"images_identical"`
}

type pdfVisualDiffReport struct {
	LeftPDFPath       string          `json:"left_pdf_path"`
	RightPDFPath      string          `json:"right_pdf_path"`
	LeftPageCount     int             `json:"left_page_count"`
	RightPageCount    int             `json:"right_page_count"`
	ComparedPageCount int             `json:"compared_page_count"`
	AllPagesIdentical bool            `json:"all_pages_identical"`
	LeftPages         []pdfPageRender `json:"left_pages"`
	RightPages        []pdfPageRender `json:"right_pages"`
	Pages             []pdfPageDiff   `json:"pages"`
}

type evalContext struct {
	config       config
	inputDocPath string
	runner       *skillRunner
	liveRunner   *liveModelRunner
	client       *apiClient
	server       *miniGoWordServer
}

type miniGoWordServer struct {
	service *goword.Service
	server  *httptest.Server
}

type apiClient struct {
	baseURL string
	http    *http.Client
}

type skillRunner struct {
	readerTool  toolruntime.BaseTool
	copilotTool toolruntime.BaseTool
}

type modelE2ESummary struct {
	Model          string `json:"model"`
	ScenarioCount  int    `json:"scenario_count"`
	Passed         int    `json:"passed"`
	Failed         int    `json:"failed"`
	DurationMillis int64  `json:"duration_ms"`
	Summary        string `json:"summary"`
}

type officeAlignmentTodoItem struct {
	Title       string `json:"title"`
	Area        string `json:"area"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Verify      string `json:"verify"`
	Evidence    string `json:"evidence"`
}

type officeGuaranteeNote struct {
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

type liveModelRunner struct {
	app        *auraapp.App
	conn       *sql.DB
	model      string
	timeout    time.Duration
	restoreEnv func()
}

type llmDialogueStage struct {
	Name                       string
	UserInstruction            string
	PromptHints                []string
	ExpectedAction             string
	ExpectedParagraphStyleID   string
	ExactActionCount           int
	MinReaderCalls             int
	MaxReaderCalls             int
	RequireExplorationSummary  bool
	RequireParagraphStylePatch bool
	RequireNoAction            bool
	SkipCopilotCheck           bool
}

type llmDialogueScenario struct {
	Name                       string
	UserInstruction            string
	PromptHints                []string
	Stages                     []llmDialogueStage
	InputDocBuilder            func(string) error
	ArtifactName               string
	ExpectedAction             string
	ExpectedParagraphStyleID   string
	MinActionCount             int
	MinReaderCalls             int
	MaxReaderCalls             int
	RequireExplorationSummary  bool
	RequireReopenedLayout      bool
	RequireParagraphStylePatch bool
	RequireSharedTableID       bool
	Suggestions                []string
	Verify                     func(*goword.Document, *executionResult) error
}

const llmDialogueStageMaxAttempts = 2
const llmDialogueAttemptMaxAttempts = 2

type llmDialogueRunResult struct {
	Actions             []*editorActionInput
	RawActions          []map[string]any
	Action              *editorActionInput
	ToolCalls           []string
	ToolTrace           []llmToolTrace
	AssistantText       string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	UsedReader          bool
	UsedCopilot         bool
	ReaderCallCount     int
	CopilotCallCount    int
}

type externalContractTableTargets struct {
	TaxIncluded  string
	TaxExcluded  string
	SummaryGross string
	SummaryNet   string
}

type llmToolTrace struct {
	Stage         string         `json:"stage,omitempty"`
	ExecutionID   string         `json:"execution_id,omitempty"`
	Name          string         `json:"name"`
	Input         map[string]any `json:"input,omitempty"`
	InputPreview  string         `json:"input_preview,omitempty"`
	IsError       bool           `json:"is_error,omitempty"`
	ResultSummary map[string]any `json:"result_summary,omitempty"`
	ResultPreview string         `json:"result_preview,omitempty"`
}

var llmGenericFormatBasisCues = []string{"保持原格式", "保持原有格式", "保持一致", "沿用原格式", "沿用原有格式", "按原格式"}
var llmConcreteFormatBasisCues = []string{"style_id", "style_catalog", "query_hits", "outline", "font", "bold", "italic", "字号", "字体", "对齐", "缩进", "段前", "段后", "normal", "heading", "title", "命中", "段落", "正文", "标题", "列表", "条目", "单元格", "所在行", "表头", "页", "page", "layout"}

type wordReaderResponse struct {
	Status       string              `json:"status"`
	Message      string              `json:"message,omitempty"`
	Document     readerDocumentInfo  `json:"document"`
	StyleCatalog readerStyleCatalog  `json:"style_catalog"`
	Outline      []readerOutlineItem `json:"outline"`
	Query        readerQueryResult   `json:"query"`
	Session      struct {
		ID string `json:"id"`
	} `json:"session"`
}

type readerDocumentInfo struct {
	Name              string `json:"name"`
	SourcePath        string `json:"source_path"`
	Revision          int64  `json:"revision"`
	BlockCount        int    `json:"block_count"`
	ParagraphCount    int    `json:"paragraph_count"`
	TableCount        int    `json:"table_count"`
	HeadingCount      int    `json:"heading_count"`
	HeaderFooterCount int    `json:"header_footer_count"`
}

type readerStyleCatalog struct {
	DefaultParagraphStyle readerParagraphStyle      `json:"default_paragraph_style"`
	DefaultTextStyle      readerTextStyle           `json:"default_text_style"`
	Styles                []readerDocumentStyleItem `json:"styles"`
}

type readerDocumentStyleItem struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Type      string               `json:"type"`
	BasedOn   string               `json:"based_on"`
	IsDefault bool                 `json:"is_default"`
	Paragraph readerParagraphStyle `json:"paragraph"`
	Text      readerTextStyle      `json:"text"`
}

type readerOutlineItem struct {
	Index               int                  `json:"index"`
	Type                string               `json:"type"`
	Region              string               `json:"region"`
	HeaderFooterKind    string               `json:"header_footer_kind"`
	HeaderFooterVariant string               `json:"header_footer_variant"`
	ParagraphID         string               `json:"paragraph_id"`
	StyleID             string               `json:"style_id"`
	ParagraphStyle      readerParagraphStyle `json:"paragraph_style"`
	DominantTextStyle   readerTextStyle      `json:"dominant_text_style"`
	Preview             string               `json:"preview"`
	TextLength          int                  `json:"text_length"`
	TableID             string               `json:"table_id"`
}

type readerParagraphStyle struct {
	StyleID         string                 `json:"style_id"`
	Alignment       string                 `json:"alignment"`
	SpacingBefore   int                    `json:"spacing_before"`
	SpacingAfter    int                    `json:"spacing_after"`
	LineSpacing     int                    `json:"line_spacing"`
	FirstLineIndent int                    `json:"first_line_indent"`
	LeftIndent      int                    `json:"left_indent"`
	Numbering       map[string]interface{} `json:"numbering"`
}

type readerTextStyle struct {
	StyleID        string `json:"style_id"`
	FontFamily     string `json:"font_family"`
	FontSizeHP     int    `json:"font_size_hp"`
	Bold           bool   `json:"bold"`
	Italic         bool   `json:"italic"`
	Underline      bool   `json:"underline"`
	Color          string `json:"color"`
	ThemeColor     string `json:"theme_color"`
	ThemeFontASCII string `json:"theme_font_ascii"`
}

type readerQueryResult struct {
	Text     string           `json:"text"`
	Scope    string           `json:"scope"`
	HitCount int              `json:"hit_count"`
	Hits     []readerQueryHit `json:"hits"`
}

type readerQueryHit struct {
	Type                string               `json:"type"`
	Region              string               `json:"region"`
	HeaderFooterKind    string               `json:"header_footer_kind"`
	HeaderFooterVariant string               `json:"header_footer_variant"`
	ParagraphID         string               `json:"paragraph_id"`
	TableID             string               `json:"table_id"`
	RowIndex            int                  `json:"row_index"`
	CellIndex           int                  `json:"cell_index"`
	LineIndex           int                  `json:"line_index"`
	MatchStart          int                  `json:"match_start"`
	MatchEnd            int                  `json:"match_end"`
	ParagraphStyle      readerParagraphStyle `json:"paragraph_style"`
	DominantTextStyle   readerTextStyle      `json:"dominant_text_style"`
	Preview             string               `json:"preview"`
}

type editorActionInput struct {
	Action               string                    `json:"action"`
	TargetText           string                    `json:"target_text"`
	PreferredParagraphID string                    `json:"preferred_paragraph_id,omitempty"`
	TableID              string                    `json:"table_id,omitempty"`
	RowIndex             int                       `json:"row_index,omitempty"`
	CellIndex            int                       `json:"cell_index,omitempty"`
	StylePatch           goword.TextStyle          `json:"style_patch,omitempty"`
	ParagraphStylePatch  goword.ParagraphStyle     `json:"paragraph_style_patch,omitempty"`
	TableSpec            *goword.TableInput        `json:"table_spec,omitempty"`
	TableRow             *goword.TableRowInput     `json:"table_row,omitempty"`
	TableColumn          *goword.TableColumnInput  `json:"table_column,omitempty"`
	TableCell            *goword.TableCellInput    `json:"table_cell,omitempty"`
	HeaderFooter         *goword.HeaderFooterInput `json:"header_footer,omitempty"`
	SectionLayoutPatch   goword.SectionLayout      `json:"section_layout_patch,omitempty"`
	NewText              string                    `json:"new_text,omitempty"`
	Position             string                    `json:"position,omitempty"`
	EnableTrackChanges   bool                      `json:"enable_track_changes,omitempty"`
	EditIntent           string                    `json:"edit_intent,omitempty"`
	ContextScope         string                    `json:"context_scope,omitempty"`
	Constraints          string                    `json:"constraints,omitempty"`
	PreviewOnly          bool                      `json:"preview_only,omitempty"`
	RetrievalQuery       string                    `json:"retrieval_query,omitempty"`
	RequireRetrieval     bool                      `json:"require_retrieval,omitempty"`
	ReaderSessionID      string                    `json:"reader_session_id,omitempty"`
	ReaderContextSummary string                    `json:"reader_context_summary,omitempty"`
	FormatBasis          string                    `json:"format_basis,omitempty"`
	ExplorationSummary   string                    `json:"exploration_summary,omitempty"`
}

type applyResponse struct {
	Document *goword.Document       `json:"document"`
	Diffs    []goword.ParagraphDiff `json:"diffs"`
}

type executionResult struct {
	BeforeDocument    *goword.Document
	ApplyResult       *applyResponse
	SavedResult       *goword.SaveResult
	ReopenedDocument  *goword.Document
	ReopenedLayout    *goword.DocumentLayout
	InsertedParagraph *goword.Paragraph
	TemplateParagraph *goword.Paragraph
	TemplateRole      string
	SearchResult      *goword.SearchResult
	AppliedActions    []*editorActionInput
	ToolTrace         []llmToolTrace
	BatchResults      []*executionResult
	TotalDiffCount    int
}

type paragraphRunSnapshot struct {
	Text      string              `json:"text"`
	BreakType goword.RunBreakType `json:"break_type,omitempty"`
	Style     goword.TextStyle    `json:"style"`
}

type paragraphStructureSnapshot struct {
	Text          string                 `json:"text"`
	Style         goword.ParagraphStyle  `json:"style"`
	NumberingText string                 `json:"numbering_text,omitempty"`
	Runs          []paragraphRunSnapshot `json:"runs"`
}

type tableCellStructureSnapshot struct {
	Paragraphs []paragraphStructureSnapshot `json:"paragraphs"`
}

type bridgeRequest struct {
	Action               string
	TargetText           string
	PreferredParagraphID string
	TableID              string
	RowIndex             int
	RowIndexSet          bool
	CellIndex            int
	CellIndexSet         bool
	StylePatch           goword.TextStyle
	ParagraphStylePatch  goword.ParagraphStyle
	TableSpec            *goword.TableInput
	TableRow             *goword.TableRowInput
	TableColumn          *goword.TableColumnInput
	TableCell            *goword.TableCellInput
	HeaderFooter         *goword.HeaderFooterInput
	SectionLayoutPatch   goword.SectionLayout
	NewText              string
	Position             string
	EditIntent           string
	ContextScope         string
	Constraints          string
	PreviewOnly          bool
	RetrievalQuery       string
	ReaderSessionID      string
	EnableTrackChanges   bool
	ReaderContextSummary string
	FormatBasis          string
	ExplorationSummary   string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	report, err := runEvaluation(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "评估失败: %v\n", err)
		os.Exit(1)
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化评估结果失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))

	if report.Failed > 0 {
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	repoRoot, err := findRepoRoot(wd)
	if err != nil {
		return config{}, err
	}

	defaultArtifacts := filepath.Join(repoRoot, "workspace", "goword-agent-eval")
	defaultReport := filepath.Join(defaultArtifacts, "report.json")

	artifacts := flag.String("artifacts", defaultArtifacts, "评估产物目录")
	reportPath := flag.String("report", defaultReport, "评估报告 JSON 输出路径")
	pythonPath := flag.String("python", "", "兼容保留参数；当前原生 Go 评测链路通常不需要")
	keepOutputs := flag.Bool("keep-outputs", true, "是否保留生成的 docx 产物")
	e2eModel := flag.String("e2e-model", "", "固定测试模型 ID；非空时启用真实大模型端到端对话评估")
	e2eTimeoutSeconds := flag.Int("e2e-timeout", 180, "真实大模型端到端单场景超时时间（秒）")
	llmScenario := flag.String("llm-scenario", "", "只运行名称包含该关键字的 llm e2e 场景")
	onlyLLME2E := flag.Bool("only-llm-e2e", false, "只运行 llm e2e 场景，跳过静态回归场景")
	externalDocx := flag.String("external-docx", "", "可选：外部真实 DOCX 路径。供指定 llm e2e 场景复制为输入文档")
	flag.Parse()

	if *e2eTimeoutSeconds <= 0 {
		return config{}, fmt.Errorf("--e2e-timeout 必须大于 0")
	}

	artifactsPath, err := filepath.Abs(filepath.Clean(*artifacts))
	if err != nil {
		return config{}, fmt.Errorf("解析 --artifacts 失败: %w", err)
	}
	reportOutputPath, err := filepath.Abs(filepath.Clean(*reportPath))
	if err != nil {
		return config{}, fmt.Errorf("解析 --report 失败: %w", err)
	}
	externalDocxPath := strings.TrimSpace(*externalDocx)
	if externalDocxPath != "" {
		externalDocxPath, err = filepath.Abs(filepath.Clean(externalDocxPath))
		if err != nil {
			return config{}, fmt.Errorf("解析 --external-docx 失败: %w", err)
		}
		if _, err := os.Stat(externalDocxPath); err != nil {
			return config{}, fmt.Errorf("external docx 不可用: %w", err)
		}
	}

	return config{
		RepoRoot:     repoRoot,
		Artifacts:    artifactsPath,
		ReportPath:   reportOutputPath,
		PythonPath:   strings.TrimSpace(*pythonPath),
		KeepOutputs:  *keepOutputs,
		E2EModel:     strings.TrimSpace(*e2eModel),
		E2ETimeout:   time.Duration(*e2eTimeoutSeconds) * time.Second,
		LLMScenario:  strings.TrimSpace(*llmScenario),
		OnlyLLME2E:   *onlyLLME2E,
		ExternalDocx: externalDocxPath,
	}, nil
}

func runEvaluation(cfg config) (*report, error) {
	started := time.Now()

	if err := os.MkdirAll(cfg.Artifacts, 0o755); err != nil {
		return nil, fmt.Errorf("创建产物目录失败: %w", err)
	}

	inputPath := filepath.Join(cfg.Artifacts, "goword-agent-real-doc.docx")
	if err := buildRealDocumentFixture(inputPath); err != nil {
		return nil, fmt.Errorf("生成真实 DOCX 失败: %w", err)
	}

	server := newMiniGoWordServer(cfg.Artifacts)
	defer server.Close()
	toolruntime.SetReadToolGoWordService(server.service)
	toolruntime.SetWordReaderToolGoWordService(server.service)
	defer toolruntime.SetReadToolGoWordService(nil)
	defer toolruntime.SetWordReaderToolGoWordService(nil)

	ctx := &evalContext{
		config:       cfg,
		inputDocPath: inputPath,
		client:       newAPIClient(server.server.URL),
		server:       server,
		runner: &skillRunner{
			readerTool:  toolruntime.NewWordReaderTool(),
			copilotTool: toolruntime.NewWordCopilotTool(),
		},
	}
	if cfg.E2EModel != "" {
		liveRunner, err := newLiveModelRunner(cfg, server.server.URL)
		if err != nil {
			return nil, fmt.Errorf("初始化真实模型端到端运行器失败: %w", err)
		}
		toolruntime.SetReadToolGoWordService(server.service)
		toolruntime.SetWordReaderToolGoWordService(server.service)
		defer func() {
			_ = liveRunner.Close()
		}()
		ctx.liveRunner = liveRunner
	}

	results := make([]scenarioResult, 0, 20)
	if !cfg.OnlyLLME2E {
		results = append(results,
			runReaderScenario(ctx),
			runCopilotGuardScenario(ctx),
			runReplaceBodyScenario(ctx),
			runWordPDFVisualBaselineScenario(ctx),
			runOOXMLObjectSummaryComplexScenario(ctx),
			runOOXMLObjectSummaryImageScenario(ctx),
			runInsertBodyScenario(ctx),
			runInsertHeadingScenario(ctx),
			runSemanticPreciseAnchorScenario(ctx),
			runSummaryInferredAnchorScenario(ctx),
			runDialogueMiddleNoteScenario(ctx),
			runDialogueFuzzyFeeInsertScenario(ctx),
			runDialogueTailRemarkScenario(ctx),
			runDialogueRiskCellInsertScenario(ctx),
			runFormatEmphasisScenario(ctx),
			runReplaceTableScenario(ctx),
			runInsertTableParagraphScenario(ctx),
			runInsertListExplanationScenario(ctx),
			runEnterpriseWorkflowScenario(ctx),
			runSessionRecoveryScenario(ctx),
		)
	}
	var modelE2E *modelE2ESummary
	if ctx.liveRunner != nil {
		llmResults, summary := runLLMEndToEndSuite(ctx)
		results = append(results, llmResults...)
		modelE2E = summary
	}
	finished := time.Now()

	result := &report{
		StartedAt:         started,
		FinishedAt:        finished,
		DurationMillis:    finished.Sub(started).Milliseconds(),
		InputDocumentPath: inputPath,
		ModelE2E:          modelE2E,
		Scenarios:         results,
	}
	collectReportSummary(result)

	if err := os.MkdirAll(filepath.Dir(cfg.ReportPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建报告目录失败: %w", err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化报告失败: %w", err)
	}
	if err := os.WriteFile(cfg.ReportPath, encoded, 0o644); err != nil {
		return nil, fmt.Errorf("写入报告失败: %w", err)
	}
	htmlReportPath := filepath.Join(filepath.Dir(cfg.ReportPath), "interactive-validation-report.html")
	if err := writeInteractiveValidationReport(result, htmlReportPath); err != nil {
		return nil, fmt.Errorf("写入 HTML 报告失败: %w", err)
	}

	return result, nil
}

func writeInteractiveValidationReport(result *report, outputPath string) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("序列化 HTML 报告数据失败: %w", err)
	}
	roadmapEncoded, err := json.Marshal(buildOfficeAlignmentTodoList(result))
	if err != nil {
		return fmt.Errorf("序列化 Office 对齐待办失败: %w", err)
	}
	guaranteeEncoded, err := json.Marshal(buildOfficeGuaranteeNotes())
	if err != nil {
		return fmt.Errorf("序列化 Office 保证阻塞说明失败: %w", err)
	}
	view := struct {
		GeneratedAt   string
		ReportJSON    template.JS
		RoadmapJSON   template.JS
		GuaranteeJSON template.JS
	}{
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05"),
		ReportJSON:    template.JS(encoded),
		RoadmapJSON:   template.JS(roadmapEncoded),
		GuaranteeJSON: template.JS(guaranteeEncoded),
	}
	tmpl, err := template.New("interactive-validation-report").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>GoWord 评测报告</title>
	<style>
		:root {
			--bg: #f4efe6;
			--panel: rgba(255, 250, 243, 0.9);
			--card: #fffaf2;
			--text: #1f1a14;
			--muted: #6b6256;
			--accent: #0f766e;
			--accent-soft: rgba(15, 118, 110, 0.12);
			--danger: #b42318;
			--danger-soft: rgba(180, 35, 24, 0.1);
			--success: #166534;
			--success-soft: rgba(22, 101, 52, 0.1);
			--border: rgba(64, 51, 37, 0.12);
			--shadow: 0 18px 50px rgba(58, 42, 25, 0.12);
		}
		* { box-sizing: border-box; }
		body {
			margin: 0;
			font-family: "Microsoft YaHei", "PingFang SC", sans-serif;
			color: var(--text);
			background:
				radial-gradient(circle at top left, rgba(15, 118, 110, 0.18), transparent 24%),
				radial-gradient(circle at top right, rgba(194, 65, 12, 0.16), transparent 20%),
				linear-gradient(180deg, #f9f4ec 0%, var(--bg) 100%);
			min-height: 100vh;
		}
		.shell {
			max-width: 1240px;
			margin: 0 auto;
			padding: 32px 20px 48px;
		}
		.hero {
			background: linear-gradient(135deg, rgba(255,255,255,0.84), rgba(255,248,238,0.92));
			border: 1px solid var(--border);
			border-radius: 28px;
			padding: 28px;
			box-shadow: var(--shadow);
			backdrop-filter: blur(10px);
		}
		.eyebrow {
			display: inline-flex;
			padding: 6px 12px;
			border-radius: 999px;
			background: var(--accent-soft);
			color: var(--accent);
			font-size: 13px;
			font-weight: 700;
			letter-spacing: 0.04em;
		}
		h1 {
			margin: 16px 0 10px;
			font-size: clamp(32px, 5vw, 48px);
			line-height: 1.08;
		}
		.summary {
			margin: 0;
			font-size: 16px;
			line-height: 1.7;
			color: var(--muted);
			max-width: 860px;
		}
		.verdict {
			margin-top: 18px;
			padding: 16px 18px;
			border-radius: 18px;
			border: 1px solid var(--border);
			background: rgba(255,255,255,0.82);
		}
		.verdict-title {
			margin: 0 0 6px;
			font-size: 18px;
			font-weight: 800;
		}
		.verdict-copy {
			margin: 0;
			line-height: 1.7;
			color: var(--muted);
		}
		.verdict-status-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
			gap: 12px;
			margin-top: 14px;
		}
		.verdict-status-item {
			padding: 12px 14px;
			border-radius: 14px;
			border: 1px solid rgba(64, 51, 37, 0.12);
			background: rgba(255,255,255,0.72);
		}
		.verdict-status-label {
			font-size: 12px;
			font-weight: 700;
			letter-spacing: 0.04em;
			text-transform: uppercase;
			color: var(--muted);
		}
		.verdict-status-value {
			margin-top: 6px;
			font-size: 15px;
			font-weight: 700;
			line-height: 1.5;
		}
		.verdict-status-value.pass {
			color: var(--success);
		}
		.verdict-status-value.caution {
			color: #b45309;
		}
		.verdict-status-value.fail {
			color: var(--danger);
		}
		.verdict.not-ready {
			border-color: rgba(180, 35, 24, 0.28);
			background: var(--danger-soft);
		}
		.verdict.not-ready .verdict-title {
			color: var(--danger);
		}
		.verdict.caution {
			border-color: rgba(180, 83, 9, 0.28);
			background: rgba(251, 191, 36, 0.10);
		}
		.verdict.caution .verdict-title {
			color: #b45309;
		}
		.verdict.ready {
			border-color: rgba(22, 101, 52, 0.24);
			background: var(--success-soft);
		}
		.verdict.ready .verdict-title {
			color: var(--success);
		}
		.meta {
			display: flex;
			flex-wrap: wrap;
			gap: 12px 18px;
			margin-top: 18px;
			color: var(--muted);
			font-size: 14px;
		}
		.cards {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
			gap: 16px;
			margin-top: 22px;
		}
		.card {
			background: var(--card);
			border: 1px solid var(--border);
			border-radius: 20px;
			padding: 18px;
			box-shadow: 0 8px 24px rgba(58, 42, 25, 0.08);
		}
		.card .label {
			font-size: 13px;
			color: var(--muted);
		}
		.card .value {
			margin-top: 10px;
			font-size: 32px;
			font-weight: 800;
			line-height: 1;
		}
		.card .hint {
			margin-top: 10px;
			font-size: 13px;
			color: var(--muted);
		}
		.section {
			margin-top: 24px;
			background: var(--panel);
			border: 1px solid var(--border);
			border-radius: 24px;
			padding: 22px;
			box-shadow: var(--shadow);
		}
		.section-head {
			display: flex;
			flex-wrap: wrap;
			justify-content: space-between;
			gap: 14px;
			align-items: center;
			margin-bottom: 18px;
		}
		.section h2 {
			margin: 0;
			font-size: 24px;
		}
		.toolbar {
			display: flex;
			flex-wrap: wrap;
			gap: 10px;
		}
		.toolbar input,
		.toolbar button {
			border: 1px solid var(--border);
			border-radius: 999px;
			padding: 10px 14px;
			font: inherit;
			background: #fffdf9;
			color: var(--text);
		}
		.toolbar button {
			cursor: pointer;
			transition: transform 160ms ease, background 160ms ease, border-color 160ms ease;
		}
		.toolbar button.active {
			background: var(--accent);
			border-color: var(--accent);
			color: white;
		}
		.toolbar button:hover { transform: translateY(-1px); }
		.scenario-list {
			display: grid;
			gap: 14px;
		}
		.scenario {
			border: 1px solid var(--border);
			border-radius: 18px;
			padding: 18px;
			background: rgba(255,255,255,0.84);
		}
		.scenario.failed { border-color: rgba(180, 35, 24, 0.28); background: var(--danger-soft); }
		.scenario.passed { border-color: rgba(22, 101, 52, 0.24); }
		.scenario-head {
			display: flex;
			justify-content: space-between;
			align-items: flex-start;
			gap: 12px;
			margin-bottom: 10px;
		}
		.scenario-name {
			margin: 0;
			font-size: 18px;
			font-weight: 700;
			word-break: break-word;
		}
		.badge {
			display: inline-flex;
			align-items: center;
			gap: 6px;
			border-radius: 999px;
			padding: 6px 10px;
			font-size: 12px;
			font-weight: 700;
			white-space: nowrap;
		}
		.badge.pass { background: var(--success-soft); color: var(--success); }
		.badge.fail { background: var(--danger-soft); color: var(--danger); }
		.scenario p {
			margin: 0;
			color: var(--muted);
			line-height: 1.7;
		}
		.pill-row {
			display: flex;
			flex-wrap: wrap;
			gap: 8px;
			margin-top: 12px;
		}
		.pill {
			padding: 6px 10px;
			border-radius: 999px;
			background: rgba(64, 51, 37, 0.06);
			font-size: 12px;
			color: var(--muted);
		}
		.list {
			display: grid;
			gap: 10px;
			margin: 0;
			padding: 0;
			list-style: none;
		}
		.list li {
			padding: 12px 14px;
			border: 1px solid var(--border);
			border-radius: 14px;
			background: rgba(255,255,255,0.8);
			line-height: 1.6;
			color: var(--muted);
		}
		.todo-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
			gap: 14px;
		}
		.todo-card {
			padding: 18px;
			border-radius: 18px;
			border: 1px solid var(--border);
			background: rgba(255,255,255,0.84);
		}
		.todo-card h3 {
			margin: 0 0 10px;
			font-size: 18px;
		}
		.todo-meta {
			display: flex;
			flex-wrap: wrap;
			gap: 8px;
			margin-bottom: 10px;
		}
		.todo-tag {
			padding: 5px 9px;
			border-radius: 999px;
			font-size: 12px;
			font-weight: 700;
			background: rgba(64, 51, 37, 0.08);
			color: var(--muted);
		}
		.todo-tag.status-done {
			background: var(--success-soft);
			color: var(--success);
		}
		.todo-tag.status-in-progress {
			background: rgba(15, 118, 110, 0.12);
			color: var(--accent);
		}
		.todo-tag.status-not-started {
			background: rgba(194, 65, 12, 0.12);
			color: #9a3412;
		}
		.todo-tag.status-partial {
			background: rgba(180, 83, 9, 0.12);
			color: #b45309;
		}
		.notice-list {
			display: grid;
			gap: 12px;
		}
		.notice {
			padding: 16px 18px;
			border-radius: 18px;
			border: 1px solid var(--border);
			background: rgba(255,255,255,0.84);
		}
		.notice.warn {
			border-color: rgba(180, 83, 9, 0.28);
			background: rgba(251, 191, 36, 0.08);
		}
		.notice.critical {
			border-color: rgba(180, 35, 24, 0.28);
			background: var(--danger-soft);
		}
		.notice h3 {
			margin: 0 0 8px;
			font-size: 18px;
		}
		.empty {
			padding: 28px 16px;
			text-align: center;
			color: var(--muted);
			border: 1px dashed var(--border);
			border-radius: 18px;
			background: rgba(255,255,255,0.56);
		}
		@media (max-width: 720px) {
			.shell { padding: 20px 14px 32px; }
			.hero, .section { padding: 18px; border-radius: 20px; }
			.scenario-head { flex-direction: column; }
		}
	</style>
</head>
<body>
	<div class="shell">
		<section class="hero">
			<span class="eyebrow">评测流水线自动生成</span>
			<h1>GoWord 综合验证报告</h1>
			<p id="summary" class="summary"></p>
			<div id="alignment-verdict" class="verdict"></div>
			<div class="meta">
				<span id="generated-at">生成时间：{{ .GeneratedAt }}</span>
				<span id="duration"></span>
				<span id="input-doc"></span>
			</div>
			<div class="cards" id="summary-cards"></div>
		</section>

		<section class="section">
			<div class="section-head">
				<h2>场景结果</h2>
				<div class="toolbar">
					<input id="search" type="search" placeholder="按场景名或消息筛选">
					<button class="active" data-filter="all">全部</button>
					<button data-filter="failed">仅失败</button>
					<button data-filter="passed">仅通过</button>
					<button data-filter="llm">仅真实 LLM</button>
				</div>
			</div>
			<div class="scenario-list" id="scenario-list"></div>
		</section>

		<section class="section">
			<div class="section-head">
				<h2>失败项与调优建议</h2>
			</div>
			<div class="cards">
				<div class="card">
					<div class="label">失败场景</div>
					<ul id="failed-list" class="list"></ul>
				</div>
				<div class="card">
					<div class="label">自动调优建议</div>
					<ul id="tuning-list" class="list"></ul>
				</div>
			</div>
		</section>

		<section class="section">
			<div class="section-head">
				<h2>完全保证阻塞项</h2>
			</div>
			<div id="guarantee-list" class="notice-list"></div>
		</section>

		<section class="section">
			<div class="section-head">
				<h2>Office 全量基准矩阵</h2>
			</div>
			<div id="todo-grid" class="todo-grid"></div>
		</section>
	</div>

	<script>
		const report = {{ .ReportJSON }};
		const roadmap = {{ .RoadmapJSON }};
		const guaranteeNotes = {{ .GuaranteeJSON }};
		const state = { filter: 'all', keyword: '' };

		const summaryEl = document.getElementById('summary');
		const durationEl = document.getElementById('duration');
		const inputDocEl = document.getElementById('input-doc');
		const cardsEl = document.getElementById('summary-cards');
		const verdictEl = document.getElementById('alignment-verdict');
		const scenarioListEl = document.getElementById('scenario-list');
		const failedListEl = document.getElementById('failed-list');
		const tuningListEl = document.getElementById('tuning-list');
		const todoGridEl = document.getElementById('todo-grid');
		const guaranteeListEl = document.getElementById('guarantee-list');
		const searchEl = document.getElementById('search');
		const filterButtons = Array.from(document.querySelectorAll('[data-filter]'));

		function formatDuration(ms) {
			if (!Number.isFinite(ms) || ms <= 0) return '总耗时：0 秒';
			if (ms < 1000) return '总耗时：' + ms + ' ms';
			const totalSeconds = Math.round(ms / 1000);
			const minutes = Math.floor(totalSeconds / 60);
			const seconds = totalSeconds % 60;
			if (minutes === 0) return '总耗时：' + seconds + ' 秒';
			return '总耗时：' + minutes + ' 分 ' + seconds + ' 秒';
		}

		function makeCard(label, value, hint) {
			const card = document.createElement('div');
			card.className = 'card';
			card.innerHTML = '<div class="label">' + label + '</div><div class="value">' + value + '</div><div class="hint">' + hint + '</div>';
			return card;
		}

		function roadmapStats() {
			const stats = { done: 0, partial: 0, 'in-progress': 0, 'not-started': 0 };
			if (!Array.isArray(roadmap)) return stats;
			roadmap.forEach((item) => {
				const key = item && item.status ? item.status : 'not-started';
				stats[key] = (stats[key] || 0) + 1;
			});
			return stats;
		}

		function computeAlignmentVerdict() {
			const stats = roadmapStats();
			const remaining = (stats.partial || 0) + (stats['in-progress'] || 0) + (stats['not-started'] || 0);
			const hasFailures = (report.failed || 0) > 0;
			const matrixReady = !hasFailures;
			const claimReady = matrixReady && remaining === 0;
			if (hasFailures) {
				return {
					tone: 'not-ready',
					matrixReady,
					claimReady,
					title: '测试矩阵仍有失败项',
					copy: '当前仍有 ' + report.failed + ' 个失败场景，已列矩阵还不能视为全绿；因此也不能给出更强的对齐声明。',
					matrixLabel: '仍有 ' + report.failed + ' 个失败场景',
					matrixTone: 'fail',
					claimLabel: remaining > 0 ? '保持保守口径，且仍有 ' + remaining + ' 项路线图未收口' : '保持保守口径，暂不宣称完全对齐',
					claimTone: 'fail',
					claimHint: '先清空失败场景，再讨论更强的对齐结论。'
				};
			}
			if (remaining > 0) {
				return {
					tone: 'caution',
					matrixReady,
					claimReady,
					title: '测试矩阵已全绿，但仍不宣称 100% 完全对齐',
					copy: '当前场景结果已经没有失败项，但 Office 基准矩阵仍有 ' + remaining + ' 项未完成或仅部分就绪，所以这里只能给出“测试矩阵全绿”的结论，不能外推成无限 Office 组合空间的完全正确。',
					matrixLabel: '已全绿（0 个失败场景）',
					matrixTone: 'pass',
					claimLabel: '保持保守口径，仍有 ' + remaining + ' 项路线图未收口',
					claimTone: 'caution',
					claimHint: '这是“矩阵通过”而不是“100% 完全对齐”的声明。'
				};
			}
			return {
				tone: 'ready',
				matrixReady,
				claimReady,
				title: '在当前已列基准矩阵内可宣称强对齐',
				copy: '当前场景全部通过，且路线图项已全部收口；因此可以对当前已列出的能力矩阵给出强对齐结论，但这仍不等于对无限 Office 组合空间作出绝对保证。',
				matrixLabel: '已全绿（0 个失败场景）',
				matrixTone: 'pass',
				claimLabel: '可对当前已列矩阵宣称强对齐',
				claimTone: 'pass',
				claimHint: '结论范围仅限当前已列出的能力矩阵。'
			};
		}

		function renderSummary() {
			const verdict = computeAlignmentVerdict();
			summaryEl.textContent = report.summary || '本次评测已完成。';
			verdictEl.className = 'verdict ' + verdict.tone;
			verdictEl.innerHTML = '<div class="verdict-title">' + verdict.title + '</div>' +
				'<p class="verdict-copy">' + verdict.copy + '</p>' +
				'<div class="verdict-status-grid">' +
					'<div class="verdict-status-item"><div class="verdict-status-label">测试矩阵</div><div class="verdict-status-value ' + verdict.matrixTone + '">' + verdict.matrixLabel + '</div></div>' +
					'<div class="verdict-status-item"><div class="verdict-status-label">对齐声明</div><div class="verdict-status-value ' + verdict.claimTone + '">' + verdict.claimLabel + '</div></div>' +
				'</div>';
			durationEl.textContent = formatDuration(report.duration_ms || 0);
			inputDocEl.textContent = report.input_document_path ? '输入文档：' + report.input_document_path : '';
			cardsEl.innerHTML = '';
			cardsEl.appendChild(makeCard('总场景', report.scenarios.length, '通过 ' + report.passed + ' / 失败 ' + report.failed));
			cardsEl.appendChild(makeCard('自动调优', report.auto_tuning_applied || 0, '建议数 ' + (Array.isArray(report.tuning_actions) ? report.tuning_actions.length : 0)));
			const stats = roadmapStats();
			cardsEl.appendChild(makeCard('矩阵完成度', (stats.done || 0) + '/' + roadmap.length, '部分就绪 ' + (stats.partial || 0) + '，未开始 ' + (stats['not-started'] || 0)));
			cardsEl.appendChild(makeCard('对齐声明', verdict.claimReady ? '当前矩阵强对齐' : '保持保守口径', verdict.claimHint));
			if (report.model_e2e) {
				cardsEl.appendChild(makeCard('真实 LLM', report.model_e2e.passed + '/' + report.model_e2e.scenario_count, report.model_e2e.model + '，失败 ' + report.model_e2e.failed));
			}
			const failRate = report.scenarios.length === 0 ? '0%' : String(Math.round((report.failed / report.scenarios.length) * 100)) + '%';
			cardsEl.appendChild(makeCard('失败占比', failRate, verdict.matrixReady ? '测试矩阵当前无失败场景' : '测试矩阵仍有失败项'));
		}

		function scenarioMatchesFilter(item) {
			if (state.filter === 'failed' && item.passed) return false;
			if (state.filter === 'passed' && !item.passed) return false;
			if (state.filter === 'llm' && !item.name.startsWith('llm_')) return false;
			if (!state.keyword) return true;
			const haystack = (item.name + ' ' + (item.message || '') + ' ' + (item.error || '')).toLowerCase();
			return haystack.includes(state.keyword);
		}

		function renderScenarios() {
			scenarioListEl.innerHTML = '';
			const filtered = report.scenarios.filter(scenarioMatchesFilter);
			if (filtered.length === 0) {
				scenarioListEl.innerHTML = '<div class="empty">当前筛选条件下没有场景。</div>';
				return;
			}
			filtered.forEach((item) => {
				const node = document.createElement('article');
				node.className = 'scenario ' + (item.passed ? 'passed' : 'failed');
				const detailPills = [];
				detailPills.push('耗时 ' + (item.duration_ms || 0) + ' ms');
				if (item.auto_tuned) detailPills.push('含自动调优');
				if (item.artifact_path) detailPills.push('产物 ' + item.artifact_path);
				if (item.details && item.details.budget_status) detailPills.push('budget ' + item.details.budget_status);
				node.innerHTML =
					'<div class="scenario-head">' +
						'<h3 class="scenario-name">' + item.name + '</h3>' +
						'<span class="badge ' + (item.passed ? 'pass' : 'fail') + '">' + (item.passed ? '通过' : '失败') + '</span>' +
					'</div>' +
					'<p>' + (item.message || item.error || '无附加消息') + '</p>' +
					(item.error ? '<p style="margin-top:10px;color:var(--danger);">' + item.error + '</p>' : '') +
					'<div class="pill-row">' + detailPills.map((text) => '<span class="pill">' + text + '</span>').join('') + '</div>';
				scenarioListEl.appendChild(node);
			});
		}

		function renderLists() {
			const failedItems = report.scenarios.filter((item) => !item.passed);
			failedListEl.innerHTML = failedItems.length
				? failedItems.map((item) => '<li><strong>' + item.name + '</strong><br>' + (item.error || item.message || '无错误描述') + '</li>').join('')
				: '<li>当前没有失败场景。</li>';
			const tuningItems = Array.isArray(report.tuning_actions) ? report.tuning_actions : [];
			tuningListEl.innerHTML = tuningItems.length
				? tuningItems.map((item) => '<li>' + item + '</li>').join('')
				: '<li>当前没有自动调优建议。</li>';
		}

		function renderTodoRoadmap() {
			if (!Array.isArray(roadmap) || roadmap.length === 0) {
				todoGridEl.innerHTML = '<div class="empty">当前没有待办项。</div>';
				return;
			}
			todoGridEl.innerHTML = roadmap.map((item) =>
				'<article class="todo-card">' +
					'<h3>' + item.title + '</h3>' +
					'<div class="todo-meta">' +
						'<span class="todo-tag">' + item.area + '</span>' +
						'<span class="todo-tag">' + item.priority + '</span>' +
						'<span class="todo-tag status-' + item.status + '">' +
							(item.status === 'done' ? '已完成' : (item.status === 'in-progress' ? '进行中' : (item.status === 'partial' ? '部分就绪' : '未开始'))) +
						'</span>' +
					'</div>' +
					'<p>' + item.description + '</p>' +
					'<div class="pill-row"><span class="pill">验证：' + item.verify + '</span><span class="pill">证据：' + item.evidence + '</span></div>' +
				'</article>'
			).join('');
		}

		function renderGuaranteeNotes() {
			if (!Array.isArray(guaranteeNotes) || guaranteeNotes.length === 0) {
				guaranteeListEl.innerHTML = '<div class="empty">当前没有阻塞项说明。</div>';
				return;
			}
			guaranteeListEl.innerHTML = guaranteeNotes.map((item) =>
				'<article class="notice ' + item.severity + '">' +
					'<h3>' + item.title + '</h3>' +
					'<p>' + item.description + '</p>' +
				'</article>'
			).join('');
		}

		searchEl.addEventListener('input', (event) => {
			state.keyword = event.target.value.trim().toLowerCase();
			renderScenarios();
		});

		filterButtons.forEach((button) => {
			button.addEventListener('click', () => {
				state.filter = button.dataset.filter;
				filterButtons.forEach((item) => item.classList.toggle('active', item === button));
				renderScenarios();
			});
		});

		renderSummary();
		renderScenarios();
		renderLists();
		renderGuaranteeNotes();
		renderTodoRoadmap();
	</script>
</body>
</html>`)
	if err != nil {
		return fmt.Errorf("解析 HTML 模板失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("创建 HTML 报告目录失败: %w", err)
	}
	var html bytes.Buffer
	if err := tmpl.Execute(&html, view); err != nil {
		return fmt.Errorf("渲染 HTML 报告失败: %w", err)
	}
	if err := os.WriteFile(outputPath, html.Bytes(), 0o644); err != nil {
		return fmt.Errorf("写入 HTML 报告失败: %w", err)
	}
	return nil
}

func buildOfficeAlignmentTodoList(result *report) []officeAlignmentTodoItem {
	statusForScenario := func(name string) string {
		if result == nil {
			return "not-started"
		}
		for _, scenario := range result.Scenarios {
			if scenario.Name == name {
				if scenario.Passed {
					return "done"
				}
				return "in-progress"
			}
		}
		return "not-started"
	}
	statusForBudgetScenario := func(name string) string {
		if result == nil {
			return "not-started"
		}
		for _, scenario := range result.Scenarios {
			if scenario.Name != name {
				continue
			}
			if !scenario.Passed {
				return "in-progress"
			}
			if details := scenario.Details; details != nil {
				if budgetStatus, ok := details["budget_status"].(string); ok {
					switch strings.ToLower(strings.TrimSpace(budgetStatus)) {
					case "passed":
						return "done"
					case "warning":
						return "partial"
					case "failed":
						return "in-progress"
					}
				}
			}
			return "partial"
		}
		return "not-started"
	}
	item := func(title, area, priority, status, description, verify, evidence string) officeAlignmentTodoItem {
		return officeAlignmentTodoItem{
			Title:       title,
			Area:        area,
			Priority:    priority,
			Status:      status,
			Description: description,
			Verify:      verify,
			Evidence:    evidence,
		}
	}
	return []officeAlignmentTodoItem{
		item("默认页眉改写基准", "Header/Footer Variant", "P0", statusForScenario("llm_e2e_header_hit_rewrite"), "验证 header:default 的命中、改写与正文隔离。", "真实 LLM e2e 通过，update_header_footer 动作稳定。", "已有真实 LLM e2e + reopen 校验。"),
		item("首页页眉与默认页眉区分基准", "Header/Footer Variant", "P0", statusForScenario("llm_e2e_first_page_header_variant_rewrite"), "验证 TitlePage + header:first 语义，确保首页页眉可独立编辑。", "真实 LLM e2e 通过，header:first 与 header:default 持久化结果分离。", "已有真实 LLM e2e + TitlePage roundtrip。"),
		item("奇偶页与首页页眉页脚分流", "Header/Footer Variant", "P0", statusForScenario("llm_e2e_header_footer_variant_mixed_isolation"), "继续覆盖 first/even/default 三种 variant 混合编辑，防止 variant 混淆。", "reader 命中的 header_footer_variant 与最终动作一一对应。", "真实 LLM e2e 覆盖 default/first/even 三种 variant 的混合更新与复开校验。"),
		item("节级页面设置格式化", "Section Layout", "P0", statusForScenario("llm_e2e_section_layout_orientation_margin_format"), "补 format_section 的 LLM e2e，覆盖横纵向、页边距、节级分页设置。", "save/reopen 后 section_layout、reader 和 layout route 一致。", "真实 LLM e2e 覆盖 orientation 与 margin patch，并保留页眉页脚与正文不串改。"),
		item("页码起始值与编号格式", "Page Number", "P0", statusForScenario("llm_e2e_page_number_start_format"), "补 page_number_start 与 page_number_format 的真实场景，覆盖罗马数字与重新编号。", "section XML、reader 结构和 reopen 后文档元数据完全一致。", "真实 LLM e2e + include_layout 取证 + reopen section_layout 校验。"),
		item("正文精准替换", "Paragraph Editing", "P0", statusForScenario("replace_body_real_doc"), "验证普通正文 replace 不破坏样式与结构。", "save/reopen 后段落 hash 与样式快照一致。", "已有真实文档链路校验。"),
		item("标题与正文插入", "Paragraph Editing", "P0", statusForScenario("insert_peer_heading"), "覆盖同级标题、新正文和标题后正文插入模板选择。", "Heading/Normal 模板落点与保存复开一致。", "已有真实 LLM/非 LLM 场景。"),
		item("标签段后正文与空格保留", "Paragraph Editing", "P0", statusForScenario("llm_e2e_bold_label_body_insert_whitespace"), "覆盖短加粗标签段后新增正文时的模板切换，并验证前导空格不丢失。", "新增正文沿用后续正文样式，且 save/reopen 后仍保留前导空格。", "新增真实 LLM e2e。"),
		item("列表与多级编号连续性", "Numbering", "P0", statusForScenario("llm_e2e_multilevel_list_mid_insert_continuation"), "覆盖一级列表、二级列表、中间插入后续号连续显示。", "编号显示、level、num_id 与保存复开一致。", "已有真实 LLM e2e。"),
		item("段落断开与段内换行", "Paragraph Structure", "P0", statusForScenario("llm_e2e_line_break_dialogue"), "覆盖 Enter 与 Shift+Enter 的动作分流。", "动作类型、保存复开后的段落结构与文本完全一致。", "已有真实 LLM e2e。"),
		item("run 级富文本强调", "Inline Formatting", "P0", statusForScenario("llm_e2e_inline_emphasis"), "覆盖字体、字号、颜色、加粗等 run 级样式编辑。", "run 样式快照与目标短语范围完全一致。", "已有真实 LLM e2e。"),
		item("表格整行插入", "Table Layout", "P0", statusForScenario("llm_e2e_insert_table_row_structured"), "覆盖 insert_table_row 的真实对话链路，防止 table_row 退化成空白行。", "新增行四个单元格全部落地，且正文段保持 TableBody 样式。", "新增真实 LLM e2e + reopen 校验。"),
		item("表格单元格整格改写", "Table Layout", "P0", statusForScenario("llm_e2e_expanded_replace_same_table_cell"), "覆盖同格正文扩写、单元格整格 update_table_cell 语义。", "table_cell 结构、样式、复开结果一致。", "已有真实 LLM e2e。"),
		item("跨页表格与重复表头", "Table Layout", "P0", statusForScenario("llm_e2e_cross_page_repeat_header_visual_update"), "覆盖分页后表格分裂、重复表头和视觉布局。", "layout route、repeat_header 元数据与保存复开一致。", "已有真实 LLM e2e + 语义布局校验。"),
		item("复杂表格网格与合并单元格", "Table Layout", "P0", statusForScenario("ooxml_object_summary_complex_structures"), "补跨列跨行、合并单元格、嵌套表格与单元格多段混合样式。", "至少先验证 merged/nested table 的对象摘要、保存复开与原始 XML 不丢。", "已有复杂表格对象摘要 + save/reopen 保真基线；真实 LLM e2e 可继续增强。"),
		item("跨区域批处理正确性", "Batch Workflow", "P1", statusForScenario("llm_e2e_cross_region_batch"), "覆盖页眉/正文/表格混合编辑的顺序拆分与正确落点。", "所有最小动作顺序执行且无串改。", "已有真实 LLM e2e，但预算仍有 warning。"),
		item("跨区域批处理预算收敛", "Agent Efficiency", "P1", statusForBudgetScenario("llm_e2e_cross_region_batch"), "继续压缩多区域任务的 reader/copilot 成本，去掉 warning。", "budget_status 从 warning 收敛到 passed。", "真实 LLM e2e 预算评估与场景结果联动。"),
		item("图片 inline / legacy pict 保真", "Drawing Objects", "P1", statusForScenario("ooxml_object_summary_image_variants"), "覆盖内联图片、linked drawing、legacy pict 的读写保真。", "至少先验证图片对象摘要、关系和保存复开不丢。", "已有 inline/anchor/legacy pict 对象摘要 + save/reopen 保真基线；真实 LLM e2e 可继续增强。"),
		item("浮动图片、文本框、形状", "Drawing Objects", "P1", statusForScenario("llm_e2e_drawing_objects_body_edit_preserves_objects"), "补浮动锚点、文本框和形状对象的编辑与布局基准。", "真实 LLM 编辑通过后，textbox/shape/anchor 仍可在 save/reopen 后完整保留。", "已有 anchor 图片 + textbox/shape 对象摘要基线，并新增真实 LLM e2e 验证正文编辑后对象 XML、关系与摘要仍完整保留。"),
		item("超链接、书签、目录、交叉引用", "Field Objects", "P1", statusForScenario("ooxml_object_summary_complex_structures"), "把 Word 字段类对象纳入模型与回归基准。", "先验证 hyperlink/bookmark/field 的对象摘要与 no-op save 保真，再逐步补可编辑能力。", "已有 hyperlink/bookmark/field 对象摘要 + save/reopen 保真基线；真实引用编辑场景可继续增强。"),
		item("脚注、尾注、批注、修订痕迹", "Review Objects", "P1", statusForScenario("ooxml_object_summary_complex_structures"), "把 footnote、endnote、comment、track changes 拉进真实基线。", "先验证 review 类对象摘要与 no-op save 保真，再逐步扩到可编辑能力。", "已有 footnote/endnote/comment/revision 对象摘要 + save/reopen 保真基线；真实编辑场景可继续增强。"),
		item("多节混合布局文档", "Section Layout", "P1", statusForScenario("llm_e2e_multi_section_body_edit_preserves_sections"), "覆盖多 section 文档的节间切换、不同方向与页眉页脚继承。", "每节 layout/header_footer 独立正确，跨节不串改。", "真实 LLM e2e 在多节样本上执行正文编辑，并验证 section break、方向与页眉页脚完整保留。"),
		item("歧义指令拒答与澄清", "Agent Guardrail", "P1", statusForScenario("llm_e2e_ambiguous_instruction_clarify_then_edit"), "补多命中、证据不足、跨区域歧义任务的负向 e2e。", "缺证据时拒绝执行或追问，不生成猜测性动作。", "真实 LLM e2e 覆盖先拒动澄清，再在用户补充后准确编辑。"),
		item("真实 Office / PDF 视觉基线", "Visual Baseline", "P0", statusForScenario("word_pdf_visual_baseline_oracle"), "引入 Word/PDF 页面对照，作为语义布局之外的最终裁判。", "原始 DOCX 与 roundtrip DOCX 都走真实 Word 导出，并完成逐页页图 diff。", "已接入真实 Word -> PDF 导出、页图渲染与逐页 diff；当前基线要求 roundtrip 后各页图像逐页一致。"),
		item("大文档与长链编辑稳定性", "Scalability", "P2", statusForScenario("llm_e2e_large_document_long_chain"), "补 50 到 200 页、多 section、多表格长链任务，验证性能与稳定性。", "大文档编辑正确率、耗时和内存曲线可控。", "真实 LLM e2e 在 50+ 页长文档样本上验证双点位长链改写与复开布局页数。"),
	}
}

func buildOfficeGuaranteeNotes() []officeGuaranteeNote {
	return []officeGuaranteeNote{
		{
			Title:       "没有真实 Office 渲染裁判，就不能宣称 100% 完全对齐",
			Severity:    "critical",
			Description: "当前主验证链路是 go-word save/reopen、reader 结构、layout route 和 LLM e2e。它们足以证明主干编辑正确性，但还不能等价为 Microsoft Word 原生渲染零偏差。要宣称完全准确，必须补 Word/PDF/截图级视觉 oracle。",
		},
		{
			Title:       "任何有限测试集都不能证明‘所有 Office 能力都完全正确’",
			Severity:    "critical",
			Description: "Office 能力面包含节、字段、对象、批注、修订、图形、域、复杂表格和多节继承等大量组合。只有当全量基准矩阵全部完成并稳定通过，才能把结论收敛为‘在已列能力面内达到强保证’，不能诚实地宣称无限域完全正确。",
		},
		{
			Title:       "底层 roundtrip 存在不等于真实 LLM e2e 已闭环",
			Severity:    "warn",
			Description: "像 section_layout、page number、image roundtrip 这类底层能力已经有解析或保存测试，但如果没有真实 LLM e2e，仍不能证明 agent 会稳定选择正确动作、填对结构化 payload 并在真实对话里命中正确对象。",
		},
		{
			Title:       "全绿结果仍需要预算与负向场景约束",
			Severity:    "warn",
			Description: "当前已有场景全绿，但仍存在批处理 warning 和缺少系统化负向集的问题。没有这两层约束，就无法把‘能做对’提升成‘稳定、低漂移且不会盲改’。",
		},
	}
}

func newLiveModelRunner(cfg config, apiBaseURL string) (*liveModelRunner, error) {
	if strings.TrimSpace(cfg.E2EModel) == "" {
		return nil, fmt.Errorf("e2e model is empty")
	}
	if strings.TrimSpace(apiBaseURL) == "" {
		return nil, fmt.Errorf("api base url is empty")
	}

	previousBaseURL, hadBaseURL := os.LookupEnv("AURAMATE_API_BASE_URL")
	if err := os.Setenv("AURAMATE_API_BASE_URL", apiBaseURL); err != nil {
		return nil, fmt.Errorf("设置 AURAMATE_API_BASE_URL 失败: %w", err)
	}
	restoreEnv := func() {
		if hadBaseURL {
			_ = os.Setenv("AURAMATE_API_BASE_URL", previousBaseURL)
			return
		}
		_ = os.Unsetenv("AURAMATE_API_BASE_URL")
	}

	if _, err := appconfig.Load("", false); err != nil {
		restoreEnv()
		return nil, fmt.Errorf("加载后端配置失败: %w", err)
	}

	conn, err := db.Connect()
	if err != nil {
		restoreEnv()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	application, err := auraapp.New(context.Background(), conn)
	if err != nil {
		_ = conn.Close()
		restoreEnv()
		return nil, fmt.Errorf("初始化真实应用失败: %w", err)
	}

	return &liveModelRunner{
		app:        application,
		conn:       conn,
		model:      cfg.E2EModel,
		timeout:    cfg.E2ETimeout,
		restoreEnv: restoreEnv,
	}, nil
}

func (r *liveModelRunner) Close() error {
	if r == nil {
		return nil
	}
	var errs []string
	if r.app != nil {
		if err := r.app.Shutdown(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if r.conn != nil {
		if err := r.conn.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if r.restoreEnv != nil {
		r.restoreEnv()
		r.restoreEnv = nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func runLLMEndToEndSuite(ctx *evalContext) ([]scenarioResult, *modelE2ESummary) {
	started := time.Now()
	scenarios := []llmDialogueScenario{
		{
			Name:            "llm_e2e_middle_note_dialogue",
			UserInstruction: "把中间那条说明改成审阅说明，内容别变。",
			PromptHints: []string{
				"这句话里的‘中间那条说明’特指正文里包含‘人工复核’的那条说明段，不是章节标题‘三、交付说明’，也不是末尾的‘附录说明’。",
				"不要在第一次 query 命中后直接编辑；至少再做一次 auramate-word-reader exploration，把包含‘人工复核’的那条正文和前后邻段确认清楚，再决定 target_text。",
				"最终调用 auramate-word-copilot 时，必须把这次 exploration 的确认结果写入 exploration_summary，至少说明已锁定包含‘人工复核’的正文说明段，并说明相邻段落没有被误选。",
				"如果 auramate-word-reader 返回了包含‘人工复核’的 query_hit，必须把该命中的完整原句作为 target_text，并把对应 paragraph_id 回填到 preferred_paragraph_id。",
				"这次修改只允许调整该条说明的前缀，不允许把标题改成‘三、审阅说明’，也不允许改动说明正文‘当前版本仍需人工复核。’。",
			},
			ArtifactName:              "llm-e2e-middle-note.docx",
			MinReaderCalls:            1,
			RequireExplorationSummary: true,
			Suggestions: []string{
				"若真实模型误改了其它说明段，应优先强化 reader 的二次 exploration 约束，让人工复核线索和相邻段落信息成为落点确认前置条件。",
			},
			Verify: verifyLLMMiddleNoteDocument,
		},
		{
			Name:            "llm_e2e_fuzzy_fee_insert",
			UserInstruction: "把交付说明里第三方费用那条后面补一句，需要提前走采购审批。",
			PromptHints: []string{
				"这次修改只能新增一句审批说明，必须使用 action=insert + position=after；不要把现有第三方费用说明改写成别的句子。",
				"target_text 必须直接使用第三方费用那条的完整原句“第三方费用由甲方另行承担，并在采购前单独确认。”；不要落到差旅费用，也不要只写‘第三方费用’这几个字。",
				"新增说明必须单独成段，优先写成“需在采购前完成审批备案。”；不要把审批要求直接拼回原句里。",
			},
			ArtifactName:   "llm-e2e-fuzzy-fee.docx",
			ExpectedAction: "insert",
			Suggestions: []string{
				"若真实模型把审批说明插错到差旅费用后，应优先检查 reader 返回的多命中排序和 copilot 的约束提示是否足够明确。",
			},
			Verify: verifyLLMFeeInsertDocument,
		},
		{
			Name:            "llm_e2e_list_explanation_insert",
			UserInstruction: "在“完成样式评审”后面补一句“需同步记录审批结论。”，这是普通说明正文，不要带编号。",
			PromptHints: []string{
				"这次修改只能新增一句说明正文，必须使用 action=insert + position=after；不要改写现有列表项，也不要生成新的编号条目。",
				"target_text 必须直接使用列表项原文“完成样式评审”；不要只写‘样式评审’或‘第二步’这类模糊锚点。",
				"新增句必须单独成段，正文固定为“需同步记录审批结论。”，不要追加别的说明。",
				"这句新增内容属于非编号正文，最终调用 auramate-word-copilot 时必须显式提供 paragraph_style_patch，至少包含 style_id=Normal，禁止让执行层沿用列表编号。",
				"format_basis 必须明确说明：新增内容沿用列表后的说明正文模板，而不是列表项模板；至少写清非编号正文、style_id=Normal、宋体 24。",
			},
			ArtifactName:               "llm-e2e-list-explanation.docx",
			ExpectedAction:             "insert",
			ExpectedParagraphStyleID:   "Normal",
			RequireParagraphStylePatch: true,
			MinReaderCalls:             1,
			Suggestions: []string{
				"若真实模型把新增说明错误继承成列表编号，应继续强化‘列表后说明正文必须显式携带 Normal paragraph_style_patch’的约束。",
			},
			Verify: verifyLLMListExplanationInsertDocument,
		},
		{
			Name:            "llm_e2e_bold_label_body_insert_whitespace",
			UserInstruction: "在“基本原则”后面新增一条“ 新增一条：坚持闭环验收。”，保留开头那个空格，内容按正文样式，不要加粗。",
			PromptHints: []string{
				"这次修改只能新增一条正文说明，必须使用 action=insert + position=after；不要改写“基本原则”原文，也不要把新增内容做成标题。",
				"target_text 必须直接使用原文“基本原则”；不要只写‘原则’或‘那个标题标签’。",
				"new_text 必须精确保留为“ 新增一条：坚持闭环验收。”，开头那个半角空格不能丢，也不要改成没有空格的版本。",
				"先用 auramate-word-reader 确认“基本原则”后面的相邻正文段仍是“坚持问题导向，确保制度可执行。”，这次新增内容必须沿用这段正文的样式，而不是沿用加粗标签“基本原则”。",
				"最终调用 auramate-word-copilot 时，必须显式提供 paragraph_style_patch，至少包含 style_id=Normal。",
				"format_basis 必须明确说明：新增段落沿用后续正文段的样式证据，而不是‘基本原则’标签样式；至少写清 style_id=Normal、宋体 24、非加粗、首行缩进。",
			},
			InputDocBuilder:            buildBoldLabelInsertFixture,
			ArtifactName:               "llm-e2e-bold-label-body-insert-whitespace.docx",
			ExpectedAction:             "insert",
			ExpectedParagraphStyleID:   "Normal",
			RequireParagraphStylePatch: true,
			MinReaderCalls:             1,
			Suggestions: []string{
				"若真实模型继续把新增内容继承成加粗标签样式，应优先检查 reader 证据是否明确锁定了后续正文模板，以及 copilot 是否显式带了 Normal paragraph_style_patch。",
				"若真实模型仍吞掉开头空格，应优先检查生成动作里的 new_text 是否已被模型自行裁剪，而不是先怀疑 save/reopen。",
			},
			Verify: verifyLLMBoldLabelBodyInsertWhitespaceDocument,
		},
		{
			Name:            "llm_e2e_multilevel_list_insert",
			UserInstruction: "在“核对表格样式”后面补一个同级子步骤“确认跨页表头”，保持和当前子步骤同一层级编号。",
			PromptHints: []string{
				"这次修改必须新增一个同级子步骤，使用 action=insert + position=after；不要把它改成普通正文说明，也不要插成一级步骤。",
				"target_text 必须直接使用现有子步骤原文“核对表格样式”；不要只写‘表格样式’或‘那个子步骤’这类模糊锚点。",
				"新增内容固定为“确认跨页表头”。它必须与“核对表格样式”保持同一层级编号，而不是回到一级列表。",
				"先用 auramate-word-reader 确认目标段落本身已经是编号列表子步骤，并结合相邻子步骤判断这次新增仍是同级子步骤；不要把模板回退到普通正文。",
				"如果调用 auramate-word-copilot 时显式提供 paragraph_style_patch，必须保持列表样式而不是 Normal；至少不要把它改成非编号正文。",
			},
			ArtifactName:   "llm-e2e-multilevel-list-insert.docx",
			ExpectedAction: "insert",
			MinReaderCalls: 1,
			Suggestions: []string{
				"若真实模型把同级子步骤错误插成普通正文，应优先强化对锚点现有编号 level 的确认，而不是继续放宽模板推断。",
			},
			Verify: verifyLLMMultilevelListInsertDocument,
		},
		{
			Name:            "llm_e2e_multilevel_list_mid_insert_continuation",
			UserInstruction: "在“核对标题层级”后面插入一个同级子步骤“补齐编号映射”，并保证这一组二级编号显示连续为 1.1、1.2、1.3。",
			PromptHints: []string{
				"这次修改必须是中间插入，且只能新增一个同级二级子步骤，使用 action=insert + position=after；不要把它改成正文说明，也不要插成一级步骤。",
				"target_text 必须直接使用现有子步骤原文“核对标题层级”；不要改‘核对表格样式’，也不要只写‘第一个子步骤’。",
				"新增内容固定为“补齐编号映射”。它必须与前后两个子步骤保持同一 num_id 与 level=1，使显示顺序成为 1.1、1.2、1.3。",
				"先用 auramate-word-reader 确认“核对标题层级”和后续“核对表格样式”都属于同一组二级编号，再生成动作；不要凭正文模板猜测。",
				"如果调用 auramate-word-copilot 时显式提供 paragraph_style_patch，必须保持 ListParagraphLevel2 对应的列表样式，不能回退成 Normal。",
			},
			ArtifactName:   "llm-e2e-multilevel-list-mid-insert-continuation.docx",
			ExpectedAction: "insert",
			MinReaderCalls: 1,
			Suggestions: []string{
				"若真实模型把中间插入后的后续号打断，应优先检查插入位置是否落在目标子步骤后，以及编号样式是否错误回退成普通正文。",
			},
			Verify: verifyLLMMultilevelListMidInsertContinuationDocument,
		},
		{
			Name:            "llm_e2e_heading_insert",
			UserInstruction: "在二、实施步骤后面加一个同级标题，标题叫三、质量门禁，正文先不要动。",
			PromptHints: []string{
				"这次修改是同级二级标题插入，不是正文补段，也不是改写已有标题。",
				"最终调用 auramate-word-copilot 时，必须使用 action=insert，并把现有标题“二、实施步骤”作为 target_text；如果 reader 返回该标题的 paragraph_id，必须回填 preferred_paragraph_id。",
				"标题插入不能只写 format_basis，必须显式提供 paragraph_style_patch，至少包含 style_id=Heading2；否则视为无效动作。",
				"new_text 必须是完整标题“三、质量门禁”，不要顺带生成标题下的正文内容，也不要改动现有“三、交付说明”标题。",
			},
			ArtifactName:               "llm-e2e-heading-insert.docx",
			ExpectedAction:             "insert",
			ExpectedParagraphStyleID:   "Heading2",
			RequireParagraphStylePatch: true,
			Suggestions: []string{
				"若真实模型仍反复 reader 却不给标题动作，应继续强化标题 insert 必须显式携带 Heading 样式补丁的提示与桥层报错。",
			},
			Verify: verifyLLMHeadingInsertDocument,
		},
		{
			Name:            "llm_e2e_tail_remark_dialogue",
			UserInstruction: "把最后那条备注改正式一点，标成归档备注。",
			PromptHints: []string{
				"这份文档里有多条带“说明”字样的段落，这里的“最后那条备注”特指文档末尾那段“附录说明：本报告用于自动化回归。”，不是前面的交付说明或验证说明。",
				"不要在第一次 query 命中后直接编辑；至少再做一次 auramate-word-reader exploration，确认末尾命中及其相邻段落，避免把前面的说明段误当成‘最后那条备注’。",
				"最终调用 auramate-word-copilot 时，必须把 exploration 的落点确认写入 exploration_summary，明确说明目标是附录末尾说明段，而不是正文里的其它说明。",
				"生成 word_editor_action 时，target_text 必须填写 reader 命中的具体原文，优先使用完整原句“附录说明：本报告用于自动化回归。”；不要只写“备注”这类抽象词。",
				"如果 reader 返回了附录说明段的 paragraph_id，必须把它写进 preferred_paragraph_id；正文内容保持不变，只把前缀改成“附录说明（归档备注）：”。",
			},
			ArtifactName:              "llm-e2e-tail-remark.docx",
			MinReaderCalls:            1,
			RequireExplorationSummary: true,
			Suggestions: []string{
				"若真实模型无法把“最后那条备注”映射到附录说明，应优先补强末尾序位词触发 exploration 的规则，而不是只强化第一次检索词。",
			},
			Verify: verifyLLMTailRemarkDocument,
		},
		{
			Name:            "llm_e2e_risk_cell_insert",
			UserInstruction: "在风险提示那格下面补一句，上线前先确认灰度范围。",
			PromptHints: []string{
				"第一次 reader 命中到‘风险提示’后，不要直接编辑；至少再做一次 auramate-word-reader exploration，确认同一格的 row_window、column_cells 和 focus_cell，确保新增内容落在该单元格正文而不是整表其它位置。",
				"最终调用 auramate-word-copilot 时，必须把 exploration_summary 写完整，至少说明 table_id、row_index、cell_index，以及你是根据哪一行哪一列的邻域信息确认这就是‘风险提示那格’。",
				"这次任务优先在同一单元格内使用 insert，target_text 应锁定到正文句‘当前为灰度验证阶段。’，并用 position=after 在该句后补一句；如果改用 update_table_cell，就必须在 table_cell 中同时保留‘风险提示’标题段、原正文段和新增句，不能只回填坐标。",
			},
			ArtifactName:              "llm-e2e-risk-cell.docx",
			MinReaderCalls:            2,
			RequireExplorationSummary: true,
			Suggestions: []string{
				"若真实模型把“那格”理解错成整张表或正文段落，应优先强化表格场景必须先读 exploration.table 的规则，而不是只扩大 query scope。",
			},
			Verify: verifyLLMRiskCellDocument,
		},
		{
			Name:            "llm_e2e_insert_table_row_structured",
			UserInstruction: "在“一、考核维度与权重”表格最后新增一行：安全合规 | 5% | 月度 | 安全规范执行、数据保密措施，保持现有表格正文样式。",
			PromptHints: []string{
				"第一次 reader 不要泛读全文；优先用 auramate-word-reader 结合 query=“工作业绩 | 50% | 月度”或“团队贡献 | 10% | 季度”锁定目标表，再确认最后一行位置。",
				"最终必须使用 action=insert_table_row；不要退化成普通 insert，也不要把整张表改写成 update_table_cell。",
				"生成动作前，必须在 exploration_summary 中明确写出真实 table_id、最后一行 row_index，以及‘新增行插在团队贡献之后’这一依据。这里 row_index 必须引用现有锚点行‘团队贡献’所在的索引 4，并配合 position=after；不要把 row_index 写成插入后的新位置 5。",
				"table_row 必须是结构化 JSON，对每个 cell 都提供 paragraphs；不要再使用旧形状 {\"cells\":[{\"text\":\"安全合规\"}]}。",
				"可直接按这个形状填写 table_row：{\"cells\":[{\"paragraphs\":[{\"text\":\"安全合规\",\"style\":{\"style_id\":\"TableBody\"}}]},{\"paragraphs\":[{\"text\":\"5%\",\"style\":{\"style_id\":\"TableBody\"}}]},{\"paragraphs\":[{\"text\":\"月度\",\"style\":{\"style_id\":\"TableBody\"}}]},{\"paragraphs\":[{\"text\":\"安全规范执行、数据保密措施\",\"style\":{\"style_id\":\"TableBody\"}}]}]}",
				"如果 reader 已确认表格共有 5 行（含表头），最后一条现有数据行是 row_index=4，那么 insert_table_row 的正确写法是 row_index=4 且 position=after；这类动作的 row_index 不是‘插入后行号’。",
				"format_basis 必须明确说明新增行沿用现有表格正文 TableBody 样式，而不是表头样式；至少写清 style_id=TableBody，并说明四列结构保持不变。",
			},
			InputDocBuilder:           buildTableRowInsertFixture,
			ArtifactName:              "llm-e2e-insert-table-row-structured.docx",
			ExpectedAction:            "insert_table_row",
			MinActionCount:            1,
			MinReaderCalls:            1,
			RequireExplorationSummary: true,
			Suggestions: []string{
				"若再次出现 signal 成功但文档里搜不到新增行，应优先检查 table_row 是否退化成空壳 cells，而不是先怀疑 save/reopen。",
				"若新增行内容正确但样式漂移，应优先检查 table_row.paragraphs[].style.style_id 是否显式写成 TableBody。",
			},
			Verify: verifyLLMInsertTableRowStructuredDocument,
		},
		{
			Name:            "llm_e2e_expanded_replace_same_table_cell",
			UserInstruction: "把风险提示那格里的正文补完整一点，改成‘当前为灰度验证阶段，上线前需同步确认回滚预案。’，但仍然保持同一格里的同一段正文，不要新增段落。",
			Stages: []llmDialogueStage{
				{
					Name:            "confirm_same_table_cell_structure",
					UserInstruction: "只做取证：确认风险提示所在单元格的标题段与正文段，确保这次修改仍保持同一格里的同一段正文。当前这一回合不要编辑文档，也不要调用 auramate-word-copilot。",
					PromptHints: []string{
						"至少做一次 auramate-word-reader exploration，确认同一格的 row_window、column_cells 和 focus_cell，明确锁定 table_id、row_index、cell_index。",
						"必须明确记录：该单元格当前包含标题段‘风险提示’和正文段‘当前为灰度验证阶段。’，本次只扩写正文段，不新增段落。",
						"这一回合只允许读取和总结，不允许生成任何 word_editor_action。",
					},
					MinReaderCalls:   1,
					RequireNoAction:  true,
					SkipCopilotCheck: true,
				},
				{
					Name:            "rewrite_same_table_cell",
					UserInstruction: "基于当前确认结果，只把风险提示所在单元格中的正文‘当前为灰度验证阶段。’改成‘当前为灰度验证阶段，上线前需同步确认回滚预案。’，仍然保持同一格里的同一段正文，不要新增段落。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"这次只处理风险提示所在单元格中的正文‘当前为灰度验证阶段。’，不要改表头‘指标’‘当前值’，也不要改右侧结果‘需人工复核’。",
						"不要退化成 replace，也不要用 insert、insert_line_break 或 insert_paragraph_break 新增段落。",
						"生成 word_editor_action 时，target_text 应填写原正文‘当前为灰度验证阶段。’，table_cell 必须一次性给出完整最终单元格内容：至少保留标题段‘风险提示’，并把正文段改成‘当前为灰度验证阶段，上线前需同步确认回滚预案。’。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串，也不要传 {text: ...} 这类空壳对象。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"风险提示\",\"style_id\":\"TableHeader\"},{\"text\":\"当前为灰度验证阶段，上线前需同步确认回滚预案。\",\"style_id\":\"TableBody\"}]}",
						"如果已经拿到了完整 table_id、row_index、cell_index 和最终 table_cell，就必须立刻调用一次 auramate-word-copilot 产出动作；不能停在总结、解释或再次空转。",
						"reader_context_summary 或 format_basis 必须明确说明：已经核对同一单元格的标题段与正文段，确认这次扩写仍保持同一格、同一段正文，不新增段落。",
					},
					ExpectedAction:   "update_table_cell",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:              "llm-e2e-expanded-replace-same-table-cell.docx",
			MinReaderCalls:            2,
			RequireExplorationSummary: false,
			Suggestions: []string{
				"若真实模型把单元格正文扩写误做成 insert 或拆成多段，应继续强化‘同一格内扩写优先 update_table_cell，且必须保留同格单段正文结构’。",
				"若单元格正文扩写仍漂移成 replace，可先加 read-only stage 锁定同一格结构，再让下一 stage 只产出一条 update_table_cell。",
			},
			Verify: verifyLLMExpandedReplaceSameTableCellDocument,
		},
		{
			Name:            "llm_e2e_table_batch_update",
			UserInstruction: "把表格里的通过率改成92%，把右侧结果改成无需人工复核，并在风险提示那格下面补一句，上线前先确认灰度范围。",
			Stages: []llmDialogueStage{
				{
					Name:            "update_pass_rate",
					UserInstruction: "只把表格里的通过率改成92%。这是三步任务的第1步，不要修改右侧结果，也不要补风险提示说明。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"第一次 reader 应优先走表格最小读取路径：query 优先用“通过率”，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，应立即用 focus_table_id 整表读取一次，确认通过率所在单元格，再生成动作。",
						"table_cell 必须明确写出更新后的最终单元格内容 92%，不能只给 row_index/cell_index。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串‘92%’，也不要传 {text: \"92%\"}。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"92%\",\"style_id\":\"TableBody\"}]}",
						"不要修改风险提示同行右侧结果，也不要补‘上线前先确认灰度范围。’。",
					},
					ExpectedAction:   "update_table_cell",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "update_risk_result",
					UserInstruction: "基于当前文档状态，只把风险提示右侧那格改成无需人工复核。不要再改通过率，也不要补风险提示说明。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"这是表格右侧值单元格改写，不是普通段落 replace；即使 query 只命中到单句‘需人工复核’，也必须回到表格语义，显式回填 table_id、row_index、cell_index 和 table_cell，禁止退化成 replace。",
						"第一次 reader 应优先走表格最小读取路径：query 优先用“风险提示”，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，应立即用 focus_table_id 整表读取一次，确认风险提示同行右侧值单元格，再生成动作。",
						"table_cell 必须明确写出更新后的最终单元格内容 无需人工复核，不能只给 row_index/cell_index。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串‘无需人工复核’，也不要传 {text: \"无需人工复核\"}。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"无需人工复核\",\"style_id\":\"TableBody\"}]}",
						"exploration_summary 必须写清这是风险提示同行右侧结果格，并明确给出 row_index/cell_index 与同一行左侧标签‘风险提示’的对应关系。",
						"不要回退去修改通过率，也不要提前补‘上线前先确认灰度范围。’。",
					},
					ExpectedAction:            "update_table_cell",
					ExactActionCount:          1,
					MinReaderCalls:            1,
					RequireExplorationSummary: true,
				},
				{
					Name:            "insert_risk_followup",
					UserInstruction: "基于当前文档状态，只在风险提示那格正文‘当前为灰度验证阶段。’后补一句‘上线前先确认灰度范围。’。不要再改通过率，也不要再改右侧结果。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 insert + position=after；完成后立即停止。",
						"第一次 reader 应优先用“风险提示”做表格最小读取；拿到真实 table_id 后，必须再做 1 次 auramate-word-reader exploration，复用同一 table_id / focus_table_id，明确读取风险提示所在单元格的 row_window、column_cells 和 focus_cell。",
						"第 2 次 reader exploration 是强制项，不是可选项；少于 2 次 reader 就直接补句，视为失败。",
						"这条 insert 必须锁定正文句‘当前为灰度验证阶段。’，不要把锚点落到标题段‘风险提示’。",
						"最终 insert 必须显式回填 table_id、row_index、cell_index，不能只靠正文 target_text 或 paragraph_style_patch 让执行层猜目标格。",
						"这条 insert 必须显式填写 paragraph_style_patch.style_id=TableBody。",
						"如果最终改成 update_table_cell 或需要回填整格内容，table_cell 也必须使用结构化 JSON 对象，优先按 paragraphs 数组写出标题段与正文段；不要传纯字符串或 {text: ...}。",
						"format_basis 必须直接引用同格正文段的真实样式证据，至少写清 style_id=TableBody；若 exploration 已读到字体、字号、段后、行距，也要一起写出，明确新增句继续沿用正文样式。",
						"exploration_summary 必须写清 row_index/cell_index 和同行、同列确认线索。",
					},
					ExpectedAction:             "insert",
					ExpectedParagraphStyleID:   "TableBody",
					ExactActionCount:           1,
					MinReaderCalls:             2,
					RequireExplorationSummary:  true,
					RequireParagraphStylePatch: true,
				},
			},
			ArtifactName:         "llm-e2e-table-batch.docx",
			MinActionCount:       3,
			MinReaderCalls:       4,
			MaxReaderCalls:       12,
			RequireSharedTableID: true,
			Suggestions: []string{
				"若真实模型仍把多格编辑压成单条动作，应继续强化‘同一请求允许多次最小动作’的约束，而不是放宽模糊替换。",
				"若真实模型反复 reader 仍无法稳定命中同一张表，应继续强化拿到真实 table_id 后先整表 exploration 再顺序编辑的规则。",
				"若单轮批量任务持续塌缩成只执行最后一步，优先把 evaluator 改成 staged orchestration：每回合只允许 1 条最小动作，并在阶段间保存中间文档状态。",
			},
			Verify: verifyLLMTableBatchUpdateDocument,
		},
		{
			Name:            "llm_e2e_paragraph_break_dialogue",
			UserInstruction: "把交付说明这句在冒号后按回车拆成两段，内容别变。",
			PromptHints: []string{
				"只处理正文里的“交付说明：交付物以验收版本为准。”，不要改后面的“验证说明”或“附录说明”。",
				"这次编辑的目标是在“交付说明：”后面插入段落断开，必须使用 insert_paragraph_break，不要用 insert 新增段落，也不要用 replace 改写文字。",
				"生成 word_editor_action 时，target_text 应优先填写前半句“交付说明：”，并用 position=after 把断点放在冒号后。",
			},
			ArtifactName:   "llm-e2e-paragraph-break.docx",
			ExpectedAction: "insert_paragraph_break",
			Suggestions: []string{
				"若真实模型把 Enter 错选成 insert 或 insert_line_break，应继续强化‘同句拆成两段’到 insert_paragraph_break 的动作映射提示。",
			},
			Verify: verifyLLMParagraphBreakDocument,
		},
		{
			Name:            "llm_e2e_line_break_dialogue",
			UserInstruction: "把验证说明这句在冒号后按 Shift+Enter 换一行，内容别变。",
			Stages: []llmDialogueStage{
				{
					Name:            "insert_soft_break",
					UserInstruction: "把验证说明这句在冒号后按 Shift+Enter 换一行，内容别变。",
					PromptHints: []string{
						"只处理正文里的“验证说明：当前版本仍需人工复核。”，不要改“交付说明”或“附录说明”。",
						"这次编辑是同一段内换行，必须使用 insert_line_break；不要用 insert_paragraph_break 拆成两段，也不要用 insert 新增第二段。",
						"生成 word_editor_action 时，target_text 应优先填写前半句“验证说明：”，并用 position=after 把换行放在冒号后。",
						"这题不允许只靠 read 直接生成动作；在调用 auramate-word-copilot 前，必须至少调用一次 auramate-word-reader，拿到真实 paragraph_id 和 reader_session_id，并把它们回填到最终动作。",
					},
					ExpectedAction:   "insert_line_break",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName: "llm-e2e-line-break.docx",
			Suggestions: []string{
				"若真实模型把 Shift+Enter 错选成 insert_paragraph_break，应继续强化‘段内换行不新增段落’的动作边界提示。",
			},
			Verify: verifyLLMLineBreakDocument,
		},
		{
			Name:            "llm_e2e_inline_emphasis",
			UserInstruction: "把导语里的“自动化回归能力”加粗标红，并改成黑体，不要改文字内容。",
			Stages: []llmDialogueStage{
				{
					Name:            "format_intro_phrase",
					UserInstruction: "把导语里的“自动化回归能力”加粗标红，并改成黑体，不要改文字内容。",
					PromptHints: []string{
						"这次修改是正文短语强调，不是改字，也不是插入说明；必须使用 action=format，禁止用 replace、insert 或 update_header_footer。",
						"target_text 必须精确填写“自动化回归能力”；不要把整句导语作为 target_text，也不要扩大到前面的“格式保持与自动化回归能力”。",
						"style_patch 必须是结构化 JSON 对象，不要写成字符串，也不要使用 Go 字段名。直接按这个形状填写：{\"font_family\":\"黑体\",\"font_size_hp\":26,\"bold\":true,\"bold_set\":true,\"color\":\"C00000\"}。",
						"这次修改不允许变更正文内容；调用 auramate-word-copilot 前，必须至少调用一次 auramate-word-reader，拿到真实 paragraph_id，并确认只修改该短语所在范围。",
						"如果 validator 提示字体或字号未生效，下一次重试时不要只保留 bold/color，必须把 font_family、font_size_hp、bold_set、color 一起显式回填。",
					},
					ExpectedAction:   "format",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName: "llm-e2e-inline-emphasis.docx",
			Suggestions: []string{
				"若真实模型把短语强调错误做成 replace，应继续强化‘run 级样式修改必须用 format，不得改动正文文本’的动作边界。",
			},
			Verify: verifyLLMInlineEmphasisDocument,
		},
		{
			Name:            "llm_e2e_expanded_replace_same_paragraph",
			UserInstruction: "把验证说明这句补完整一点，增加发布前要同步补齐回归记录，但仍然保持同一段，不要换行也不要拆段。",
			PromptHints: []string{
				"只处理正文里的‘验证说明：当前版本仍需人工复核。’，不要改‘交付说明’、‘附录说明’或表格内容。",
				"这次修改属于原句扩写，但结构仍保持同一段，因此应优先使用 replace；不要插入 insert_paragraph_break、insert_line_break，也不要新增第二个正文段落。",
				"第一次命中后不要直接改写；至少再做一次 auramate-word-reader exploration，确认目标句前后相邻段仍是正文说明结构，并把‘继续保持同一段’的判断写进 reader_context_summary 或 format_basis。",
				"生成 word_editor_action 时，target_text 应填写完整原句‘验证说明：当前版本仍需人工复核。’，new_text 应为‘验证说明：当前版本仍需人工复核，发布前需同步补齐回归记录。’。",
			},
			ArtifactName:   "llm-e2e-expanded-replace-same-paragraph.docx",
			ExpectedAction: "replace",
			MinReaderCalls: 1,
			Suggestions: []string{
				"若真实模型把同段扩写误做成换行或拆段，应继续强化 expanded replace 的结构判断约束，让 reader_context_summary / format_basis 必须明确说明‘保持同一段’。",
			},
			Verify: verifyLLMExpandedReplaceSameParagraphDocument,
		},
		{
			Name:            "llm_e2e_expanded_replace_requires_split",
			UserInstruction: "把交付说明这句扩成两段，前面保留‘交付说明：’单独成段，下一段改成‘交付物以验收版本为准，归档时同步附上签收单。’。",
			Stages: []llmDialogueStage{
				{
					Name:            "split_delivery_sentence",
					UserInstruction: "只把正文里的‘交付说明：交付物以验收版本为准。’在冒号后拆成两段。当前这一回合只做断段，不要改后半句文案，也不要改验证说明、附录说明或表格内容。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 insert_paragraph_break；完成后立即停止。",
						"第一次命中后不要直接编辑；至少再做一次 auramate-word-reader exploration，确认目标句前后相邻段属于同一正文区域，并把‘需要拆成两段’的结构判断写进 reader_context_summary 或 format_basis。",
						"生成动作时，只围绕前半句‘交付说明：’在冒号后使用 insert_paragraph_break，不要提前改写后半句。",
					},
					ExpectedAction:   "insert_paragraph_break",
					ExactActionCount: 1,
					MinReaderCalls:   2,
				},
				{
					Name:            "rewrite_delivery_tail",
					UserInstruction: "基于当前文档状态，只把拆段后的后半段‘交付物以验收版本为准。’改成‘交付物以验收版本为准，归档时同步附上签收单。’。不要再断段，也不要改验证说明、附录说明或表格内容。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"如果 reader 已显示‘交付说明：’和后半句已经分成两段，必须只改后半段‘交付物以验收版本为准。’，不要回头重做 insert_paragraph_break。",
						"生成动作时，target_text 必须是拆段后的完整后半句‘交付物以验收版本为准。’，new_text 必须是‘交付物以验收版本为准，归档时同步附上签收单。’。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-expanded-replace-requires-split.docx",
			MinActionCount: 2,
			MinReaderCalls: 3,
			Suggestions: []string{
				"若真实模型仍试图用单条 replace 硬塞结构变化，应继续强化‘扩写后需要断段时必须先用 insert_paragraph_break，再处理后续正文’的动作映射。",
				"若同句拆段 + 改写持续在单轮里超时，优先改成 staged orchestration：先断段，再在下一回合只改后半句。",
			},
			Verify: verifyLLMExpandedReplaceRequiresSplitDocument,
		},
		{
			Name:            "llm_e2e_header_hit_rewrite",
			UserInstruction: "把页眉里的“Auramate 内部资料”改成“Auramate 项目内参”，不要动正文。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_default_header",
					UserInstruction: "只把默认页眉里的“Auramate 内部资料”改成“Auramate 项目内参”。不要改正文、页脚或附录。",
					PromptHints: []string{
						"如果 auramate-word-reader 的 query_hits 显示 region=header、header_footer_kind=header、header_footer_variant=default，必须把这次修改视为默认页眉更新，而不是正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"生成 word_editor_action 时必须显式提供完整的 header_footer JSON，目标 part 固定为 kind=header、variant=default，blocks 至少保留一个 paragraph block，并把文本写成“Auramate 项目内参”。",
						"不要只回填 target_text 或 preferred_paragraph_id；若没有 header_footer 结构，视为无效动作。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-header-rewrite.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若真实模型把页眉文本错误当成正文 replace，应继续强化 reader query_hits 中 region/header_footer_kind/header_footer_variant 的使用约束。",
			},
			Verify: verifyLLMHeaderRewriteDocument,
		},
		{
			Name:            "llm_e2e_first_page_header_variant_rewrite",
			UserInstruction: "只把首页页眉里的“Auramate 首页资料”改成“Auramate 首页摘要”，不要改默认页眉“Auramate 内部资料”，也不要动正文。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_first_page_header",
					UserInstruction: "只把首页页眉里的“Auramate 首页资料”改成“Auramate 首页摘要”。不要改默认页眉、页脚或正文。",
					PromptHints: []string{
						"如果 auramate-word-reader 的 query_hits 显示 region=header、header_footer_kind=header、header_footer_variant=first，必须把这次修改视为首页页眉更新，而不是默认页眉或正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"生成 word_editor_action 时必须显式提供完整的 header_footer JSON，目标 part 固定为 kind=header、variant=first，blocks 至少保留一个 paragraph block，并把文本写成“Auramate 首页摘要”。",
						"不要把默认页眉“Auramate 内部资料”一起回填进 first variant，也不要把 variant 漏成 default。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-first-page-header-variant-rewrite.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若真实模型把首页页眉与默认页眉混淆，应继续强化 reader query_hits 中 header_footer_variant=first 的消费规则，并要求 header_footer JSON 显式回填 variant=first。",
			},
			Verify: verifyLLMFirstPageHeaderVariantRewriteDocument,
		},
		{
			Name:            "llm_e2e_header_footer_variant_mixed_isolation",
			InputDocBuilder: buildHeaderFooterVariantFixture,
			UserInstruction: "把默认页眉改成“Auramate 默认周报”，把首页页眉改成“Auramate 首页摘要”，再把偶数页页脚改成“偶数页审阅版”。不要改默认页脚、正文或其他 variant。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_default_header_variant",
					UserInstruction: "只把默认页眉“Auramate 内部资料”改成“Auramate 默认周报”。不要改首页页眉、偶数页页脚、默认页脚或正文。",
					PromptHints: []string{
						"如果 auramate-word-reader 的 query_hits 显示 region=header、header_footer_kind=header、header_footer_variant=default，必须把这次修改视为默认页眉更新，而不是正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"生成 word_editor_action 时必须显式提供完整的 header_footer JSON，目标 part 固定为 kind=header、variant=default，blocks 至少保留一个 paragraph block，并把文本写成“Auramate 默认周报”。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "rewrite_first_header_variant",
					UserInstruction: "基于当前文档状态，只把首页页眉“Auramate 首页资料”改成“Auramate 首页摘要”。不要改默认页眉、偶数页页脚、默认页脚或正文。",
					PromptHints: []string{
						"如果 auramate-word-reader 的 query_hits 显示 region=header、header_footer_kind=header、header_footer_variant=first，必须把这次修改视为首页页眉更新，而不是默认页眉或正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"生成 word_editor_action 时必须显式提供完整的 header_footer JSON，目标 part 固定为 kind=header、variant=first，blocks 至少保留一个 paragraph block，并把文本写成“Auramate 首页摘要”。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "rewrite_even_footer_variant",
					UserInstruction: "基于当前文档状态，只把偶数页页脚“偶数页页脚”改成“偶数页审阅版”。不要改默认页脚、默认页眉、首页页眉或正文。",
					PromptHints: []string{
						"如果 auramate-word-reader 的 query_hits 显示 region=footer、header_footer_kind=footer、header_footer_variant=even，必须把这次修改视为偶数页页脚更新，而不是默认页脚或正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"生成 word_editor_action 时必须显式提供完整的 header_footer JSON，目标 part 固定为 kind=footer、variant=even，blocks 至少保留一个 paragraph block，并把文本写成“偶数页审阅版”。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-header-footer-variant-mixed.docx",
			MinActionCount: 3,
			MinReaderCalls: 3,
			Suggestions: []string{
				"若真实模型在 first/even/default 混合任务里串写 variant，应继续强化 query_hits 里的 header_footer_variant 消费约束，而不是放宽 replace。",
			},
			Verify: verifyLLMHeaderFooterVariantMixedDocument,
		},
		{
			Name:            "llm_e2e_section_layout_orientation_margin_format",
			UserInstruction: "把当前文档改成横向页面，并把左右页边距改成 1200。不要改正文、页码、页眉页脚文案，也不要改首页不同设置。",
			Stages: []llmDialogueStage{
				{
					Name:            "format_orientation_and_margins",
					UserInstruction: "只把当前文档的页面方向改成 landscape，并把 margin_left、margin_right 改成 1200。不要改正文、页码、页眉页脚文本，也不要改首页不同设置。",
					PromptHints: []string{
						"第一次 auramate-word-reader 必须显式传 include_layout=true 与 focus_page_index=0，先确认当前节的页面方向和页边距。",
						"这一回合只允许完成 1 条动作，并且只能是 format_section；完成后立即停止。",
						"section_layout_patch 必须至少显式回填 orientation=landscape、margin_left=1200、margin_right=1200；不要把任务退化成修改页眉页脚文字。",
						"生成 word_editor_action 时，必须显式输出结构化 section_layout_patch JSON；可直接按这个形状填写：{\"orientation\":\"landscape\",\"margin_left\":1200,\"margin_right\":1200}。",
					},
					ExpectedAction:   "format_section",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-section-layout-orientation-margin-format.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若真实模型把节布局任务误做成页眉页脚文本改写，应继续强化 include_layout 取证和 format_section 动作收敛。",
			},
			Verify: verifyLLMSectionLayoutOrientationMarginDocument,
		},
		{
			Name:            "llm_e2e_page_number_start_format",
			UserInstruction: "把当前文档设置成从第 5 页开始编号，并使用大写罗马数字页码格式。不要改正文、页眉页脚文案，也不要改页面方向或页边距。",
			Stages: []llmDialogueStage{
				{
					Name:            "format_page_number_start_and_style",
					UserInstruction: "只把当前文档的页码设置改成从第 5 页开始编号，并使用大写罗马数字格式。不要改正文、页眉页脚文本，也不要改页面方向、页边距或首页不同设置。",
					PromptHints: []string{
						"第一次 auramate-word-reader 必须显式传 include_layout=true 与 focus_page_index=0，先确认当前是同一节的页码/版式配置，不要把任务退化成改页脚文本。",
						"即使 reader 命中了页脚里的‘第 1 页’，这次任务仍然是 section_layout 层的页码编号规则修改，不能使用 replace 或 update_header_footer。",
						"这一回合只允许完成 1 条动作，并且只能是 format_section；完成后立即停止。",
						"section_layout_patch 必须至少显式回填 page_number_start=5 与 page_number_format=upperRoman；不要漏成空 patch，也不要顺手覆盖 orientation、margin 或 title_page。",
						"生成 word_editor_action 时，必须显式输出结构化 section_layout_patch JSON；不要只写 format_basis 或 reader_context_summary。可直接按这个形状填写：{\"page_number_start\":5,\"page_number_format\":\"upperRoman\"}。",
						"如果缺少 section_layout_patch，动作会被判定为无效；不要只返回 action=format_section + target_text。",
						"reader_context_summary 或 format_basis 必须明确说明：这是节级页码设置修改，目标是 page number start/format，不是页脚文字改写。",
					},
					ExpectedAction:   "format_section",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-page-number-start-format.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若真实模型把页码设置误做成页脚文本改写，应继续强化 include_layout 取证与 format_section 的动作收敛，而不是放宽 footer replace。",
			},
			Verify: verifyLLMPageNumberStartFormatDocument,
		},
		{
			Name:            "llm_e2e_cross_region_batch",
			UserInstruction: "把页眉里的“Auramate 内部资料”改成“Auramate 项目周报”，把“验证说明：当前版本仍需人工复核。”改成“审阅说明：当前版本仍需人工复核。”，并把表格里的85%改成92%，不要动附录说明。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_weekly_header",
					UserInstruction: "只把默认页眉里的“Auramate 内部资料”改成“Auramate 项目周报”。不要改正文说明、通过率或附录说明。",
					PromptHints: []string{
						"第一步必须先调用 auramate-word-reader，query 直接使用“Auramate 内部资料”，确认命中落在默认页眉，而不是正文标题或其他区域。",
						"如果 auramate-word-reader 的 query_hits 显示 region=header、header_footer_kind=header、header_footer_variant=default，必须把这次修改视为默认页眉更新，而不是正文 replace。",
						"这一回合只允许完成 1 条动作，并且只能是 update_header_footer；完成后立即停止。",
						"必须显式提供完整的 header_footer JSON，kind=header、variant=default，blocks 至少包含一个 paragraph block，文本改成“Auramate 项目周报”。",
						"不要只回填 target_text 或 preferred_paragraph_id；若没有 header_footer 结构，视为无效动作。",
						"不要退化成 replace，也不要改正文标题“Auramate 智能编辑综合验证报告”。",
					},
					ExpectedAction:   "update_header_footer",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "relabel_review_note",
					UserInstruction: "基于当前文档状态，只把“验证说明：当前版本仍需人工复核。”改成“审阅说明：当前版本仍需人工复核。”。不要改页眉、通过率或附录说明。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"target_text 必须直接使用完整原句“验证说明：当前版本仍需人工复核。”，不要只写‘验证说明’或‘中间那条说明’。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "update_weekly_pass_rate",
					UserInstruction: "基于当前文档状态，只把表格里的85%改成92%。不要改页眉、正文说明或附录说明。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"第一次 reader 应优先走表格最小读取路径：query 用“通过率”，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，应立即用 focus_table_id 整表读取一次，确认通过率所在单元格，再生成动作。",
						"table_cell 必须明确写出更新后的最终单元格内容 92%，不能只给 row_index/cell_index。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串‘92%’，也不要传 {text: \"92%\"}。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"92%\",\"style_id\":\"TableBody\"}]}",
					},
					ExpectedAction:   "update_table_cell",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:              "llm-e2e-cross-region-batch.docx",
			MinActionCount:            3,
			MinReaderCalls:            2,
			RequireExplorationSummary: false,
			Suggestions: []string{
				"若跨页眉/正文/表格的混合批处理仍漏动作，应优先强化‘多区域任务必须拆成顺序最小动作’的提示，而不是放宽单条动作的容错。",
				"若模型把页眉和正文混淆，应优先强化 reader 的 region/header_footer 证据消费规则。",
			},
			Verify: verifyLLMCrossRegionBatchDocument,
		},
		{
			Name: "llm_e2e_repeat_header_table_update",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_gray_release_requirement",
					UserInstruction: "把发布批次表里“灰度批次”右侧要求改成“需附版本签字记录。”，不要动表头，也不要破坏重复表头设置。",
					PromptHints: []string{
						"这次修改只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"第一次 reader 必须优先走表格最小读取路径：query 用“灰度批次”，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，应立即用 focus_table_id 再读一次整表，确认“灰度批次”所在行与右侧要求单元格，再生成动作。",
						"target_text 必须填写当前单元格原文“需关注灰度开关与监控告警。”；不要只写“灰度批次”或“右侧要求”。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"需附版本签字记录。\",\"style_id\":\"TableBody\"}]}。",
						"exploration_summary 必须明确说明：目标表的首行是重复表头，当前动作只改 body 行单元格，不能改表头，也不能破坏 repeat_header/cant_split。",
					},
					ExpectedAction:            "update_table_cell",
					ExactActionCount:          1,
					MinReaderCalls:            1,
					RequireExplorationSummary: true,
				},
			},
			ArtifactName: "llm-e2e-repeat-header-table-update.docx",
			Suggestions: []string{
				"若真实模型改对了文本却破坏了表头行属性，应优先检查 update_table_cell 是否完整保留未改动行以及 SaveDOCX 对 repeat_header/cant_split 的写回。",
			},
			Verify: verifyLLMRepeatHeaderTableUpdateDocument,
		},
		{
			Name:                  "llm_e2e_cross_page_repeat_header_visual_update",
			RequireReopenedLayout: true,
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_second_page_release_requirement",
					UserInstruction: "把第二页发布批次表里“灾备演练批次”右侧要求改成“需同步灾备切换窗口与回退负责人。”，并保持跨页后的重复表头可见。",
					PromptHints: []string{
						"这次修改只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"第一次 reader 必须优先走表格最小读取路径：query 用“灾备演练批次”，显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，至少再调用一次 auramate-word-reader，并显式传 include_layout=true、focus_page_index=1 与 focus_table_id，确认第二页仍然显示重复表头，再生成动作。",
						"target_text 必须填写当前单元格原文“需演练双活切换与值班交接。”；不要只写“灾备演练批次”或“第二页那行要求”。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"需同步灾备切换窗口与回退负责人。\",\"style_id\":\"TableBody\"}]}。",
						"exploration_summary 必须明确说明：目标表跨页延续到第二页，第二页首行仍是重复表头，当前动作只改第二页 body 行单元格。",
					},
					ExpectedAction:            "update_table_cell",
					ExactActionCount:          1,
					MinReaderCalls:            2,
					RequireExplorationSummary: true,
				},
			},
			ArtifactName: "llm-e2e-cross-page-repeat-header-visual-update.docx",
			Suggestions: []string{
				"若真实模型能改中文案却拿不到第二页表头证据，应优先检查 reader 是否真的走了 include_layout + focus_page_index，而不是继续放宽表格定位。",
			},
			Verify: verifyLLMCrossPageRepeatHeaderVisualUpdateDocument,
		},
		{
			Name:            "llm_e2e_drawing_objects_body_edit_preserves_objects",
			InputDocBuilder: buildDrawingObjectsEditFixture,
			UserInstruction: "只把正文里的“对象区提示：当前版本仅供内部评审。”改成“对象区提示：当前版本已进入联合评审。”。不要改后面的文本框内容、形状对象、图片或旧式图形对象。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_body_note_beside_drawing_objects",
					UserInstruction: "只把正文里的“对象区提示：当前版本仅供内部评审。”改成“对象区提示：当前版本已进入联合评审。”。不要改对象区说明、文本框内容、shape、anchor 图片或旧式图形对象。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"第一次 auramate-word-reader 的 query 必须直接使用完整原句“对象区提示：当前版本仅供内部评审。”，先锁定正文锚点，不要把文本框里的文字当成可编辑目标。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
						"reader_context_summary 或 format_basis 必须明确说明：这次只改正文提示段，后续包含 textbox/shape/anchor 的对象区不能被改写或重建。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-drawing-objects-body-edit.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若正文编辑后 textbox/shape 或 anchor 图片丢失，应优先检查正文改写与 SaveDOCX 是否错误重建了包含对象的相邻段落。",
				"若真实模型尝试直接改文本框内容，应继续强化当前 paragraph 模型只允许锁定正文锚点、对象区以保真验证为主的约束。",
			},
			Verify: verifyLLMDrawingObjectsBodyEditDocument,
		},
		{
			Name:            "llm_e2e_relative_multi_region_batch",
			UserInstruction: "把附录说明改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_archive_note",
					UserInstruction: "只把末尾正文“附录说明：本报告用于自动化回归。”改成“归档备注：本报告用于自动化回归。”。不要改第三方费用、风险提示右侧结果、验证说明或页眉页脚。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"必须锁定文档末尾那条附录说明正文；如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
						"本次仅修改前缀标识，保持同一段落，不拆分、不新增段落。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "insert_purchase_approval",
					UserInstruction: "基于当前文档状态，只在第三方费用那条后面补一句“需要提前走采购审批。”。不要改附录说明、风险提示右侧结果、验证说明或页眉页脚。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 insert + position=after；完成后立即停止。",
						"‘第三方费用那条’必须落到第三方费用正文，不允许误落到差旅费用；如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
						"target_text 必须使用第三方费用那条的完整原句，不要只写‘费用’或‘那条’。",
					},
					ExpectedAction:   "insert",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "update_risk_right_value",
					UserInstruction: "基于当前文档状态，只把风险提示右侧结果改成“无需人工复核”。不要再改附录说明或第三方费用。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
						"第一次 reader 应优先走表格最小读取路径：query 优先用“风险提示”，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
						"拿到真实 table_id 后，应立即用 focus_table_id 整表读取一次，确认风险提示同行右侧值单元格，再生成动作。",
						"table_cell 必须明确写出更新后的最终单元格内容 无需人工复核，不能只给 row_index/cell_index。",
						"table_cell 必须使用结构化 JSON 对象，不要传纯字符串，也不要传 {text: \"无需人工复核\"}。可直接按这个形状填写：{\"paragraphs\":[{\"text\":\"无需人工复核\",\"style_id\":\"TableHeader\"}]}。",
						"format_basis 必须直接说明沿用当前结果单元格的原样式证据，至少写清单段结构与 style_id=TableHeader；若 reader 已读到字体、字号、加粗，也要一起写出。",
						"exploration_summary 必须写清这是风险提示同行右侧结果格，并明确给出 row_index/cell_index 与同一行左侧标签‘风险提示’的对应关系。",
						"如果已经拿到了真实 table_id、row_index、cell_index、最终 table_cell 和 format_basis，就必须立刻只调用 1 次 auramate-word-copilot；不要再试探性发送缺字段或错误形状的 payload。",
					},
					ExpectedAction:            "update_table_cell",
					ExactActionCount:          1,
					MinReaderCalls:            1,
					RequireExplorationSummary: true,
				},
			},
			ArtifactName:              "llm-e2e-relative-multi-region-batch.docx",
			MinActionCount:            3,
			MinReaderCalls:            3,
			RequireExplorationSummary: false,
			Suggestions: []string{
				"若多处相对定位任务仍串锚点，应优先加强每条动作前的二次 exploration，而不是继续放宽 target_text 模糊匹配。",
				"若第三方费用后的插入仍落到差旅费用后，应优先强化同类费用条款的相邻段排序和 paragraph_id 回填。",
			},
			Verify: verifyLLMRelativeMultiRegionBatchDocument,
		},
		{
			Name:            "llm_e2e_ambiguous_instruction_clarify_then_edit",
			UserInstruction: "先判断“把说明改成审阅说明”这句是否能唯一定位；如果不能，就先说明需要澄清哪一条。明确后，再只改提到人工复核的那条说明。",
			Stages: []llmDialogueStage{
				{
					Name:            "detect_ambiguous_explanation_request",
					UserInstruction: "当前只做取证：用户说“把说明改成审阅说明”，文档里存在多条说明时先判断是否唯一。如果不能唯一定位，只返回歧义说明和需要补充的信息，不要编辑文档，也不要调用 auramate-word-copilot。",
					PromptHints: []string{
						"至少做一次 auramate-word-reader 检索，确认文档里是否存在多条‘说明’类段落。",
						"如果存在交付说明、验证说明、附录说明等多个候选，当前这一回合只允许读取和总结，不允许生成任何 word_editor_action。",
						"回答里必须明确指出：还需要用户补充是交付说明、验证说明还是附录说明。",
					},
					MinReaderCalls:   1,
					RequireNoAction:  true,
					SkipCopilotCheck: true,
				},
				{
					Name:            "rewrite_clarified_review_note",
					UserInstruction: "现在已澄清：只把提到人工复核的那条“验证说明：当前版本仍需人工复核。”改成“审阅说明：当前版本仍需人工复核。”。不要改交付说明或附录说明。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"target_text 必须直接使用完整原句“验证说明：当前版本仍需人工复核。”，不要只写‘验证说明’。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-ambiguous-instruction-clarify.docx",
			MinActionCount: 1,
			MinReaderCalls: 2,
			Suggestions: []string{
				"若真实模型在多命中条件下仍直接编辑，应继续强化 reader 取证后先拒动澄清的负向合同，而不是扩大模糊匹配。",
			},
			Verify: verifyLLMAmbiguousInstructionClarifyThenEditDocument,
		},
		{
			Name:            "llm_e2e_multi_section_body_edit_preserves_sections",
			InputDocBuilder: buildMultiSectionFixture,
			UserInstruction: "只把第二节正文“第二节正文”改成“第二节正文已复核”，不要改第一节页眉、第二节页眉、第一节末段或各节页面方向。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_second_section_body_only",
					UserInstruction: "只把第二节正文“第二节正文”改成“第二节正文已复核”。不要改第一节正文“第一节末段”，也不要改两节页眉或节方向。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"target_text 必须直接使用完整原句“第二节正文”，不要写‘第二节’或‘正文’这种模糊片段。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-multi-section-body-edit.docx",
			MinActionCount: 1,
			Suggestions: []string{
				"若多节文档编辑后节元数据或页眉页脚丢失，应优先检查 save/reopen 对 section break 和 section header/footer 的保真，而不是放宽正文编辑验证。",
			},
			Verify: verifyLLMMultiSectionBodyEditPreservesSectionsDocument,
		},
		{
			Name:                  "llm_e2e_large_document_long_chain",
			InputDocBuilder:       buildLargeDocumentFixture,
			RequireReopenedLayout: true,
			UserInstruction:       "把第 042 节验证结论改成已完成自动复核，再把第 057 节验证结论改成需要补齐灰度记录。不要改其他节。",
			Stages: []llmDialogueStage{
				{
					Name:            "rewrite_long_doc_section_042",
					UserInstruction: "只把“第 042 节验证结论：当前批次需要人工复核。”改成“第 042 节验证结论：当前批次已完成自动复核。”。不要改其他节。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"第一次 reader 应优先走 paragraph 最小读取路径，query 直接使用完整原句，不要展开全文。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
				{
					Name:            "rewrite_long_doc_section_057",
					UserInstruction: "基于当前长文档状态，只把“第 057 节验证结论：当前批次需要人工复核。”改成“第 057 节验证结论：当前批次需要补齐灰度记录。”。不要改其他节。",
					PromptHints: []string{
						"这一回合只允许完成 1 条动作，并且只能是 replace；完成后立即停止。",
						"第一次 reader 应优先走 paragraph 最小读取路径，query 直接使用完整原句，不要展开全文。",
						"如果 reader 返回 paragraph_id，必须回填 preferred_paragraph_id 与同一 reader_session_id。",
					},
					ExpectedAction:   "replace",
					ExactActionCount: 1,
					MinReaderCalls:   1,
				},
			},
			ArtifactName:   "llm-e2e-large-document-long-chain.docx",
			MinActionCount: 2,
			MinReaderCalls: 2,
			Suggestions: []string{
				"若长文档场景退化成全文展开，应继续强化最小 query + paragraph_id 回填，而不是放宽长文档基线。",
			},
			Verify: verifyLLMLargeDocumentLongChainDocument,
		},
	}
	if strings.TrimSpace(ctx.config.ExternalDocx) != "" {
		scenarios = append(scenarios, buildExternalContractTableBatchScenario(ctx.config.ExternalDocx))
	}

	results := make([]scenarioResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		if !matchesLLMScenarioFilter(scenario.Name, ctx.config.LLMScenario) {
			continue
		}
		results = append(results, runLLMDialogueScenario(ctx, scenario))
	}

	return results, summarizeLLMEndToEndSuite(ctx.liveRunner.model, started, results)
}

func buildExternalContractTableBatchScenario(externalDocx string) llmDialogueScenario {
	targets := chooseExternalContractTableTargets(externalDocx)
	summaryText := fmt.Sprintf("合计含税人民币总金额： %s 元，不含税人民币总金额： %s 元", targets.SummaryGross, targets.SummaryNet)
	return llmDialogueScenario{
		Name:            "llm_e2e_external_contract_table_batch_update",
		UserInstruction: fmt.Sprintf("只改产品清单表格：把防火墙设备含税单价改成%s，把不含税单价改成%s，并把合计行改成含税%s元、不含税%s元。不要改正文其他条款。", targets.TaxIncluded, targets.TaxExcluded, targets.SummaryGross, targets.SummaryNet),
		Stages: []llmDialogueStage{
			{
				Name:            "update_firewall_tax_included_price",
				UserInstruction: fmt.Sprintf("只把产品清单表格里防火墙设备的含税单价改成%s。不要改不含税单价，也不要改合计行，更不要改正文条款。", targets.TaxIncluded),
				PromptHints: []string{
					"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
					"reader 应优先走表格最小读取路径：query 优先用‘防火墙’或‘防火墙设备’，并显式传 context_scope=table、include_full_text=false、include_outline=false。",
					"拿到真实 table_id 后，应继续用 focus_table_id 整表确认防火墙设备所在行，以及‘单价（元）含税’这一列对应的单元格。",
					"如果已锁定 table_id、row_index、cell_index 且只是整格纯文本改写，优先使用 new_text 简写并复用同一 reader_session_id；不要退回 replace。",
					"exploration_summary 必须写清：目标格属于防火墙设备所在行的含税单价列，而不是不含税列或合计行。",
				},
				ExpectedAction:            "update_table_cell",
				ExactActionCount:          1,
				MinReaderCalls:            1,
				RequireExplorationSummary: true,
			},
			{
				Name:            "update_firewall_tax_excluded_price",
				UserInstruction: fmt.Sprintf("基于当前文档状态，只把产品清单表格里防火墙设备的不含税单价改成%s。不要改含税单价，不要改合计行，也不要改正文条款。", targets.TaxExcluded),
				PromptHints: []string{
					"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
					"reader 应优先复用表格最小读取路径锁定防火墙设备所在行；重点确认这次目标是‘单价（元）不含税’列，而不是含税列。",
					"如果已锁定 table_id、row_index、cell_index 且只是整格纯文本改写，优先使用 new_text 简写并复用同一 reader_session_id；不要退回 replace。",
					"exploration_summary 必须明确区分：本次修改的是防火墙设备行的不含税单价格，不是含税格，也不是合计格。",
				},
				ExpectedAction:            "update_table_cell",
				ExactActionCount:          1,
				MinReaderCalls:            1,
				RequireExplorationSummary: true,
			},
			{
				Name:            "update_contract_summary_row",
				UserInstruction: fmt.Sprintf("基于当前文档状态，只把产品清单表格的合计行改成‘%s’。不要改正文其他条款。", summaryText),
				PromptHints: []string{
					"这一回合只允许完成 1 条动作，并且只能是 update_table_cell；完成后立即停止。",
					"reader 应优先用‘合计含税人民币总金额’或‘合计’锁定产品清单表，再用 focus_table_id 整表确认这是跨列合并的合计单元格。",
					"如果已锁定 table_id、row_index、cell_index 且只是整格纯文本改写，优先使用 new_text 简写并复用同一 reader_session_id；不要退回 replace。",
					"exploration_summary 必须写清：这是产品清单表格的合计单元格，不是正文金额条款，也不是单价列普通单元格。",
				},
				ExpectedAction:            "update_table_cell",
				ExactActionCount:          1,
				MinReaderCalls:            1,
				RequireExplorationSummary: true,
			},
		},
		InputDocBuilder: func(outputPath string) error {
			return copyBinaryFile(externalDocx, outputPath)
		},
		ArtifactName:   "llm-e2e-external-contract-table-batch.docx",
		MinActionCount: 3,
		MinReaderCalls: 3,
		MaxReaderCalls: 12,
		Suggestions: []string{
			"若真实合同表格场景仍出现 target_text 漏填后的中途失败，应继续把最近 reader 命中的单元格原文注入恢复上下文，而不是继续加泛化提示。",
			"若模型把合计单元格误改成 replace，应继续强化‘表格整格纯文本改写优先 update_table_cell + new_text 简写’。",
			"若正文金额条款被误改，优先加强合计行与正文金额段的区分证据，而不是放宽表格动作落点。",
		},
		Verify: func(reopened *goword.Document, result *executionResult) error {
			return verifyLLMExternalContractTableBatchDocument(reopened, result, targets)
		},
	}
}

func chooseExternalContractTableTargets(externalDocx string) externalContractTableTargets {
	primary := externalContractTableTargets{
		TaxIncluded:  "6000",
		TaxExcluded:  "5940.60",
		SummaryGross: "10000",
		SummaryNet:   "9900.99",
	}
	secondary := externalContractTableTargets{
		TaxIncluded:  "6100",
		TaxExcluded:  "6040.60",
		SummaryGross: "10100",
		SummaryNet:   "10000.99",
	}
	primarySummary := fmt.Sprintf("合计含税人民币总金额： %s 元，不含税人民币总金额： %s 元", primary.SummaryGross, primary.SummaryNet)
	secondarySummary := fmt.Sprintf("合计含税人民币总金额： %s 元，不含税人民币总金额： %s 元", secondary.SummaryGross, secondary.SummaryNet)
	if contains, err := docxEntryContains(externalDocx, "word/document.xml", primarySummary); err == nil && contains {
		return secondary
	}
	if contains, err := docxEntryContains(externalDocx, "word/document.xml", secondarySummary); err == nil && contains {
		return primary
	}
	return primary
}

func matchesLLMScenarioFilter(name, filter string) bool {
	trimmedFilter := strings.TrimSpace(filter)
	if trimmedFilter == "" {
		return true
	}
	for _, part := range strings.Split(trimmedFilter, ",") {
		candidate := strings.ToLower(strings.TrimSpace(part))
		if candidate == "" {
			continue
		}
		if strings.Contains(strings.ToLower(name), candidate) {
			return true
		}
	}
	return false
}

func runLLMDialogueScenario(ctx *evalContext, scenario llmDialogueScenario) scenarioResult {
	started := time.Now()
	llmResult, execResult, inputDocPath, artifactPath, err := runLLMDialogueAttempt(ctx, scenario)
	details := buildLLMDialogueDetails(ctx.liveRunner.model, scenario, inputDocPath, llmResult, execResult, artifactPath, time.Since(started).Milliseconds())
	if err != nil {
		return failScenario(scenario.Name, started, err, append(llmE2ESuggestions(), scenario.Suggestions...), details)
	}
	if scenario.MinReaderCalls > 0 && llmResult != nil && llmResult.ReaderCallCount < scenario.MinReaderCalls {
		return failScenario(
			scenario.Name,
			started,
			fmt.Errorf("reader 调用次数不足：期望至少 %d 次，实际 %d 次", scenario.MinReaderCalls, llmResult.ReaderCallCount),
			append(llmE2ESuggestions(), scenario.Suggestions...),
			details,
		)
	}
	if scenario.MaxReaderCalls > 0 && llmResult != nil && llmResult.ReaderCallCount > scenario.MaxReaderCalls {
		return failScenario(
			scenario.Name,
			started,
			fmt.Errorf("reader 调用次数过多：期望不超过 %d 次，实际 %d 次", scenario.MaxReaderCalls, llmResult.ReaderCallCount),
			append(llmE2ESuggestions(), scenario.Suggestions...),
			details,
		)
	}
	if scenario.MinActionCount > 0 && llmResult != nil && len(llmResult.Actions) < scenario.MinActionCount {
		return failScenario(
			scenario.Name,
			started,
			fmt.Errorf("最终动作数量不足：期望至少 %d 条，实际 %d 条", scenario.MinActionCount, len(llmResult.Actions)),
			append(llmE2ESuggestions(), scenario.Suggestions...),
			details,
		)
	}
	if scenario.RequireSharedTableID && llmResult != nil {
		sharedTableID := ""
		requireStableTableID := len(scenario.Stages) == 0
		for _, action := range llmResult.Actions {
			if action == nil {
				continue
			}
			tableID := strings.TrimSpace(action.TableID)
			if tableID == "" {
				return failScenario(
					scenario.Name,
					started,
					fmt.Errorf("批量表格动作缺少 table_id"),
					append(llmE2ESuggestions(), scenario.Suggestions...),
					details,
				)
			}
			if sharedTableID == "" {
				sharedTableID = tableID
				continue
			}
			if !requireStableTableID {
				continue
			}
			if tableID != sharedTableID {
				return failScenario(
					scenario.Name,
					started,
					fmt.Errorf("批量表格动作使用了多个 table_id: %s / %s", sharedTableID, tableID),
					append(llmE2ESuggestions(), scenario.Suggestions...),
					details,
				)
			}
		}
	}
	if scenario.RequireExplorationSummary && llmResult != nil && llmResult.Action != nil && strings.TrimSpace(llmResult.Action.ExplorationSummary) == "" {
		return failScenario(
			scenario.Name,
			started,
			fmt.Errorf("缺少 exploration_summary：模型虽已多轮读取，但最终动作未给出落点确认摘要"),
			append(llmE2ESuggestions(), scenario.Suggestions...),
			details,
		)
	}
	message := fmt.Sprintf("真实模型 %s 已完成端到端对话编辑，并通过保存复开校验。", ctx.liveRunner.model)
	if budgetStatus, _ := details["budget_status"].(string); budgetStatus == "warning" {
		message = fmt.Sprintf("%s 当前预算评估为预警。", message)
	}
	return passScenario(scenario.Name, started, message, details, artifactPath)
}

func runLLMDialogueAttempt(ctx *evalContext, scenario llmDialogueScenario) (*llmDialogueRunResult, *executionResult, string, string, error) {
	if len(scenario.Stages) > 0 {
		return runLLMDialogueStagedAttempt(ctx, scenario)
	}
	inputDocPath, err := prepareLLMScenarioInputDocument(ctx, scenario)
	if err != nil {
		return nil, nil, "", "", err
	}
	session, err := ctx.client.openDocument(inputDocPath)
	if err != nil {
		return nil, nil, inputDocPath, "", err
	}
	defer ctx.client.closeSession(session.ID)

	prompt := buildLLMDialoguePrompt(inputDocPath, scenario.UserInstruction, scenario.PromptHints)
	var llmResult *llmDialogueRunResult
	err = nil
	for attempt := 1; attempt <= llmDialogueAttemptMaxAttempts; attempt++ {
		llmResult, err = ctx.liveRunner.generateAction(prompt, func(result *llmDialogueRunResult) bool {
			return shouldEarlyStopLLMScenario(scenario, result)
		})
		if err == nil || !isRetryableLLMScenarioError(err) || attempt == llmDialogueAttemptMaxAttempts {
			break
		}
	}
	if err != nil {
		return llmResult, nil, inputDocPath, "", err
	}
	if !llmResult.UsedReader {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型未调用 auramate-word-reader")
	}
	if !llmResult.UsedCopilot {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型未调用 auramate-word-copilot")
	}
	if len(llmResult.Actions) == 0 || llmResult.Action == nil {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型未产出 word_editor_action")
	}
	if scenario.ExpectedAction != "" && !strings.EqualFold(strings.TrimSpace(llmResult.Action.Action), scenario.ExpectedAction) {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型动作类型不符合预期: got=%s want=%s", llmResult.Action.Action, scenario.ExpectedAction)
	}
	if scenario.RequireParagraphStylePatch && !hasParagraphStylePatch(llmResult.Action.ParagraphStylePatch) {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型缺少 paragraph_style_patch")
	}
	if scenario.ExpectedParagraphStyleID != "" && !strings.EqualFold(strings.TrimSpace(llmResult.Action.ParagraphStylePatch.StyleID), scenario.ExpectedParagraphStyleID) {
		return llmResult, nil, inputDocPath, "", fmt.Errorf("真实模型 paragraph_style_patch.style_id 不符合预期: got=%s want=%s", llmResult.Action.ParagraphStylePatch.StyleID, scenario.ExpectedParagraphStyleID)
	}
	if err := validateLLMActionContracts(llmResult); err != nil {
		return llmResult, nil, inputDocPath, "", err
	}

	result, err := executeEditorActions(ctx.client, session.ID, llmResult.Actions)
	if err != nil {
		return llmResult, nil, inputDocPath, "", err
	}
	result.ToolTrace = append([]llmToolTrace(nil), llmResult.ToolTrace...)

	artifactPath := filepath.Join(ctx.config.Artifacts, scenario.ArtifactName)
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return llmResult, nil, inputDocPath, artifactPath, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	if scenario.RequireReopenedLayout {
		reopenedLayout, err := loadSavedDocumentLayout(ctx.client, saved.Path)
		if err != nil {
			return llmResult, result, inputDocPath, artifactPath, err
		}
		result.ReopenedLayout = reopenedLayout
	}
	if scenario.Verify != nil {
		if err := scenario.Verify(reopened, result); err != nil {
			return llmResult, result, inputDocPath, artifactPath, err
		}
	}
	return llmResult, result, inputDocPath, artifactPath, nil
}

func runLLMDialogueStagedAttempt(ctx *evalContext, scenario llmDialogueScenario) (*llmDialogueRunResult, *executionResult, string, string, error) {
	inputDocPath, err := prepareLLMScenarioInputDocument(ctx, scenario)
	if err != nil {
		return nil, nil, "", "", err
	}
	session, err := ctx.client.openDocument(inputDocPath)
	if err != nil {
		return nil, nil, inputDocPath, "", err
	}
	defer ctx.client.closeSession(session.ID)

	artifactPath := filepath.Join(ctx.config.Artifacts, scenario.ArtifactName)
	currentDocumentPath := inputDocPath
	aggregated := &llmDialogueRunResult{}
	stageResults := make([]*executionResult, 0, len(scenario.Stages))

	for index, stage := range scenario.Stages {
		var acceptedStageResult *llmDialogueRunResult
		var lastStageResult *llmDialogueRunResult
		var lastStageErr error
		for attempt := 1; attempt <= llmDialogueStageMaxAttempts; attempt++ {
			promptHints := append([]string(nil), stage.PromptHints...)
			if attempt > 1 && lastStageErr != nil {
				promptHints = append(promptHints, fmt.Sprintf("上一轮失败原因：%s。本轮只修复这个失败原因；不要输出解释，只允许产出 1 条满足合同的 word_editor_action。", lastStageErr.Error()))
			}
			prompt := buildLLMDialoguePrompt(currentDocumentPath, stage.UserInstruction, promptHints)
			stageLLMResult, stageErr := ctx.liveRunner.generateAction(prompt, func(result *llmDialogueRunResult) bool {
				return shouldEarlyStopLLMStage(stage, result)
			})
			lastStageResult = stageLLMResult
			if stageErr != nil {
				lastStageErr = stageErr
				continue
			}
			if err := validateLLMDialogueStageResult(stage, stageLLMResult); err != nil {
				lastStageErr = err
				continue
			}
			if err := validateLLMActionContracts(stageLLMResult); err != nil {
				lastStageErr = err
				continue
			}
			acceptedStageResult = stageLLMResult
			lastStageErr = nil
			break
		}
		if acceptedStageResult == nil {
			mergeLLMDialogueRunResult(aggregated, lastStageResult, stage.Name)
			return aggregated, combineExecutionResults(stageResults, aggregated.Actions), inputDocPath, artifactPath, lastStageErr
		}
		mergeLLMDialogueRunResult(aggregated, acceptedStageResult, stage.Name)

		if stage.RequireNoAction {
			continue
		}

		stageExecResult, err := executeEditorActions(ctx.client, session.ID, acceptedStageResult.Actions)
		if err != nil {
			return aggregated, combineExecutionResults(stageResults, aggregated.Actions), inputDocPath, artifactPath, err
		}
		stageResults = append(stageResults, stageExecResult)

		if index < len(scenario.Stages)-1 {
			stagePath := stagedArtifactPath(artifactPath, index+1)
			if _, err := ctx.client.saveDocument(session.ID, stagePath); err != nil {
				return aggregated, combineExecutionResults(stageResults, aggregated.Actions), inputDocPath, artifactPath, err
			}
			currentDocumentPath = stagePath
		}
	}

	finalResult := combineExecutionResults(stageResults, aggregated.Actions)
	if finalResult == nil {
		return aggregated, nil, inputDocPath, artifactPath, fmt.Errorf("staged llm execution produced no results")
	}
	finalResult.ToolTrace = append([]llmToolTrace(nil), aggregated.ToolTrace...)
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return aggregated, finalResult, inputDocPath, artifactPath, err
	}
	finalResult.SavedResult = saved
	finalResult.ReopenedDocument = reopened
	if scenario.RequireReopenedLayout {
		reopenedLayout, err := loadSavedDocumentLayout(ctx.client, saved.Path)
		if err != nil {
			return aggregated, finalResult, inputDocPath, artifactPath, err
		}
		finalResult.ReopenedLayout = reopenedLayout
	}
	if scenario.Verify != nil {
		if err := scenario.Verify(reopened, finalResult); err != nil {
			return aggregated, finalResult, inputDocPath, artifactPath, err
		}
	}
	return aggregated, finalResult, inputDocPath, artifactPath, nil
}

func mergeLLMDialogueRunResult(target, stage *llmDialogueRunResult, stageName string) {
	if target == nil || stage == nil {
		return
	}
	target.Actions = append(target.Actions, stage.Actions...)
	target.RawActions = append(target.RawActions, stage.RawActions...)
	target.ToolCalls = append(target.ToolCalls, stage.ToolCalls...)
	for _, trace := range stage.ToolTrace {
		cloned := trace
		if cloned.Stage == "" {
			cloned.Stage = stageName
		}
		target.ToolTrace = append(target.ToolTrace, cloned)
	}
	target.InputTokens += stage.InputTokens
	target.OutputTokens += stage.OutputTokens
	target.CacheReadTokens += stage.CacheReadTokens
	target.CacheCreationTokens += stage.CacheCreationTokens
	target.ReaderCallCount += stage.ReaderCallCount
	target.CopilotCallCount += stage.CopilotCallCount
	target.UsedReader = target.UsedReader || stage.UsedReader
	target.UsedCopilot = target.UsedCopilot || stage.UsedCopilot
	if len(stage.Actions) > 0 {
		target.Action = stage.Actions[len(stage.Actions)-1]
	}
	if len(stage.RawActions) > 0 {
		target.RawActions = append(target.RawActions[:0], target.RawActions...)
	}
	trimmed := strings.TrimSpace(stage.AssistantText)
	if trimmed == "" {
		return
	}
	if strings.TrimSpace(target.AssistantText) != "" {
		target.AssistantText += "\n\n"
	}
	if strings.TrimSpace(stageName) != "" {
		target.AssistantText += fmt.Sprintf("[%s]\n%s", stageName, trimmed)
		return
	}
	target.AssistantText += trimmed
}

func isRetryableLLMScenarioError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "stream error") ||
		strings.Contains(message, "internal_error") ||
		strings.Contains(message, "received from peer")
}

func validateLLMDialogueStageResult(stage llmDialogueStage, result *llmDialogueRunResult) error {
	if result == nil {
		return fmt.Errorf("stage %s 未返回结果", stage.Name)
	}
	if !result.UsedReader {
		return fmt.Errorf("stage %s 真实模型未调用 auramate-word-reader", stage.Name)
	}
	if !stage.SkipCopilotCheck && !result.UsedCopilot {
		return fmt.Errorf("stage %s 真实模型未调用 auramate-word-copilot", stage.Name)
	}
	if stage.RequireNoAction {
		if len(result.Actions) > 0 || result.Action != nil {
			return fmt.Errorf("stage %s 不应产出 word_editor_action", stage.Name)
		}
	} else if len(result.Actions) == 0 || result.Action == nil {
		return fmt.Errorf("stage %s 真实模型未产出 word_editor_action", stage.Name)
	}
	if !stage.RequireNoAction && stage.ExactActionCount > 0 && len(result.Actions) != stage.ExactActionCount {
		return fmt.Errorf("stage %s 最终动作数量不符合预期: got=%d want=%d", stage.Name, len(result.Actions), stage.ExactActionCount)
	}
	if stage.MinReaderCalls > 0 && result.ReaderCallCount < stage.MinReaderCalls {
		return fmt.Errorf("stage %s reader 调用次数不足: got=%d want>=%d", stage.Name, result.ReaderCallCount, stage.MinReaderCalls)
	}
	if stage.MaxReaderCalls > 0 && result.ReaderCallCount > stage.MaxReaderCalls {
		return fmt.Errorf("stage %s reader 调用次数过多: got=%d want<=%d", stage.Name, result.ReaderCallCount, stage.MaxReaderCalls)
	}
	if !stage.RequireNoAction && stage.ExpectedAction != "" && !strings.EqualFold(strings.TrimSpace(result.Action.Action), stage.ExpectedAction) {
		return fmt.Errorf("stage %s 动作类型不符合预期: got=%s want=%s", stage.Name, result.Action.Action, stage.ExpectedAction)
	}
	if !stage.RequireNoAction && stage.RequireParagraphStylePatch && !hasParagraphStylePatch(result.Action.ParagraphStylePatch) {
		return fmt.Errorf("stage %s 缺少 paragraph_style_patch", stage.Name)
	}
	if !stage.RequireNoAction && stage.ExpectedParagraphStyleID != "" && !strings.EqualFold(strings.TrimSpace(result.Action.ParagraphStylePatch.StyleID), stage.ExpectedParagraphStyleID) {
		return fmt.Errorf("stage %s paragraph_style_patch.style_id 不符合预期: got=%s want=%s", stage.Name, result.Action.ParagraphStylePatch.StyleID, stage.ExpectedParagraphStyleID)
	}
	if !stage.RequireNoAction && stage.RequireExplorationSummary && strings.TrimSpace(result.Action.ExplorationSummary) == "" {
		return fmt.Errorf("stage %s 缺少 exploration_summary", stage.Name)
	}
	return nil
}

func combineExecutionResults(results []*executionResult, actions []*editorActionInput) *executionResult {
	if len(results) == 0 {
		return nil
	}
	finalResult := results[len(results)-1]
	if finalResult == nil {
		return nil
	}
	if results[0] != nil && results[0].BeforeDocument != nil {
		finalResult.BeforeDocument = results[0].BeforeDocument
	}
	totalDiffCount := 0
	for _, item := range results {
		if item == nil {
			continue
		}
		if item.TotalDiffCount > 0 {
			totalDiffCount += item.TotalDiffCount
			continue
		}
		if item.ApplyResult != nil {
			totalDiffCount += len(item.ApplyResult.Diffs)
		}
	}
	finalResult.AppliedActions = actions
	finalResult.BatchResults = results
	finalResult.TotalDiffCount = totalDiffCount
	return finalResult
}

func stagedArtifactPath(finalArtifactPath string, stageIndex int) string {
	ext := filepath.Ext(finalArtifactPath)
	base := strings.TrimSuffix(finalArtifactPath, ext)
	if ext == "" {
		return fmt.Sprintf("%s.stage%d", finalArtifactPath, stageIndex)
	}
	return fmt.Sprintf("%s.stage%d%s", base, stageIndex, ext)
}

func (r *liveModelRunner) generateAction(prompt string, stopWhen func(*llmDialogueRunResult) bool) (*llmDialogueRunResult, error) {
	if r == nil || r.app == nil {
		return nil, fmt.Errorf("live model runner is not initialized")
	}
	agentService := r.app.NormalAgentNoSubagents
	if agentService == nil {
		agentService = r.app.NormalAgent
	}
	if agentService == nil {
		return nil, fmt.Errorf("normal agent is not available")
	}

	session, err := r.app.Sessions.Create(context.Background(), "goword-llm-e2e")
	if err != nil {
		return nil, fmt.Errorf("创建真实模型会话失败: %w", err)
	}
	defer r.app.Sessions.Delete(context.Background(), session.ID)

	runCtx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	runCtx = context.WithValue(runCtx, toolruntime.ModelOverrideCandidatesContextKey, []string{r.model})

	events, err := agentService.Run(runCtx, session.ID, prompt, "normal")
	if err != nil {
		return nil, fmt.Errorf("启动真实模型执行失败: %w", err)
	}

	result := &llmDialogueRunResult{}
	var assistantBuilder strings.Builder
	var currentActionBatch []*editorActionInput
	var currentRawActionBatch []map[string]any
	var actionBatches [][]*editorActionInput
	var rawActionBatches [][]map[string]any
	earlyStopTriggered := false
	toolTraceIndexByExecID := make(map[string]int)
	flushActionBatch := func() {
		if len(currentActionBatch) == 0 {
			return
		}
		batch := append([]*editorActionInput(nil), currentActionBatch...)
		actionBatches = append(actionBatches, batch)
		rawBatch := append([]map[string]any(nil), currentRawActionBatch...)
		rawActionBatches = append(rawActionBatches, rawBatch)
		currentActionBatch = currentActionBatch[:0]
		currentRawActionBatch = currentRawActionBatch[:0]
	}
	setResultActions := func(actions []*editorActionInput, rawActions []map[string]any) {
		result.Actions = actions
		result.RawActions = rawActions
		if len(actions) > 0 {
			result.Action = actions[0]
			return
		}
		result.Action = nil
	}
	maybeEarlyStop := func() {
		if earlyStopTriggered || stopWhen == nil {
			return
		}
		if !stopWhen(result) {
			return
		}
		earlyStopTriggered = true
		cancel()
	}
	var runErr error
	seenToolCallKeys := make(map[string]struct{})
	for event := range events {
		if event.Error != nil {
			runErr = event.Error
		}
		switch event.Type {
		case llmagent.AgentEventTypeToken:
			assistantBuilder.WriteString(event.Content)
		case llmagent.AgentEventTypeToolUse:
			if event.ToolCall != nil {
				toolCallKey := strings.TrimSpace(event.ToolCall.ID)
				if toolCallKey == "" {
					toolCallKey = strings.TrimSpace(event.ToolCall.Name) + "|" + strings.TrimSpace(event.ToolCall.Input)
				}
				if _, seen := seenToolCallKeys[toolCallKey]; !seen {
					seenToolCallKeys[toolCallKey] = struct{}{}
					result.ToolCalls = append(result.ToolCalls, event.ToolCall.Name)
					result.ToolTrace = append(result.ToolTrace, llmToolTrace{
						ExecutionID:  event.ToolExecutionID,
						Name:         event.ToolCall.Name,
						Input:        summarizeLLMToolInput(event.ToolCall.Name, event.ToolCall.Input),
						InputPreview: clipDetailText(strings.TrimSpace(event.ToolCall.Input), 220),
					})
					if event.ToolExecutionID != "" {
						toolTraceIndexByExecID[event.ToolExecutionID] = len(result.ToolTrace) - 1
					}
					toolName := strings.ToLower(event.ToolCall.Name)
					if strings.Contains(toolName, "word-reader") {
						flushActionBatch()
						result.UsedReader = true
						result.ReaderCallCount++
					}
					if strings.Contains(toolName, "word-copilot") {
						result.UsedCopilot = true
						result.CopilotCallCount++
					}
				}
			}
		case llmagent.AgentEventTypeToolResult:
			if event.ToolResult != nil {
				traceIndex, ok := toolTraceIndexByExecID[event.ToolExecutionID]
				if !ok {
					result.ToolTrace = append(result.ToolTrace, llmToolTrace{
						ExecutionID: event.ToolExecutionID,
						Name:        event.ToolResult.Name,
					})
					traceIndex = len(result.ToolTrace) - 1
					if event.ToolExecutionID != "" {
						toolTraceIndexByExecID[event.ToolExecutionID] = traceIndex
					}
				}
				result.ToolTrace[traceIndex].IsError = event.ToolResult.IsError
				result.ToolTrace[traceIndex].ResultPreview = clipDetailText(strings.TrimSpace(event.ToolResult.Content), 220)
				result.ToolTrace[traceIndex].ResultSummary = summarizeLLMToolResult(event.ToolResult.Name, event.ToolResult.Content, event.ToolResult.IsError)
				actions, rawSignals, parseErr := extractBridgeSignalsDetailed(event.ToolResult.Content)
				if parseErr == nil {
					currentActionBatch, currentRawActionBatch = appendUniqueActionsWithRaw(currentActionBatch, currentRawActionBatch, actions, rawSignals)
					setResultActions(currentActionBatch, currentRawActionBatch)
					maybeEarlyStop()
				}
			}
		case llmagent.AgentEventTypeUsage:
			if event.Usage != nil {
				result.InputTokens = maxInt64(result.InputTokens, event.Usage.InputTokens)
				result.OutputTokens = maxInt64(result.OutputTokens, event.Usage.OutputTokens)
				result.CacheReadTokens = maxInt64(result.CacheReadTokens, event.Usage.CacheReadTokens)
				result.CacheCreationTokens = maxInt64(result.CacheCreationTokens, event.Usage.CacheCreationTokens)
			}
		}
	}
	result.AssistantText = strings.TrimSpace(assistantBuilder.String())
	if runCtx.Err() != nil {
		if earlyStopTriggered && errors.Is(runCtx.Err(), context.Canceled) {
			goto finalize
		}
		return result, fmt.Errorf("真实模型执行超时: %w", runCtx.Err())
	}

finalize:
	if result.AssistantText != "" {
		actions, rawSignals, parseErr := extractBridgeSignalsDetailed(result.AssistantText)
		if parseErr == nil {
			currentActionBatch, currentRawActionBatch = appendUniqueActionsWithRaw(currentActionBatch, currentRawActionBatch, actions, rawSignals)
		}
	}
	if len(currentActionBatch) > 0 {
		setResultActions(currentActionBatch, currentRawActionBatch)
	} else if len(actionBatches) > 0 {
		last := len(actionBatches) - 1
		setResultActions(actionBatches[last], rawActionBatches[last])
	} else {
		setResultActions(nil, nil)
	}
	if runErr != nil {
		if earlyStopTriggered && errors.Is(runErr, context.Canceled) {
			return result, nil
		}
		return result, runErr
	}
	return result, nil
}

func shouldEarlyStopLLMScenario(scenario llmDialogueScenario, result *llmDialogueRunResult) bool {
	if len(scenario.Stages) > 0 || scenario.MinActionCount > 1 {
		return false
	}
	stage := llmDialogueStage{
		Name:                       scenario.Name,
		ExpectedAction:             scenario.ExpectedAction,
		MinReaderCalls:             scenario.MinReaderCalls,
		MaxReaderCalls:             scenario.MaxReaderCalls,
		RequireParagraphStylePatch: scenario.RequireParagraphStylePatch,
		ExpectedParagraphStyleID:   scenario.ExpectedParagraphStyleID,
		RequireExplorationSummary:  scenario.RequireExplorationSummary,
	}
	return shouldEarlyStopLLMStage(stage, result)
}

func shouldEarlyStopLLMStage(stage llmDialogueStage, result *llmDialogueRunResult) bool {
	if err := validateLLMDialogueStageResult(stage, result); err != nil {
		return false
	}
	if err := validateLLMActionContracts(result); err != nil {
		return false
	}
	return true
}

func buildLLMDialoguePrompt(documentPath, userInstruction string, promptHints []string) string {
	var builder strings.Builder
	builder.WriteString("[WORD_CONTEXT]\n")
	builder.WriteString("editing_policy: interface_only\n")
	builder.WriteString("notes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n")
	builder.WriteString(fmt.Sprintf("document_path: %s\n", documentPath))
	builder.WriteString("[/WORD_CONTEXT]\n\n")
	builder.WriteString(fmt.Sprintf("你现在要处理一个真实 Word 文档。\n文档路径：%s\n\n请严格遵守下面约束：", documentPath))
	instructions := []string{
		"1. 只能使用 read、auramate-word-reader 和 auramate-word-copilot 三类工具完成任务。",
		"2. 如果这些工具还没加载，只允许先用 manage_tools 加载它们；加载完成后不要再调用其他代码、文件、桌面或文档工具。",
		"3. 先自行判断取证路径：需要 docx 全文理解、模糊搜索或先看候选上下文时优先用 read；需要精确段落/表格/页眉页脚落点、真实 paragraph_id/table_id 或样式依据时再调用 auramate-word-reader。",
		"4. 如果第一次 reader 只定位到附近内容、出现多条候选命中，或用户使用‘中间/最后/那条/那段/那格/右边那个值/第几页/上一页/下一页’这类相对或页级定位描述，必须再次调用 auramate-word-reader，并使用 focus_hit_index、focus_paragraph_id、focus_table_id、focus_row_index、focus_cell_index、focus_page_index、expand_radius 展开局部上下文；涉及页级证据时优先补 include_layout=true，确认最终段落、单元格或页码。",
		"5. 只有在目标已经被 read/reader 的证据锁定后，才能调用 auramate-word-copilot 生成 word_editor_action 信号；如果 read 仍存在歧义，再补 auramate-word-reader exploration。单一改动通常只生成一条；如果用户一次要求多个彼此独立的精确编辑，可以按顺序多次调用 auramate-word-copilot，生成多条最小动作，每条只执行一个明确编辑。对依赖 exploration 的任务，每条动作都必须额外填写 exploration_summary，明确写出最终锁定的段落、单元格或页码，以及你依据了哪些邻域或 layout 上下文。不要拿同一份证据或同一份 payload 连续重复调用 auramate-word-copilot 试探；只有 checklist 中仍有未完成项时，才允许下一次 copilot 调用。",
		"6. 不要凭空臆测，不要跳过必要的 read/reader 取证，不要改动无关内容。",
		"7. 生成 word_editor_action 时，target_text 必须是文档里真实存在、可直接命中的原文片段；如果 reader 已返回 paragraph_id，优先回填 preferred_paragraph_id；如果已经依赖 preferred_paragraph_id、table_id、row_index/cell_index 或 exploration 结果，必须同时回填同一个 reader_session_id。",
		"8. 当用户使用‘中间/最后/那条/那段/那格/第几页/上一页/下一页’这类相对或页级定位描述时，必须绑定到 reader 的具体命中；如果已有 paragraph_id 或 table_id，必须回填，不能拿标题或抽象词替代。",
		"9. 对表格金额、总计、右侧值单元格这类任务，如果 query_hits 仍不足以唯一确认目标格，必须先看 exploration.table 的 row_window、column_cells、focus_cell，再决定是否生成 update_table_cell，并把最终格子的确认依据写进 exploration_summary；update_table_cell 以及表格行/列表达动作必须显式填写 row_index/cell_index，不能依赖默认 0。凡是 update_table_cell，table_cell 也必须填写最终单元格内容；如果单元格里同时有标题段和正文段，必须把保留段与改写后的正文段一起写入 table_cell。",
		"10. 对‘总价’‘总计’‘分项报价’‘金额’这类表格值修改，如果任务需要进入 reader 路径，第一次 reader 应优先走最小读取路径：显式传 context_scope=table、include_full_text=false、include_outline=false，先只拿 query_hits 和表格命中上下文；命中后优先用 focus_hit_index=0 做二次展开，而不是先读全文。",
		"11. table_count=1 只表示文档里有一个表格，不等于已经拿到了 table_id；禁止仅凭 table_count 臆造 focus_table_id，必须先从 query_hits、outline 或 exploration 结果里拿到真实 table_id。",
		"12. 如果一次请求要改同一张表里的多个单元格，拿到真实 table_id 后应优先用 focus_table_id 整表读取一次，复用同一 reader_session_id 和 table_id，再按顺序生成多条最小动作；不要为每个单元格都回退到全文重读，也不要把多处修改压成一条模糊动作。",
		"13. 选择动作时必须贴合 Word 语义：新增整段标题/正文/列表段落用 insert；在现有段落中按 Enter 拆成两段用 insert_paragraph_break；在现有段落中按 Shift+Enter 换行用 insert_line_break，禁止拿 insert 冒充段落断开或行内换行。",
		"14. 对 replace 或 update_table_cell 这类把原句、原段或原单元格内容改长的动作，必须先再次确认目标附近上下文，并在 reader_context_summary 或 format_basis 里明确说明这次修改是继续保持同一段/同一格，还是需要换行、断段或拆成多条动作；这类扩写任务至少需要两次 auramate-word-reader（首次命中 + 二次 exploration）后才能调用 auramate-word-copilot。如果结构会变化，必须显式改用 insert_paragraph_break、insert_line_break 或多条最小动作，禁止把结构变化硬塞进单条改写。",
		"15. 如果新增内容属于标题或列表段落，调用 auramate-word-copilot 时必须显式提供 paragraph_style_patch；标题插入至少要给出 style_id=HeadingN 或 Title，不能只靠 format_basis 让执行层猜样式。format_basis 也不能只写‘保持原格式/保持一致’，至少要说明 style_id、字体字号、对齐缩进，或命中单元格/页级 layout 依据。",
		"16. 如果用户一次提出多个独立编辑项，先在心里列出 checklist；每完成一项，就继续处理下一项，直到 checklist 全部都有对应的 word_editor_action。不要因为某一项已成功定位或已生成动作就提前结束。",
		"17. 在结束前必须自检：最终动作数量要与用户请求中的独立编辑项数量一致；如果还缺项，就继续 read/reader/auramate-word-copilot，而不是停止在分析文本。若 checklist 已全部完成，必须立即停止，不要再追加 manage_tools、read、auramate-word-reader、auramate-word-copilot，也不要为了解释原因再补发第 N+1 条动作。",
		"18. 除必要的工具调用外，不要输出长篇解释。",
	}
	for _, instruction := range instructions {
		builder.WriteString("\n")
		builder.WriteString(instruction)
	}
	builder.WriteString(fmt.Sprintf("\n\n用户原话：%s", userInstruction))
	if len(promptHints) > 0 {
		builder.WriteString("\n\n本场景额外约束：")
		for index, hint := range promptHints {
			trimmed := strings.TrimSpace(hint)
			if trimmed == "" {
				continue
			}
			builder.WriteString(fmt.Sprintf("\n%d. %s", index+1, trimmed))
		}
	}
	return strings.TrimSpace(builder.String())
}

func buildLLMDialogueDetails(model string, scenario llmDialogueScenario, inputDocPath string, llmResult *llmDialogueRunResult, execResult *executionResult, artifactPath string, durationMillis int64) map[string]any {
	details := map[string]any{
		"evaluation_layer": "llm_e2e",
		"model":            model,
	}
	if strings.TrimSpace(inputDocPath) != "" {
		details["scenario_input_document_path"] = inputDocPath
	}
	if artifactPath != "" {
		details["artifact_path"] = artifactPath
	}
	if llmResult != nil {
		details["tool_calls"] = llmResult.ToolCalls
		details["used_reader"] = llmResult.UsedReader
		details["used_copilot"] = llmResult.UsedCopilot
		details["reader_call_count"] = llmResult.ReaderCallCount
		details["copilot_call_count"] = llmResult.CopilotCallCount
		if len(llmResult.ToolTrace) > 0 {
			details["tool_trace"] = llmResult.ToolTrace
			details["tool_call_summary"] = buildLLMToolTraceSummary(llmResult.ToolTrace)
		}
		details["assistant_preview"] = clipDetailText(llmResult.AssistantText, 160)
		details["input_tokens"] = llmResult.InputTokens
		details["output_tokens"] = llmResult.OutputTokens
		details["cache_read_tokens"] = llmResult.CacheReadTokens
		details["cache_creation_tokens"] = llmResult.CacheCreationTokens
		if llmResult.Action != nil {
			details["action"] = llmResult.Action.Action
			details["target_text"] = llmResult.Action.TargetText
			details["new_text"] = llmResult.Action.NewText
			details["context_scope"] = llmResult.Action.ContextScope
			details["preferred_paragraph_id"] = llmResult.Action.PreferredParagraphID
			details["paragraph_style_patch_style_id"] = llmResult.Action.ParagraphStylePatch.StyleID
			details["reader_session_id"] = llmResult.Action.ReaderSessionID
			details["exploration_summary"] = llmResult.Action.ExplorationSummary
		}
		if len(llmResult.Actions) > 0 {
			details["action_count"] = len(llmResult.Actions)
			actions := make([]map[string]any, 0, len(llmResult.Actions))
			for _, action := range llmResult.Actions {
				if action == nil {
					continue
				}
				actions = append(actions, map[string]any{
					"action":           action.Action,
					"target_text":      action.TargetText,
					"new_text":         action.NewText,
					"table_id":         action.TableID,
					"row_index":        action.RowIndex,
					"cell_index":       action.CellIndex,
					"table_cell_texts": collectTableCellTexts(action.TableCell),
					"context_scope":    action.ContextScope,
				})
			}
			details["actions"] = actions
		}
		if budgetAssessment := buildLLMDialogueBudgetAssessment(scenario, llmResult, durationMillis); budgetAssessment != nil {
			details["budget_assessment"] = budgetAssessment
			if budgetStatus, ok := budgetAssessment["status"].(string); ok {
				details["budget_status"] = budgetStatus
			}
		}
	}
	if execResult != nil {
		if execResult.TotalDiffCount > 0 {
			details["diff_count"] = execResult.TotalDiffCount
		} else if execResult.ApplyResult != nil {
			details["diff_count"] = len(execResult.ApplyResult.Diffs)
		}
		if len(execResult.AppliedActions) > 0 {
			details["executed_action_count"] = len(execResult.AppliedActions)
		}
		if execResult.InsertedParagraph != nil {
			details["inserted_style_id"] = execResult.InsertedParagraph.Style.StyleID
		}
		if execResult.TemplateRole != "" {
			details["template_role"] = execResult.TemplateRole
		}
	}
	return details
}

func buildLLMDialogueBudgetAssessment(scenario llmDialogueScenario, llmResult *llmDialogueRunResult, durationMillis int64) map[string]any {
	if llmResult == nil {
		return nil
	}
	plannedSteps := maxInt(1, len(scenario.Stages))
	if scenario.MinActionCount > plannedSteps {
		plannedSteps = scenario.MinActionCount
	}
	if plannedSteps <= 1 {
		return nil
	}
	retryBudget := maxInt(1, llmDialogueStageMaxAttempts-1)
	warnReaderCalls := maxInt(scenario.MinReaderCalls+2+retryBudget, plannedSteps*(retryBudget+1)+2)
	failReaderCalls := warnReaderCalls + 2
	warnCopilotCalls := plannedSteps*(retryBudget+1) + 1
	failCopilotCalls := warnCopilotCalls + plannedSteps
	warnToolCalls := plannedSteps*(retryBudget+1)*2 + plannedSteps
	failToolCalls := warnToolCalls + plannedSteps*2
	warnInputTokens := int64(plannedSteps) * 90000
	failInputTokens := warnInputTokens + int64(plannedSteps)*60000
	warnDurationMillis := int64(plannedSteps) * 60000
	failDurationMillis := warnDurationMillis + int64(plannedSteps)*45000

	issues := make([]map[string]any, 0)
	status := "passed"
	updateStatus := func(next string) {
		if status == "failed" {
			return
		}
		if next == "failed" || (next == "warning" && status == "passed") {
			status = next
		}
	}
	recordMetric := func(metric string, actual, warn, fail int64) {
		metricStatus := "passed"
		if actual > fail {
			metricStatus = "failed"
		} else if actual > warn {
			metricStatus = "warning"
		}
		if metricStatus == "passed" {
			return
		}
		updateStatus(metricStatus)
		issues = append(issues, map[string]any{
			"metric": metric,
			"status": metricStatus,
			"actual": actual,
			"warn":   warn,
			"fail":   fail,
		})
	}

	recordMetric("reader_call_count", int64(llmResult.ReaderCallCount), int64(warnReaderCalls), int64(failReaderCalls))
	recordMetric("copilot_call_count", int64(llmResult.CopilotCallCount), int64(warnCopilotCalls), int64(failCopilotCalls))
	recordMetric("tool_call_count", int64(len(llmResult.ToolCalls)), int64(warnToolCalls), int64(failToolCalls))
	if llmResult.InputTokens > 0 {
		recordMetric("input_tokens", llmResult.InputTokens, warnInputTokens, failInputTokens)
	}
	if durationMillis > 0 {
		recordMetric("duration_ms", durationMillis, warnDurationMillis, failDurationMillis)
	}

	result := map[string]any{
		"status":        status,
		"planned_steps": plannedSteps,
		"metrics": map[string]any{
			"reader_call_count":  llmResult.ReaderCallCount,
			"copilot_call_count": llmResult.CopilotCallCount,
			"tool_call_count":    len(llmResult.ToolCalls),
			"input_tokens":       llmResult.InputTokens,
			"duration_ms":        durationMillis,
		},
		"limits": map[string]any{
			"reader_call_count":  map[string]any{"warn": warnReaderCalls, "fail": failReaderCalls},
			"copilot_call_count": map[string]any{"warn": warnCopilotCalls, "fail": failCopilotCalls},
			"tool_call_count":    map[string]any{"warn": warnToolCalls, "fail": failToolCalls},
			"input_tokens":       map[string]any{"warn": warnInputTokens, "fail": failInputTokens},
			"duration_ms":        map[string]any{"warn": warnDurationMillis, "fail": failDurationMillis},
		},
	}
	if len(issues) > 0 {
		result["issues"] = issues
	}
	return result
}

func summarizeLLMEndToEndSuite(model string, started time.Time, results []scenarioResult) *modelE2ESummary {
	if len(results) == 0 {
		return nil
	}
	summary := &modelE2ESummary{
		Model:          model,
		ScenarioCount:  len(results),
		DurationMillis: time.Since(started).Milliseconds(),
	}
	for _, item := range results {
		if item.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}
	if summary.Failed == 0 {
		summary.Summary = fmt.Sprintf("真实模型 %s 的 %d 个端到端对话场景已全部通过。", model, summary.ScenarioCount)
		return summary
	}
	summary.Summary = fmt.Sprintf("真实模型 %s 的端到端对话场景共 %d 个，通过 %d 个，失败 %d 个。", model, summary.ScenarioCount, summary.Passed, summary.Failed)
	return summary
}

func collectTableCellTexts(cell *goword.TableCellInput) []string {
	if cell == nil || len(cell.Paragraphs) == 0 {
		return nil
	}
	texts := make([]string, 0, len(cell.Paragraphs))
	for _, paragraph := range cell.Paragraphs {
		trimmed := strings.TrimSpace(paragraph.Text)
		if trimmed == "" {
			continue
		}
		texts = append(texts, trimmed)
	}
	if len(texts) == 0 {
		return nil
	}
	return texts
}

func summarizeLLMToolInput(name, input string) map[string]any {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	toolName := strings.ToLower(strings.TrimSpace(name))
	if toolName == "read" {
		return map[string]any{"request": clipDetailText(trimmed, 180)}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return map[string]any{"request": clipDetailText(trimmed, 180)}
	}
	if strings.Contains(toolName, "word-reader") {
		summary := map[string]any{}
		copyKnownMapField(summary, payload, "path")
		copyKnownMapField(summary, payload, "session_id")
		copyKnownMapField(summary, payload, "query")
		copyKnownMapField(summary, payload, "context_scope")
		copyKnownMapField(summary, payload, "focus_hit_index")
		copyKnownMapField(summary, payload, "focus_paragraph_id")
		copyKnownMapField(summary, payload, "focus_table_id")
		copyKnownMapField(summary, payload, "focus_row_index")
		copyKnownMapField(summary, payload, "focus_cell_index")
		copyKnownMapField(summary, payload, "focus_page_index")
		copyKnownMapField(summary, payload, "expand_radius")
		copyKnownMapField(summary, payload, "include_layout")
		copyKnownMapField(summary, payload, "include_full_text")
		copyKnownMapField(summary, payload, "include_outline")
		copyKnownMapField(summary, payload, "max_chars")
		copyKnownMapField(summary, payload, "max_hits")
		return summary
	}
	if strings.Contains(toolName, "word-copilot") {
		summary := map[string]any{}
		copyKnownMapField(summary, payload, "action")
		if value, ok := payload["target_text"].(string); ok {
			summary["target_text"] = clipDetailText(value, 120)
		}
		if value, ok := payload["new_text"].(string); ok {
			summary["new_text"] = clipDetailText(value, 120)
		}
		copyKnownMapField(summary, payload, "position")
		copyKnownMapField(summary, payload, "context_scope")
		copyKnownMapField(summary, payload, "preferred_paragraph_id")
		copyKnownMapField(summary, payload, "table_id")
		copyKnownMapField(summary, payload, "row_index")
		copyKnownMapField(summary, payload, "cell_index")
		copyKnownMapField(summary, payload, "reader_session_id")
		if value, ok := payload["reader_context_summary"].(string); ok {
			summary["reader_context_summary"] = clipDetailText(value, 140)
		}
		if value, ok := payload["format_basis"].(string); ok {
			summary["format_basis"] = clipDetailText(value, 140)
		}
		if value, ok := payload["exploration_summary"].(string); ok {
			summary["exploration_summary"] = clipDetailText(value, 140)
		}
		if patch, ok := payload["paragraph_style_patch"].(map[string]any); ok {
			copyKnownMapField(summary, patch, "style_id")
			if value, ok := summary["style_id"]; ok {
				summary["paragraph_style_patch_style_id"] = value
				delete(summary, "style_id")
			}
		}
		return summary
	}
	return map[string]any{"request": clipDetailText(trimmed, 180)}
}

func summarizeLLMToolResult(name, content string, isError bool) map[string]any {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if isError {
		return map[string]any{"status": "error", "message": clipDetailText(trimmed, 200)}
	}
	toolName := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(toolName, "word-reader") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return map[string]any{"status": "unparsed", "message": clipDetailText(trimmed, 200)}
		}
		summary := map[string]any{}
		copyKnownMapField(summary, payload, "status")
		if session, ok := payload["session"].(map[string]any); ok {
			copyKnownMapField(summary, session, "id")
			if value, ok := summary["id"]; ok {
				summary["session_id"] = value
				delete(summary, "id")
			}
		}
		if query, ok := payload["query"].(map[string]any); ok {
			copyKnownMapField(summary, query, "scope")
			copyKnownMapField(summary, query, "hit_count")
			if hits, ok := query["hits"].([]any); ok {
				summary["top_hits"] = summarizeReaderHits(hits, 3)
			}
		}
		if exploration, ok := payload["exploration"].(map[string]any); ok {
			summary["exploration"] = summarizeReaderExploration(exploration)
		}
		if guidance, ok := payload["guidance"].(map[string]any); ok {
			if recommendations, ok := guidance["recommendations"].([]any); ok && len(recommendations) > 0 {
				summary["recommendations"] = summarizeStringArray(recommendations, 3, 120)
			}
		}
		if warnings, ok := payload["warnings"].([]any); ok && len(warnings) > 0 {
			summary["warnings"] = summarizeStringArray(warnings, 3, 120)
		}
		return summary
	}
	if strings.Contains(toolName, "word-copilot") {
		actions, _, err := extractBridgeSignalsDetailed(trimmed)
		if err != nil {
			return map[string]any{"status": "ok", "message": clipDetailText(trimmed, 200)}
		}
		summary := map[string]any{"status": "ok", "signal_count": len(actions)}
		if len(actions) > 0 {
			summary["signals"] = summarizeEditorActions(actions, 3)
		}
		return summary
	}
	return map[string]any{"status": "ok", "message": clipDetailText(trimmed, 200)}
}

func summarizeReaderHits(hits []any, limit int) []map[string]any {
	result := make([]map[string]any, 0, minInt(limit, len(hits)))
	for _, item := range hits {
		if len(result) >= limit {
			break
		}
		hit, ok := item.(map[string]any)
		if !ok {
			continue
		}
		summary := map[string]any{}
		copyKnownMapField(summary, hit, "type")
		copyKnownMapField(summary, hit, "paragraph_id")
		copyKnownMapField(summary, hit, "table_id")
		copyKnownMapField(summary, hit, "row_index")
		copyKnownMapField(summary, hit, "cell_index")
		if value, ok := hit["preview"].(string); ok {
			summary["preview"] = clipDetailText(value, 80)
		}
		if matched, ok := hit["matched_queries"].([]any); ok && len(matched) > 0 {
			summary["matched_queries"] = summarizeStringArray(matched, 3, 40)
		}
		result = append(result, summary)
	}
	return result
}

func summarizeReaderExploration(exploration map[string]any) map[string]any {
	summary := map[string]any{}
	if paragraph, ok := exploration["paragraph"].(map[string]any); ok {
		paragraphSummary := map[string]any{}
		copyKnownMapField(paragraphSummary, paragraph, "paragraph_id")
		if anchor, ok := paragraph["anchor"].(map[string]any); ok {
			copyKnownMapField(paragraphSummary, anchor, "table_id")
			copyKnownMapField(paragraphSummary, anchor, "row_index")
			copyKnownMapField(paragraphSummary, anchor, "cell_index")
		}
		if len(paragraphSummary) > 0 {
			summary["paragraph"] = paragraphSummary
		}
	}
	if table, ok := exploration["table"].(map[string]any); ok {
		tableSummary := map[string]any{}
		copyKnownMapField(tableSummary, table, "table_id")
		copyKnownMapField(tableSummary, table, "row_index")
		copyKnownMapField(tableSummary, table, "cell_index")
		copyKnownMapField(tableSummary, table, "row_count")
		copyKnownMapField(tableSummary, table, "cell_count")
		if len(tableSummary) > 0 {
			summary["table"] = tableSummary
		}
	}
	if layout, ok := exploration["layout"].(map[string]any); ok {
		layoutSummary := map[string]any{}
		copyKnownMapField(layoutSummary, layout, "focus_page_index")
		if pages, ok := layout["pages"].([]any); ok {
			layoutSummary["page_count"] = len(pages)
		}
		if len(layoutSummary) > 0 {
			summary["layout"] = layoutSummary
		}
	}
	return summary
}

func summarizeEditorActions(actions []*editorActionInput, limit int) []map[string]any {
	result := make([]map[string]any, 0, minInt(limit, len(actions)))
	for _, action := range actions {
		if action == nil || len(result) >= limit {
			break
		}
		summary := map[string]any{
			"action":        action.Action,
			"target_text":   clipDetailText(action.TargetText, 80),
			"context_scope": action.ContextScope,
		}
		if strings.TrimSpace(action.NewText) != "" {
			summary["new_text"] = clipDetailText(action.NewText, 80)
		}
		if strings.TrimSpace(action.PreferredParagraphID) != "" {
			summary["preferred_paragraph_id"] = action.PreferredParagraphID
		}
		if strings.TrimSpace(action.TableID) != "" {
			summary["table_id"] = action.TableID
		}
		if action.RowIndex != 0 {
			summary["row_index"] = action.RowIndex
		}
		if action.CellIndex != 0 {
			summary["cell_index"] = action.CellIndex
		}
		result = append(result, summary)
	}
	return result
}

func summarizeStringArray(items []any, limit int, clipLimit int) []string {
	result := make([]string, 0, minInt(limit, len(items)))
	for _, item := range items {
		if len(result) >= limit {
			break
		}
		trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
		if trimmed == "" {
			continue
		}
		result = append(result, clipDetailText(trimmed, clipLimit))
	}
	return result
}

func buildLLMToolTraceSummary(traces []llmToolTrace) map[string]any {
	if len(traces) == 0 {
		return nil
	}
	countsByTool := make(map[string]int)
	readerQueries := make([]string, 0)
	copilotActions := make([]string, 0)
	signatureCounts := make(map[string]int)
	signatureSummary := make(map[string]map[string]any)
	for _, trace := range traces {
		countsByTool[trace.Name]++
		signature := trace.Name + "|" + stableMapSignature(trace.Input)
		signatureCounts[signature]++
		if _, exists := signatureSummary[signature]; !exists {
			signatureSummary[signature] = map[string]any{
				"name":  trace.Name,
				"stage": trace.Stage,
				"input": trace.Input,
			}
		}
		if strings.Contains(strings.ToLower(trace.Name), "word-reader") {
			if query, ok := trace.Input["query"].(string); ok && strings.TrimSpace(query) != "" {
				readerQueries = append(readerQueries, query)
			}
		}
		if strings.Contains(strings.ToLower(trace.Name), "word-copilot") {
			if action, ok := trace.Input["action"].(string); ok && strings.TrimSpace(action) != "" {
				copilotActions = append(copilotActions, action)
			}
		}
	}
	duplicates := make([]map[string]any, 0)
	for signature, count := range signatureCounts {
		if count <= 1 {
			continue
		}
		item := map[string]any{"count": count}
		for key, value := range signatureSummary[signature] {
			item[key] = value
		}
		duplicates = append(duplicates, item)
	}
	sort.Slice(duplicates, func(i, j int) bool {
		left, _ := duplicates[i]["count"].(int)
		right, _ := duplicates[j]["count"].(int)
		if left == right {
			return fmt.Sprintf("%v", duplicates[i]["name"]) < fmt.Sprintf("%v", duplicates[j]["name"])
		}
		return left > right
	})
	return map[string]any{
		"counts_by_tool":          countsByTool,
		"reader_queries":          readerQueries,
		"copilot_actions":         copilotActions,
		"duplicate_call_count":    len(duplicates),
		"duplicate_calls":         duplicates,
		"unique_tool_call_count":  len(signatureCounts),
		"total_traced_call_count": len(traces),
	}
}

func stableMapSignature(value map[string]any) string {
	if len(value) == 0 {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

func copyKnownMapField(target map[string]any, source map[string]any, key string) {
	if target == nil || source == nil {
		return
	}
	if value, ok := source[key]; ok && value != nil {
		target[key] = value
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func llmE2ESuggestions() []string {
	return []string{
		"若真实模型没有先读文档就尝试编辑，应优先强化 prompt 中的 reader 前置约束，并检查技能描述是否清晰暴露 path/query/scope 参数。",
		"若真实模型调用了工具但没有返回 __WORD_EDITOR_SIG__，应优先检查 auramate-word-copilot 的工具结果是否完整透传给 agent 事件流。",
		"若真实模型已拿到 preferred_paragraph_id、table_id、row_index/cell_index 或 exploration 结果却仍被 bridge 拒绝，应优先检查是否同步回填了同一 reader_session_id。",
		"若真实模型在表格动作上继续依赖默认第 0 行/列，应优先强化 row_index/cell_index 必须显式回填的约束，而不是放宽执行层兜底。",
		"若真实模型把 format_basis 写成‘保持原格式/保持一致’，应优先补强 prompt 中对 style_id、字体字号、对齐缩进或 layout 证据的要求。",
		"若用户一次要求多个独立编辑，应优先检查是否允许多次 auramate-word-copilot 调用并顺序执行多条最小动作，而不是强行压成单条信号。",
		"若真实模型动作正确但保存复开后校验失败，应优先检查 executeEditorAction 与 SaveDOCX 对真实工具返回 action 的兼容性。",
	}
}

func verifyLLMTableBatchUpdateDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("批量表格场景缺少编辑前基线快照")
	}
	throughputCell := findParagraphByText(reopened, "92%")
	if throughputCell == nil {
		return fmt.Errorf("表格中未找到更新后的通过率 92%%")
	}
	if !tableContainsExactText(reopened, "92%") {
		return fmt.Errorf("表格中未找到更新后的通过率 92%%")
	}
	if tableContainsExactText(reopened, "85%") {
		return fmt.Errorf("旧的通过率 85%% 仍然存在")
	}
	statusCell := findParagraphByText(reopened, "无需人工复核")
	if statusCell == nil {
		return fmt.Errorf("表格中未找到更新后的右侧结果")
	}
	if !tableContainsExactText(reopened, "无需人工复核") {
		return fmt.Errorf("表格中未找到更新后的右侧结果")
	}
	if tableContainsExactText(reopened, "需人工复核") {
		return fmt.Errorf("旧的右侧结果仍然存在")
	}
	if !tableCellContainsTexts(reopened, "风险提示", "上线前先确认灰度范围。") {
		return fmt.Errorf("风险提示单元格内未找到新增说明")
	}
	if !tableCellContainsTexts(reopened, "风险提示", "当前为灰度验证阶段。") {
		return fmt.Errorf("风险提示单元格原正文被误删")
	}
	if !tableContainsText(reopened, "指标") || !tableContainsText(reopened, "当前值") {
		return fmt.Errorf("表格表头被误修改")
	}
	riskCell := findTableCellContainingTexts(reopened, "风险提示", "当前为灰度验证阶段。", "上线前先确认灰度范围。")
	if riskCell == nil {
		return fmt.Errorf("未找到批量更新后的风险提示单元格")
	}
	beforeRiskCell := findTableCellContainingTexts(result.BeforeDocument, "风险提示", "当前为灰度验证阶段。")
	if beforeRiskCell == nil {
		return fmt.Errorf("编辑前未找到风险提示单元格基线")
	}
	beforeRiskSnapshot := buildTableCellStructureSnapshot(beforeRiskCell)
	if len(beforeRiskSnapshot.Paragraphs) != 2 {
		return fmt.Errorf("编辑前风险提示单元格基线段落数量异常: got=%d want=2", len(beforeRiskSnapshot.Paragraphs))
	}
	beforeThroughput := findParagraphByText(result.BeforeDocument, "85%")
	if beforeThroughput == nil {
		return fmt.Errorf("编辑前未找到通过率单元格基线")
	}
	beforeStatus := findParagraphByText(result.BeforeDocument, "需人工复核")
	if beforeStatus == nil {
		return fmt.Errorf("编辑前未找到右侧结果单元格基线")
	}
	riskParagraphs := tableCellParagraphsContainingTexts(reopened, "风险提示", "当前为灰度验证阶段。", "上线前先确认灰度范围。")
	if len(riskParagraphs) != 3 {
		return fmt.Errorf("风险提示单元格段落数量异常: got=%d want=3", len(riskParagraphs))
	}
	expectedRiskSnapshot := expectedTableCellSnapshot(
		beforeRiskSnapshot.Paragraphs[0],
		beforeRiskSnapshot.Paragraphs[1],
		paragraphSnapshotWithText(beforeRiskSnapshot.Paragraphs[1], "上线前先确认灰度范围。"),
	)
	if err := assertTableCellSnapshot(riskCell, expectedRiskSnapshot, "风险提示单元格"); err != nil {
		return err
	}
	if err := assertParagraphSnapshot(throughputCell, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeThroughput), "92%"), "通过率单元格"); err != nil {
		return err
	}
	if err := assertParagraphSnapshot(statusCell, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeStatus), "无需人工复核"), "右侧结果单元格"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "表头左单元格", "指标"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "表头右单元格", "当前值"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "通过率标签单元格", "通过率"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "文档标题段", "Auramate 智能编辑综合验证报告"); err != nil {
		return err
	}
	if result == nil || len(result.AppliedActions) != 3 {
		return fmt.Errorf("批量表格场景实际执行动作数量异常: got=%d want=3", len(result.AppliedActions))
	}
	if countResultActionType(result, "update_table_cell") != 2 || countResultActionType(result, "insert") != 1 {
		return fmt.Errorf("批量表格场景动作类型异常: update_table_cell=%d insert=%d", countResultActionType(result, "update_table_cell"), countResultActionType(result, "insert"))
	}
	return nil
}

func verifyLLMExternalContractTableBatchDocument(reopened *goword.Document, result *executionResult, targets externalContractTableTargets) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("真实合同表格场景缺少编辑前基线快照")
	}
	if !tableContainsExactText(reopened, targets.TaxIncluded) {
		return fmt.Errorf("产品清单表格中未找到更新后的含税单价 %s", targets.TaxIncluded)
	}
	if !tableContainsExactText(reopened, targets.TaxExcluded) {
		return fmt.Errorf("产品清单表格中未找到更新后的不含税单价 %s", targets.TaxExcluded)
	}
	if !tableCellContainsTexts(reopened, "合计含税人民币总金额", targets.SummaryGross, targets.SummaryNet) {
		return fmt.Errorf("产品清单表格中未找到更新后的合计单元格")
	}
	beforeBodyPayment := findParagraphContainingAll(result.BeforeDocument, "合同款", "支付给乙方")
	if beforeBodyPayment == nil {
		return fmt.Errorf("编辑前未找到正文合同款条款")
	}
	afterBodyPayment := findParagraphContainingAll(reopened, "合同款", "支付给乙方")
	if afterBodyPayment == nil {
		return fmt.Errorf("编辑后未找到正文合同款条款")
	}
	if err := assertParagraphSnapshot(afterBodyPayment, buildParagraphStructureSnapshot(beforeBodyPayment), "正文合同款条款"); err != nil {
		return err
	}
	if len(result.AppliedActions) != 3 {
		return fmt.Errorf("真实合同表格场景实际执行动作数量异常: got=%d want=3", len(result.AppliedActions))
	}
	if countResultActionType(result, "update_table_cell") != 3 {
		return fmt.Errorf("真实合同表格场景动作类型异常: update_table_cell=%d want=3", countResultActionType(result, "update_table_cell"))
	}
	return nil
}

func verifyLLMMiddleNoteDocument(reopened *goword.Document, result *executionResult) error {
	editedParagraph := findParagraphByText(reopened, "验证说明（审阅项）：当前版本仍需人工复核。")
	if editedParagraph == nil {
		editedParagraph = findParagraphByText(reopened, "审阅说明：当前版本仍需人工复核。")
	}
	if editedParagraph == nil {
		return fmt.Errorf("未正确改写人工复核说明段")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("中间说明改写场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "验证说明：当前版本仍需人工复核。")
	if before == nil {
		return fmt.Errorf("编辑前未找到验证说明基线")
	}
	if err := assertParagraphSnapshot(editedParagraph, paragraphSnapshotWithText(buildParagraphStructureSnapshot(before), editedParagraph.PlainText()), "改写后的中间说明段"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "交付说明段", "交付说明：交付物以验收版本为准。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	return nil
}

func verifyLLMFeeInsertDocument(reopened *goword.Document, result *executionResult) error {
	vendorParagraph := findParagraphByText(reopened, "第三方费用由甲方另行承担，并在采购前单独确认。")
	if vendorParagraph == nil {
		return fmt.Errorf("保存后未找到第三方费用条款")
	}
	travelParagraph := findParagraphByText(reopened, "差旅费用由乙方内部报销，不计入本次报价。")
	if travelParagraph == nil {
		return fmt.Errorf("差旅费用条款被误修改")
	}
	nextParagraph := paragraphImmediatelyAfterText(reopened, "第三方费用由甲方另行承担，并在采购前单独确认。")
	nextContainsApproval := nextParagraph != nil && (strings.Contains(nextParagraph.PlainText(), "采购审批") || strings.Contains(nextParagraph.PlainText(), "审批备案"))
	if !nextContainsApproval {
		return fmt.Errorf("保存后未找到针对第三方费用条款的审批补充")
	}
	if result != nil && nextContainsApproval {
		result.InsertedParagraph = nextParagraph
	}
	if paragraphImmediatelyAfterText(reopened, "差旅费用由乙方内部报销，不计入本次报价。") == nextParagraph && nextContainsApproval {
		return fmt.Errorf("新增审批说明错误插入到差旅费用条款后")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("第三方费用补句场景缺少编辑前基线快照")
	}
	beforeVendor := findParagraphByText(result.BeforeDocument, "第三方费用由甲方另行承担，并在采购前单独确认。")
	if beforeVendor == nil {
		return fmt.Errorf("编辑前未找到第三方费用条款基线")
	}
	if err := assertParagraphSnapshot(nextParagraph, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeVendor), nextParagraph.PlainText()), "第三方费用后的审批说明段"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "第三方费用条款", "第三方费用由甲方另行承担，并在采购前单独确认。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "差旅费用条款", "差旅费用由乙方内部报销，不计入本次报价。"); err != nil {
		return err
	}
	return nil
}

func verifyLLMTailRemarkDocument(reopened *goword.Document, result *executionResult) error {
	editedParagraph := findParagraphByText(reopened, "附录说明（归档备注）：本报告用于自动化回归。")
	if editedParagraph == nil {
		return fmt.Errorf("未正确改写附录备注段")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("验证说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("尾部备注改写场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "附录说明：本报告用于自动化回归。")
	if before == nil {
		return fmt.Errorf("编辑前未找到附录说明基线")
	}
	if err := assertParagraphSnapshot(editedParagraph, paragraphSnapshotWithText(buildParagraphStructureSnapshot(before), editedParagraph.PlainText()), "改写后的附录备注段"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "交付说明段", "交付说明：交付物以验收版本为准。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "验证说明段", "验证说明：当前版本仍需人工复核。"); err != nil {
		return err
	}
	return nil
}

func verifyLLMListExplanationInsertDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	inserted := findParagraphByText(reopened, "需同步记录审批结论。")
	if inserted == nil {
		return fmt.Errorf("未找到新增的列表后说明正文")
	}
	if inserted.Style.Numbering != nil {
		return fmt.Errorf("列表后说明正文错误继承了编号: %+v", inserted.Style.Numbering)
	}
	if normalizeStyleID(inserted.Style.StyleID) != "normal" {
		return fmt.Errorf("列表后说明正文样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if paragraphImmediatelyAfterText(reopened, "完成样式评审") != inserted {
		return fmt.Errorf("新增说明正文未插入到目标列表项后")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("列表说明插入场景缺少编辑前基线快照")
	}
	beforeNote := findParagraphByText(result.BeforeDocument, "以上步骤需保留审计记录。")
	if beforeNote == nil {
		return fmt.Errorf("编辑前未找到列表说明正文基线")
	}
	if err := assertParagraphSnapshot(inserted, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeNote), "需同步记录审批结论。"), "列表后的新增说明正文"); err != nil {
		return err
	}
	firstItem := findParagraphByText(reopened, "收集需求清单")
	if firstItem == nil {
		return fmt.Errorf("第一条列表项被误修改或丢失")
	}
	if normalizeStyleID(firstItem.Style.StyleID) != "listparagraph" || firstItem.Style.Numbering == nil {
		return fmt.Errorf("第一条列表项样式或编号异常: style_id=%s numbering=%+v", firstItem.Style.StyleID, firstItem.Style.Numbering)
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "现有列表说明段", "以上步骤需保留审计记录。"); err != nil {
		return err
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	return nil
}

func verifyLLMBoldLabelBodyInsertWhitespaceDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	inserted := findParagraphByText(reopened, " 新增一条：坚持闭环验收。")
	if inserted == nil {
		return fmt.Errorf("未找到带前导空格的新增正文")
	}
	if paragraphImmediatelyAfterText(reopened, "基本原则") != inserted {
		return fmt.Errorf("新增正文未插入到“基本原则”后")
	}
	followingBody := paragraphImmediatelyAfterText(reopened, " 新增一条：坚持闭环验收。")
	if followingBody == nil || followingBody.PlainText() != "坚持问题导向，确保制度可执行。" {
		return fmt.Errorf("原有正文未保持在新增正文之后")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("标签段后正文插入场景缺少编辑前基线快照")
	}
	beforeBody := findParagraphByText(result.BeforeDocument, "坚持问题导向，确保制度可执行。")
	if beforeBody == nil {
		return fmt.Errorf("编辑前未找到正文模板基线")
	}
	if err := assertParagraphSnapshot(inserted, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeBody), " 新增一条：坚持闭环验收。"), "基本原则后的新增正文"); err != nil {
		return err
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	return nil
}

func verifyLLMMultilevelListInsertDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	inserted := findParagraphByText(reopened, "确认跨页表头")
	if inserted == nil {
		return fmt.Errorf("未找到新增的同级子步骤")
	}
	if inserted.Style.Numbering == nil {
		return fmt.Errorf("新增子步骤缺少编号")
	}
	if strings.TrimSpace(inserted.Style.Numbering.NumID) != "9" || inserted.Style.Numbering.Level != 1 {
		return fmt.Errorf("新增子步骤编号层级异常: %+v", inserted.Style.Numbering)
	}
	if normalizeStyleID(inserted.Style.StyleID) != "listparagraphlevel2" {
		return fmt.Errorf("新增子步骤样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if paragraphImmediatelyAfterText(reopened, "核对表格样式") != inserted {
		return fmt.Errorf("新增子步骤未插入到目标子步骤后")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("多级编号场景缺少编辑前基线快照")
	}
	beforeSibling := findParagraphByText(result.BeforeDocument, "核对标题层级")
	if beforeSibling == nil {
		return fmt.Errorf("编辑前未找到同级子步骤基线")
	}
	expected := paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeSibling), "确认跨页表头")
	expected.NumberingText = ""
	if err := assertParagraphSnapshot(inserted, expected, "新增同级子步骤"); err != nil {
		return err
	}
	parentItem := findParagraphByText(reopened, "收集需求清单")
	if parentItem == nil || parentItem.Style.Numbering == nil || parentItem.Style.Numbering.Level != 0 {
		return fmt.Errorf("一级列表项层级异常")
	}
	sibling := findParagraphByText(reopened, "核对标题层级")
	if sibling == nil || normalizeStyleID(sibling.Style.StyleID) != "listparagraphlevel2" || sibling.Style.Numbering == nil || strings.TrimSpace(sibling.Style.Numbering.NumID) != "9" || sibling.Style.Numbering.Level != 1 {
		return fmt.Errorf("同级子步骤基线项样式或编号异常")
	}
	nextTopLevel := findParagraphByText(reopened, "完成样式评审")
	if nextTopLevel == nil || normalizeStyleID(nextTopLevel.Style.StyleID) != "listparagraph" || nextTopLevel.Style.Numbering == nil || strings.TrimSpace(nextTopLevel.Style.Numbering.NumID) != "9" || nextTopLevel.Style.Numbering.Level != 0 {
		return fmt.Errorf("后续一级步骤样式或编号异常")
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	return nil
}

func verifyLLMMultilevelListMidInsertContinuationDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	inserted := findParagraphByText(reopened, "补齐编号映射")
	if inserted == nil {
		return fmt.Errorf("未找到中间插入的同级子步骤")
	}
	if inserted.Style.Numbering == nil {
		return fmt.Errorf("新增子步骤缺少编号")
	}
	if strings.TrimSpace(inserted.Style.Numbering.NumID) != "9" || inserted.Style.Numbering.Level != 1 {
		return fmt.Errorf("新增子步骤编号层级异常: %+v", inserted.Style.Numbering)
	}
	if normalizeStyleID(inserted.Style.StyleID) != "listparagraphlevel2" {
		return fmt.Errorf("新增子步骤样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if paragraphImmediatelyAfterText(reopened, "核对标题层级") != inserted {
		return fmt.Errorf("新增子步骤未插入到目标子步骤后")
	}
	if paragraphImmediatelyAfterText(reopened, "补齐编号映射") != findParagraphByText(reopened, "核对表格样式") {
		return fmt.Errorf("后续同级子步骤未保持在新增项之后")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("中间插入续号场景缺少编辑前基线快照")
	}
	beforeSibling := findParagraphByText(result.BeforeDocument, "核对标题层级")
	if beforeSibling == nil {
		return fmt.Errorf("编辑前未找到同级子步骤基线")
	}
	expected := paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeSibling), "补齐编号映射")
	expected.NumberingText = ""
	if err := assertParagraphSnapshot(inserted, expected, "中间插入的同级子步骤"); err != nil {
		return err
	}
	leading := findParagraphByText(reopened, "核对标题层级")
	trailing := findParagraphByText(reopened, "核对表格样式")
	if leading == nil || trailing == nil {
		return fmt.Errorf("缺少用于续号校验的相邻子步骤")
	}
	if got := effectiveParagraphNumberingText(reopened, leading); got != "1.1" {
		return fmt.Errorf("前置子步骤显示编号异常: got=%s want=1.1", got)
	}
	if got := effectiveParagraphNumberingText(reopened, inserted); got != "1.2" {
		return fmt.Errorf("新增子步骤显示编号异常: got=%s want=1.2", got)
	}
	if got := effectiveParagraphNumberingText(reopened, trailing); got != "1.3" {
		return fmt.Errorf("后续子步骤显示编号异常: got=%s want=1.3", got)
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	return nil
}

func verifyLLMRiskCellDocument(reopened *goword.Document, result *executionResult) error {
	inserted := findParagraphByText(reopened, "灰度范围")
	if inserted == nil {
		return fmt.Errorf("保存后未找到新增风险说明")
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	if !tableCellContainsTexts(reopened, "风险提示", "灰度范围") {
		return fmt.Errorf("新增风险说明未插入到目标单元格")
	}
	if normalizeStyleID(inserted.Style.StyleID) != "tablebody" {
		return fmt.Errorf("新增风险说明样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if !tableContainsText(reopened, "85%") {
		return fmt.Errorf("通过率单元格被误修改")
	}
	if !tableContainsText(reopened, "需人工复核") {
		return fmt.Errorf("右侧风控提示被误修改")
	}
	return nil
}

func verifyLLMInsertTableRowStructuredDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("表格整行插入场景缺少编辑前基线快照")
	}
	beforeTable := findTableContainingTexts(result.BeforeDocument, "考核维度", "工作业绩", "团队贡献")
	afterTable := findTableContainingTexts(reopened, "考核维度", "工作业绩", "团队贡献", "安全合规")
	if beforeTable == nil || afterTable == nil {
		return fmt.Errorf("缺少用于整行插入校验的目标表格")
	}
	if len(beforeTable.Rows) != 5 {
		return fmt.Errorf("编辑前表格行数异常: got=%d want=5", len(beforeTable.Rows))
	}
	if len(afterTable.Rows) != 6 {
		return fmt.Errorf("新增后表格行数异常: got=%d want=6", len(afterTable.Rows))
	}
	if tableContainsText(result.BeforeDocument, "安全合规") {
		return fmt.Errorf("编辑前基线已包含安全合规，无法证明插行效果")
	}
	insertedRow, insertedIndex := findTableRowContainingTexts(afterTable, "安全合规", "5%", "月度", "安全规范执行、数据保密措施")
	if insertedRow == nil {
		return fmt.Errorf("保存后未找到新增的安全合规整行")
	}
	if insertedIndex == 0 {
		return fmt.Errorf("新增行错误插入到表头位置")
	}
	if insertedIndex-1 < 0 || !tableRowContainsTexts(afterTable.Rows[insertedIndex-1], "团队贡献", "10%", "季度", "知识分享、代码 Review") {
		return fmt.Errorf("新增行未插入到团队贡献之后")
	}
	if len(insertedRow.Cells) < 4 {
		return fmt.Errorf("新增行列数异常: got=%d want>=4", len(insertedRow.Cells))
	}
	for index := 0; index < 4; index++ {
		paragraph := firstParagraphFromTableCell(&insertedRow.Cells[index])
		if paragraph == nil {
			return fmt.Errorf("新增行第 %d 列缺少段落内容", index)
		}
		if normalizeStyleID(paragraph.Style.StyleID) != "tablebody" {
			return fmt.Errorf("新增行第 %d 列样式异常: style_id=%s", index, paragraph.Style.StyleID)
		}
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "团队贡献标签单元格", "团队贡献"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "团队贡献说明单元格", "知识分享、代码 Review"); err != nil {
		return err
	}
	if result != nil {
		result.InsertedParagraph = firstParagraphFromTableCell(&insertedRow.Cells[0])
	}
	return nil
}

func verifyLLMExpandedReplaceSameTableCellDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("单元格扩写场景缺少编辑前基线快照")
	}
	body := findParagraphByText(reopened, "当前为灰度验证阶段，上线前需同步确认回滚预案。")
	if body == nil {
		return fmt.Errorf("未找到扩写后的风险提示正文")
	}
	if normalizeStyleID(body.Style.StyleID) != "tablebody" {
		return fmt.Errorf("扩写后的单元格正文样式异常: style_id=%s", body.Style.StyleID)
	}
	header := findParagraphByText(reopened, "风险提示")
	if header == nil {
		return fmt.Errorf("风险提示标题段被误删")
	}
	if normalizeStyleID(header.Style.StyleID) != "tableheader" {
		return fmt.Errorf("风险提示标题样式异常: style_id=%s", header.Style.StyleID)
	}
	if !tableCellContainsTexts(reopened, "风险提示", "当前为灰度验证阶段，上线前需同步确认回滚预案。") {
		return fmt.Errorf("扩写后的正文未保留在风险提示单元格内")
	}
	riskCell := findTableCellContainingTexts(reopened, "风险提示", "当前为灰度验证阶段，上线前需同步确认回滚预案。")
	if riskCell == nil {
		return fmt.Errorf("未找到扩写后的风险提示单元格")
	}
	beforeRiskCell := findTableCellContainingTexts(result.BeforeDocument, "风险提示", "当前为灰度验证阶段。")
	if beforeRiskCell == nil {
		return fmt.Errorf("编辑前未找到风险提示单元格基线")
	}
	beforeRiskSnapshot := buildTableCellStructureSnapshot(beforeRiskCell)
	if len(beforeRiskSnapshot.Paragraphs) != 2 {
		return fmt.Errorf("编辑前风险提示单元格基线段落数量异常: got=%d want=2", len(beforeRiskSnapshot.Paragraphs))
	}
	riskParagraphs := tableCellParagraphsContainingTexts(reopened, "风险提示", "当前为灰度验证阶段，上线前需同步确认回滚预案。")
	if len(riskParagraphs) != 2 {
		return fmt.Errorf("风险提示单元格正文被错误拆成了多段")
	}
	if err := assertTableCellSnapshot(riskCell, expectedTableCellSnapshot(
		beforeRiskSnapshot.Paragraphs[0],
		paragraphSnapshotWithText(beforeRiskSnapshot.Paragraphs[1], "当前为灰度验证阶段，上线前需同步确认回滚预案。"),
	), "风险提示单元格"); err != nil {
		return err
	}
	if tableCellParagraphCount(reopened, "风险提示", "当前为灰度验证阶段，上线前需同步确认回滚预案。") != 2 {
		return fmt.Errorf("风险提示单元格正文被错误拆成了多段")
	}
	if !tableContainsText(reopened, "需人工复核") {
		return fmt.Errorf("右侧结果被误修改")
	}
	if !tableContainsText(reopened, "85%") {
		return fmt.Errorf("通过率单元格被误修改")
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "通过率标签单元格", "通过率"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "通过率数值单元格", "85%"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "右侧结果单元格", "需人工复核"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if result == nil || len(result.AppliedActions) != 1 {
		return fmt.Errorf("单元格扩写场景实际执行动作数量异常")
	}
	if !resultHasActionType(result, "update_table_cell") {
		return fmt.Errorf("单元格扩写场景缺少 update_table_cell 动作")
	}
	if resultHasActionType(result, "insert") || resultHasActionType(result, "insert_line_break") || resultHasActionType(result, "insert_paragraph_break") {
		return fmt.Errorf("单元格扩写场景不应包含新增段落或断开动作")
	}
	if result != nil {
		result.InsertedParagraph = body
	}
	return nil
}

func verifyLLMParagraphBreakDocument(reopened *goword.Document, result *executionResult) error {
	leading := findParagraphByText(reopened, "交付说明：")
	if leading == nil || strings.TrimSpace(leading.PlainText()) != "交付说明：" {
		return fmt.Errorf("未找到拆分后的前半段")
	}
	tail := paragraphImmediatelyAfterText(reopened, "交付说明：")
	if tail == nil || strings.TrimSpace(tail.PlainText()) != "交付物以验收版本为准。" {
		return fmt.Errorf("未找到拆分后的后半段")
	}
	if paragraphStyleID(leading) != paragraphStyleID(tail) {
		return fmt.Errorf("段落断开后样式未保持一致: before=%s after=%s", paragraphStyleID(leading), paragraphStyleID(tail))
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") != nil {
		return fmt.Errorf("原始整句仍存在，说明没有真正拆成两段")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("验证说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("段落断开场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "交付说明：交付物以验收版本为准。")
	if before == nil {
		return fmt.Errorf("编辑前未找到交付说明基线")
	}
	expectedLeading, expectedTail := splitParagraphSnapshotAt(buildParagraphStructureSnapshot(before), len([]rune("交付说明：")))
	if err := assertParagraphSnapshot(leading, expectedLeading, "交付说明前半段"); err != nil {
		return err
	}
	if err := assertParagraphSnapshot(tail, expectedTail, "交付说明后半段"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "验证说明段", "验证说明：当前版本仍需人工复核。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if result != nil {
		result.InsertedParagraph = tail
	}
	return nil
}

func verifyLLMLineBreakDocument(reopened *goword.Document, result *executionResult) error {
	paragraph := findParagraphContainingAll(reopened, "验证说明：", "当前版本仍需人工复核。")
	if paragraph == nil {
		return fmt.Errorf("未找到插入行内换行后的说明段")
	}
	text := strings.TrimSpace(paragraph.PlainText())
	if !strings.Contains(text, "\n") {
		return fmt.Errorf("目标段落未产生行内换行")
	}
	if text != "验证说明：\n当前版本仍需人工复核。" {
		return fmt.Errorf("行内换行后的正文不符合预期: %q", text)
	}
	for index := range reopened.Paragraphs {
		trimmed := strings.TrimSpace(reopened.Paragraphs[index].PlainText())
		if trimmed == "验证说明：" || trimmed == "当前版本仍需人工复核。" {
			return fmt.Errorf("目标说明被错误拆成两个独立段落")
		}
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("段内换行场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "验证说明：当前版本仍需人工复核。")
	if before == nil {
		return fmt.Errorf("编辑前未找到验证说明基线")
	}
	expected := paragraphSnapshotWithInsertedLineBreak(buildParagraphStructureSnapshot(before), len([]rune("验证说明：")))
	if err := assertParagraphSnapshot(paragraph, expected, "验证说明段内换行"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "交付说明段", "交付说明：交付物以验收版本为准。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if result != nil {
		result.InsertedParagraph = paragraph
	}
	return nil
}

func verifyLLMInlineEmphasisDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	paragraphText := "本报告用于验证 Agent 对真实 Word 文档的结构理解、格式保持与自动化回归能力。"
	paragraph := findParagraphByText(reopened, paragraphText)
	if paragraph == nil {
		return fmt.Errorf("未找到导语段落")
	}
	if strings.TrimSpace(paragraph.PlainText()) != paragraphText {
		return fmt.Errorf("run 级强调错误改动了导语正文")
	}
	styledRun := findTextRunContaining(paragraph, "自动化回归能力")
	if styledRun == nil {
		return fmt.Errorf("未找到被强调短语 run")
	}
	if !textStyleSatisfies(styledRun.Style, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true, Color: "C00000"}) {
		return fmt.Errorf("被强调短语样式不符合预期: %+v", styledRun.Style)
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("run 级强调场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, paragraphText)
	if before == nil {
		return fmt.Errorf("编辑前未找到导语段落基线")
	}
	expected, err := paragraphSnapshotWithStyledText(buildParagraphStructureSnapshot(before), "自动化回归能力", goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true, Color: "C00000"})
	if err != nil {
		return err
	}
	if err := assertParagraphSnapshot(paragraph, expected, "导语中的强调短语"); err != nil {
		return err
	}
	if findParagraphByText(reopened, "第一章 项目背景") == nil {
		return fmt.Errorf("标题段被误修改")
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "项目背景标题段", "第一章 项目背景"); err != nil {
		return err
	}
	if result == nil || len(result.AppliedActions) == 0 {
		return fmt.Errorf("未记录到实际执行动作")
	}
	if !resultHasActionType(result, "format") {
		return fmt.Errorf("run 级强调缺少 format 动作")
	}
	if resultHasActionType(result, "replace") || resultHasActionType(result, "insert") {
		return fmt.Errorf("run 级强调不应包含 replace 或 insert 动作")
	}
	if result != nil {
		result.InsertedParagraph = paragraph
	}
	return nil
}

func verifyLLMExpandedReplaceSameParagraphDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	paragraph := findParagraphByText(reopened, "验证说明：当前版本仍需人工复核，发布前需同步补齐回归记录。")
	if paragraph == nil {
		return fmt.Errorf("未找到同段扩写后的验证说明")
	}
	if strings.Contains(paragraph.PlainText(), "\n") {
		return fmt.Errorf("同段扩写错误产生了换行或断段")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") != nil {
		return fmt.Errorf("旧的验证说明仍然存在")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("同段扩写场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "验证说明：当前版本仍需人工复核。")
	if before == nil {
		return fmt.Errorf("编辑前未找到验证说明基线")
	}
	if err := assertParagraphSnapshot(paragraph, paragraphSnapshotWithText(buildParagraphStructureSnapshot(before), "验证说明：当前版本仍需人工复核，发布前需同步补齐回归记录。"), "同段扩写后的验证说明"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "交付说明段", "交付说明：交付物以验收版本为准。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if result == nil || len(result.AppliedActions) == 0 {
		return fmt.Errorf("未记录到实际执行动作")
	}
	if !resultHasActionType(result, "replace") {
		return fmt.Errorf("同段扩写缺少 replace 动作")
	}
	if resultHasActionType(result, "insert_paragraph_break") || resultHasActionType(result, "insert_line_break") {
		return fmt.Errorf("同段扩写不应包含断段或换行动作")
	}
	if result != nil {
		result.InsertedParagraph = paragraph
	}
	return nil
}

func verifyLLMExpandedReplaceRequiresSplitDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	leading := findParagraphByText(reopened, "交付说明：")
	if leading == nil || strings.TrimSpace(leading.PlainText()) != "交付说明：" {
		return fmt.Errorf("未找到拆分后的交付说明前半段")
	}
	tail := paragraphImmediatelyAfterText(reopened, "交付说明：")
	if tail == nil || strings.TrimSpace(tail.PlainText()) != "交付物以验收版本为准，归档时同步附上签收单。" {
		return fmt.Errorf("未找到拆分并扩写后的交付说明后半段")
	}
	if paragraphStyleID(leading) != paragraphStyleID(tail) {
		return fmt.Errorf("拆段后前后段样式不一致: before=%s after=%s", paragraphStyleID(leading), paragraphStyleID(tail))
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") != nil {
		return fmt.Errorf("旧的整句交付说明仍然存在")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("验证说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("扩写拆段场景缺少编辑前基线快照")
	}
	before := findParagraphByText(result.BeforeDocument, "交付说明：交付物以验收版本为准。")
	if before == nil {
		return fmt.Errorf("编辑前未找到交付说明基线")
	}
	expectedLeading, expectedTail := splitParagraphSnapshotAt(buildParagraphStructureSnapshot(before), len([]rune("交付说明：")))
	expectedTail = paragraphSnapshotWithText(expectedTail, "交付物以验收版本为准，归档时同步附上签收单。")
	if err := assertParagraphSnapshot(leading, expectedLeading, "拆段后的交付说明前半段"); err != nil {
		return err
	}
	if err := assertParagraphSnapshot(tail, expectedTail, "拆段并扩写后的交付说明后半段"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "验证说明段", "验证说明：当前版本仍需人工复核。"); err != nil {
		return err
	}
	if err := assertUntouchedParagraphHash(result.BeforeDocument, reopened, "附录说明段", "附录说明：本报告用于自动化回归。"); err != nil {
		return err
	}
	if result == nil || len(result.AppliedActions) < 2 {
		return fmt.Errorf("扩写拆段场景实际执行动作不足 2 条")
	}
	if !resultHasActionType(result, "insert_paragraph_break") || !resultHasActionType(result, "replace") {
		return fmt.Errorf("扩写拆段场景缺少预期动作类型")
	}
	if resultHasActionType(result, "insert_line_break") {
		return fmt.Errorf("扩写拆段场景错误使用了 insert_line_break")
	}
	if result != nil {
		result.InsertedParagraph = tail
	}
	return nil
}

func verifyLLMHeadingInsertDocument(reopened *goword.Document, result *executionResult) error {
	inserted := findParagraphByText(reopened, "三、质量门禁")
	if inserted == nil {
		return fmt.Errorf("保存后未找到新增标题")
	}
	if !strings.EqualFold(normalizeStyleID(inserted.Style.StyleID), "heading2") {
		return fmt.Errorf("新增标题样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if paragraphImmediatelyAfterText(reopened, "二、实施步骤") != inserted {
		return fmt.Errorf("新增标题未插入到实施步骤标题后方")
	}
	if findParagraphByText(reopened, "三、交付说明") == nil {
		return fmt.Errorf("原有交付说明标题被误修改")
	}
	if result != nil {
		result.InsertedParagraph = inserted
	}
	return nil
}

func verifyLLMHeaderRewriteDocument(reopened *goword.Document, _ *executionResult) error {
	header := findHeaderFooterPart(reopened, "header", "default")
	if header == nil {
		return fmt.Errorf("保存后未找到默认页眉")
	}
	if !headerFooterPartContainsText(header, "Auramate 项目内参") {
		return fmt.Errorf("默认页眉未改写为目标文本")
	}
	if headerFooterPartContainsText(header, "Auramate 内部资料") {
		return fmt.Errorf("默认页眉仍残留旧文本")
	}
	if findParagraphByText(reopened, "Auramate 智能编辑综合验证报告") == nil {
		return fmt.Errorf("正文标题被误修改")
	}
	footer := findHeaderFooterPart(reopened, "footer", "default")
	if footer == nil || !headerFooterPartContainsText(footer, "第 1 页") {
		return fmt.Errorf("页脚被误修改或丢失")
	}
	return nil
}

func verifyLLMFirstPageHeaderVariantRewriteDocument(reopened *goword.Document, _ *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if !reopened.Meta.SectionLayout.TitlePage || !reopened.Meta.SectionLayout.TitlePageSet {
		return fmt.Errorf("首页不同设置未保留")
	}
	firstHeader := findHeaderFooterPart(reopened, "header", "first")
	if firstHeader == nil {
		return fmt.Errorf("保存后未找到首页页眉")
	}
	if !headerFooterPartContainsText(firstHeader, "Auramate 首页摘要") {
		return fmt.Errorf("首页页眉未改写为目标文本")
	}
	if headerFooterPartContainsText(firstHeader, "Auramate 首页资料") {
		return fmt.Errorf("首页页眉仍残留旧文本")
	}
	defaultHeader := findHeaderFooterPart(reopened, "header", "default")
	if defaultHeader == nil {
		return fmt.Errorf("保存后未找到默认页眉")
	}
	if !headerFooterPartContainsText(defaultHeader, "Auramate 内部资料") {
		return fmt.Errorf("默认页眉被误修改")
	}
	if headerFooterPartContainsText(defaultHeader, "Auramate 首页摘要") {
		return fmt.Errorf("首页页眉文本误写入默认页眉")
	}
	if findParagraphByText(reopened, "Auramate 智能编辑综合验证报告") == nil {
		return fmt.Errorf("正文标题被误修改")
	}
	return nil
}

func verifyLLMHeaderFooterVariantMixedDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if !reopened.Meta.SectionLayout.TitlePage || !reopened.Meta.SectionLayout.TitlePageSet {
		return fmt.Errorf("首页不同设置未保留")
	}
	defaultHeader := findHeaderFooterPart(reopened, "header", "default")
	if defaultHeader == nil || !headerFooterPartContainsText(defaultHeader, "Auramate 默认周报") {
		return fmt.Errorf("默认页眉未更新为目标文本")
	}
	if headerFooterPartContainsText(defaultHeader, "Auramate 内部资料") {
		return fmt.Errorf("默认页眉仍残留旧文本")
	}
	firstHeader := findHeaderFooterPart(reopened, "header", "first")
	if firstHeader == nil || !headerFooterPartContainsText(firstHeader, "Auramate 首页摘要") {
		return fmt.Errorf("首页页眉未更新为目标文本")
	}
	if headerFooterPartContainsText(firstHeader, "Auramate 首页资料") {
		return fmt.Errorf("首页页眉仍残留旧文本")
	}
	evenFooter := findHeaderFooterPart(reopened, "footer", "even")
	if evenFooter == nil || !headerFooterPartContainsText(evenFooter, "偶数页审阅版") {
		return fmt.Errorf("偶数页页脚未更新为目标文本")
	}
	if headerFooterPartContainsText(evenFooter, "偶数页页脚") {
		return fmt.Errorf("偶数页页脚仍残留旧文本")
	}
	defaultFooter := findHeaderFooterPart(reopened, "footer", "default")
	if defaultFooter == nil || !headerFooterPartContainsText(defaultFooter, "第 1 页") {
		return fmt.Errorf("默认页脚被误修改")
	}
	if findParagraphByText(reopened, "Auramate 智能编辑综合验证报告") == nil {
		return fmt.Errorf("正文标题被误修改")
	}
	if result == nil || len(result.AppliedActions) != 3 {
		return fmt.Errorf("variant 混合场景动作数异常")
	}
	for _, action := range result.AppliedActions {
		if action == nil || !strings.EqualFold(strings.TrimSpace(action.Action), "update_header_footer") {
			return fmt.Errorf("variant 混合场景出现了非 update_header_footer 动作")
		}
	}
	return nil
}

func verifyLLMSectionLayoutOrientationMarginDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	layout := reopened.Meta.SectionLayout
	if !strings.EqualFold(layout.Orientation, "landscape") {
		return fmt.Errorf("页面方向未更新为 landscape: got=%s", layout.Orientation)
	}
	if layout.MarginLeft != 1200 || layout.MarginRight != 1200 {
		return fmt.Errorf("左右页边距未更新为 1200: left=%d right=%d", layout.MarginLeft, layout.MarginRight)
	}
	if !layout.TitlePage || !layout.TitlePageSet {
		return fmt.Errorf("首页不同设置被误丢失")
	}
	if findHeaderFooterPart(reopened, "header", "default") == nil || !headerFooterPartContainsText(findHeaderFooterPart(reopened, "header", "default"), "Auramate 内部资料") {
		return fmt.Errorf("默认页眉被误修改")
	}
	if findHeaderFooterPart(reopened, "footer", "default") == nil || !headerFooterPartContainsText(findHeaderFooterPart(reopened, "footer", "default"), "第 1 页") {
		return fmt.Errorf("默认页脚被误修改")
	}
	if findParagraphByText(reopened, "Auramate 智能编辑综合验证报告") == nil {
		return fmt.Errorf("正文标题被误修改")
	}
	if result == nil || len(result.AppliedActions) != 1 {
		return fmt.Errorf("节布局场景动作数异常")
	}
	if !resultHasActionType(result, "format_section") {
		return fmt.Errorf("节布局场景缺少 format_section 动作")
	}
	if resultHasActionType(result, "replace") || resultHasActionType(result, "update_header_footer") {
		return fmt.Errorf("节布局场景误用了文本或页眉页脚动作")
	}
	if !resultHasReaderLayoutTrace(result, 0) {
		return fmt.Errorf("节布局场景缺少 include_layout 的 reader 取证")
	}
	return nil
}

func verifyLLMPageNumberStartFormatDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	layout := reopened.Meta.SectionLayout
	if layout.PageNumStart != 5 {
		return fmt.Errorf("页码起始值未更新为 5: got=%d", layout.PageNumStart)
	}
	if !strings.EqualFold(layout.PageNumFmt, "upperRoman") {
		return fmt.Errorf("页码格式未更新为 upperRoman: got=%s", layout.PageNumFmt)
	}
	if !layout.TitlePage || !layout.TitlePageSet {
		return fmt.Errorf("首页不同设置被误丢失")
	}
	defaultHeader := findHeaderFooterPart(reopened, "header", "default")
	if defaultHeader == nil || !headerFooterPartContainsText(defaultHeader, "Auramate 内部资料") {
		return fmt.Errorf("默认页眉被误修改")
	}
	firstHeader := findHeaderFooterPart(reopened, "header", "first")
	if firstHeader == nil || !headerFooterPartContainsText(firstHeader, "Auramate 首页资料") {
		return fmt.Errorf("首页页眉被误修改")
	}
	footer := findHeaderFooterPart(reopened, "footer", "default")
	if footer == nil || !headerFooterPartContainsText(footer, "第 1 页") {
		return fmt.Errorf("页脚文本被误修改")
	}
	if findParagraphByText(reopened, "Auramate 智能编辑综合验证报告") == nil {
		return fmt.Errorf("正文标题被误修改")
	}
	if result == nil {
		return fmt.Errorf("execution result is nil")
	}
	if len(result.AppliedActions) != 1 {
		return fmt.Errorf("页码设置场景实际执行动作数异常: got=%d", len(result.AppliedActions))
	}
	if !resultHasActionType(result, "format_section") {
		return fmt.Errorf("页码设置场景缺少 format_section 动作")
	}
	if resultHasActionType(result, "update_header_footer") || resultHasActionType(result, "replace") {
		return fmt.Errorf("页码设置场景误用了文本或页眉页脚动作")
	}
	if !resultHasReaderLayoutTrace(result, 0) {
		return fmt.Errorf("页码设置场景缺少 include_layout 的 reader 取证")
	}
	return nil
}

func verifyLLMCrossRegionBatchDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	header := findHeaderFooterPart(reopened, "header", "default")
	if header == nil || !headerFooterPartContainsText(header, "Auramate 项目周报") {
		return fmt.Errorf("页眉未改成 Auramate 项目周报")
	}
	if headerFooterPartContainsText(header, "Auramate 内部资料") {
		return fmt.Errorf("页眉仍残留旧文本")
	}
	if findParagraphByText(reopened, "审阅说明：当前版本仍需人工复核。") == nil && findParagraphByText(reopened, "验证说明（审阅项）：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("中间说明未改成审阅说明")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") != nil {
		return fmt.Errorf("旧的验证说明仍然存在")
	}
	if !tableContainsExactText(reopened, "92%") || tableContainsExactText(reopened, "85%") {
		return fmt.Errorf("通过率未正确更新为 92%%")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || len(result.AppliedActions) < 3 {
		return fmt.Errorf("跨区域批处理实际执行动作不足 3 条")
	}
	if !resultHasActionType(result, "update_header_footer") || !resultHasActionType(result, "update_table_cell") || !resultHasActionType(result, "replace") {
		return fmt.Errorf("跨区域批处理缺少预期动作类型")
	}
	return nil
}

func verifyLLMRepeatHeaderTableUpdateDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	updatedCell := findParagraphByText(reopened, "需附版本签字记录。")
	if updatedCell == nil {
		return fmt.Errorf("未找到更新后的发布批次要求")
	}
	if normalizeStyleID(updatedCell.Style.StyleID) != "tablebody" {
		return fmt.Errorf("发布批次要求样式异常: style_id=%s", updatedCell.Style.StyleID)
	}
	if tableContainsExactText(reopened, "需关注灰度开关与监控告警。") {
		return fmt.Errorf("旧的灰度批次要求仍然存在")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("重复表头场景缺少编辑前基线快照")
	}
	beforeTable := findTableContainingTexts(result.BeforeDocument, "发布批次", "灰度批次", "正式批次")
	afterTable := findTableContainingTexts(reopened, "发布批次", "灰度批次", "正式批次")
	if beforeTable == nil || afterTable == nil {
		return fmt.Errorf("缺少发布批次表用于结构比对")
	}
	if len(beforeTable.Rows) == 0 || len(afterTable.Rows) == 0 {
		return fmt.Errorf("发布批次表行结构异常")
	}
	if !afterTable.Rows[0].RepeatHeaderSet || !afterTable.Rows[0].RepeatHeader {
		return fmt.Errorf("发布批次表首行 repeat_header 丢失")
	}
	if !afterTable.Rows[0].CantSplitSet || !afterTable.Rows[0].CantSplit {
		return fmt.Errorf("发布批次表首行 cant_split 丢失")
	}
	if beforeTable.Rows[0].RepeatHeader != afterTable.Rows[0].RepeatHeader || beforeTable.Rows[0].RepeatHeaderSet != afterTable.Rows[0].RepeatHeaderSet || beforeTable.Rows[0].CantSplit != afterTable.Rows[0].CantSplit || beforeTable.Rows[0].CantSplitSet != afterTable.Rows[0].CantSplitSet {
		return fmt.Errorf("发布批次表首行属性漂移")
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "发布批次表头左侧", "发布批次"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "发布批次表头右侧", "要求"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "正式批次标签单元格", "正式批次"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "正式批次要求单元格", "需附签发邮件与发布时间窗。"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "回滚批次标签单元格", "回滚批次"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "回滚批次要求单元格", "需保留回滚脚本与审批记录。"); err != nil {
		return err
	}
	beforeBodyCell := findTableCellContainingTexts(result.BeforeDocument, "灰度批次", "需关注灰度开关与监控告警。")
	afterBodyCell := findTableCellContainingTexts(reopened, "灰度批次", "需附版本签字记录。")
	if beforeBodyCell == nil || afterBodyCell == nil {
		return fmt.Errorf("发布批次目标单元格缺失")
	}
	beforeBodyParagraph := firstParagraphFromTableCell(beforeBodyCell)
	if beforeBodyParagraph == nil {
		return fmt.Errorf("编辑前未找到目标单元格正文基线")
	}
	if err := assertParagraphSnapshot(updatedCell, paragraphSnapshotWithText(buildParagraphStructureSnapshot(beforeBodyParagraph), "需附版本签字记录。"), "发布批次目标单元格正文"); err != nil {
		return err
	}
	if len(result.AppliedActions) != 1 || !resultHasActionType(result, "update_table_cell") {
		return fmt.Errorf("重复表头场景动作类型异常")
	}
	return nil
}

func verifyLLMCrossPageRepeatHeaderVisualUpdateDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	updatedCell := findParagraphByText(reopened, "需同步灾备切换窗口与回退负责人。")
	if updatedCell == nil {
		return fmt.Errorf("未找到更新后的第二页发布批次要求")
	}
	if normalizeStyleID(updatedCell.Style.StyleID) != "tablebody" {
		return fmt.Errorf("第二页发布批次要求样式异常: style_id=%s", updatedCell.Style.StyleID)
	}
	if tableContainsExactText(reopened, "需演练双活切换与值班交接。") {
		return fmt.Errorf("旧的灾备演练批次要求仍然存在")
	}
	if result == nil || result.BeforeDocument == nil {
		return fmt.Errorf("跨页重复表头场景缺少编辑前基线快照")
	}
	beforeTable := findTableContainingTexts(result.BeforeDocument, "发布批次", "灰度批次", "灾备演练批次")
	afterTable := findTableContainingTexts(reopened, "发布批次", "灰度批次", "灾备演练批次")
	if beforeTable == nil || afterTable == nil {
		return fmt.Errorf("缺少发布批次表用于跨页结构比对")
	}
	if len(afterTable.Rows) == 0 || !afterTable.Rows[0].RepeatHeaderSet || !afterTable.Rows[0].RepeatHeader {
		return fmt.Errorf("发布批次表首行 repeat_header 丢失")
	}
	if len(afterTable.Rows) == 0 || !afterTable.Rows[0].CantSplitSet || !afterTable.Rows[0].CantSplit {
		return fmt.Errorf("发布批次表首行 cant_split 丢失")
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "发布批次表头左侧", "发布批次"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "发布批次表头右侧", "要求"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "灰度批次要求单元格", "需关注灰度开关与监控告警。"); err != nil {
		return err
	}
	if err := assertUntouchedCellHash(result.BeforeDocument, reopened, "正式批次要求单元格", "需附签发邮件与发布时间窗。"); err != nil {
		return err
	}
	if result.ReopenedLayout == nil {
		return fmt.Errorf("跨页重复表头场景缺少保存后 layout 证据")
	}
	firstPageTable := findLayoutTableOnPage(result.ReopenedLayout, 0, "tbl_release_batches")
	secondPageTable := findLayoutTableOnPage(result.ReopenedLayout, 1, "tbl_release_batches")
	if firstPageTable == nil || secondPageTable == nil {
		return fmt.Errorf("发布批次表未形成真正的跨页布局")
	}
	if len(secondPageTable.Rows) == 0 || !secondPageTable.Rows[0].RepeatHeader {
		return fmt.Errorf("第二页未看到重复表头行")
	}
	if !layoutTableHasRowTexts(secondPageTable, "发布批次", "要求") {
		return fmt.Errorf("第二页表格块未显示重复表头文本")
	}
	if !layoutTableHasRowTexts(firstPageTable, "灰度批次", "需关注灰度开关与监控告警。") {
		return fmt.Errorf("第一页表格块缺少首批 body 行，无法证明表格从第一页开始")
	}
	if !layoutTableHasRowTexts(secondPageTable, "灾备演练批次", "需同步灾备切换窗口与回退负责人。") {
		return fmt.Errorf("第二页表格块未显示更新后的灾备演练批次要求")
	}
	if !resultHasReaderLayoutTrace(result, 1) {
		return fmt.Errorf("模型未留下 focus_page_index=1 的 reader layout 证据")
	}
	if len(result.AppliedActions) != 1 || !resultHasActionType(result, "update_table_cell") {
		return fmt.Errorf("跨页重复表头视觉场景动作类型异常")
	}
	return nil
}

func verifyLLMDrawingObjectsBodyEditDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil || result == nil || result.SavedResult == nil {
		return fmt.Errorf("drawing objects 验证缺少保存结果")
	}
	updated := findParagraphByText(reopened, "对象区提示：当前版本已进入联合评审。")
	if updated == nil {
		return fmt.Errorf("保存后未找到已更新的对象区提示段落")
	}
	if findParagraphByText(reopened, "对象区提示：当前版本仅供内部评审。") != nil {
		return fmt.Errorf("旧的对象区提示段落仍然存在")
	}
	if findParagraphByText(reopened, "对象区说明：后续文本框和形状对象必须原样保留。") == nil {
		return fmt.Errorf("对象区说明段落丢失")
	}
	summary := reopened.Meta.ObjectSummary
	if summary == nil {
		return fmt.Errorf("重开文档缺少对象摘要")
	}
	if summary.InlineImages != 1 || summary.AnchoredImages != 2 || summary.LegacyImages != 1 || summary.LinkedImages != 1 || summary.TextBoxes != 1 || summary.Shapes != 1 {
		return fmt.Errorf("对象摘要异常: %+v", *summary)
	}
	for _, snippet := range []string{"<wp:anchor>", "<w:pict>", "<wps:wsp>", "<w:txbxContent>", "文本框内容"} {
		contains, err := docxEntryContains(result.SavedResult.Path, "word/document.xml", snippet)
		if err != nil {
			return err
		}
		if !contains {
			return fmt.Errorf("保存后的 document.xml 缺少对象片段: %s", snippet)
		}
	}
	return nil
}

func verifyLLMRelativeMultiRegionBatchDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if findParagraphByText(reopened, "归档备注：本报告用于自动化回归。") == nil {
		return fmt.Errorf("最后那条备注未正确改成归档备注")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") != nil {
		return fmt.Errorf("附录说明旧文本仍然存在")
	}
	vendorParagraph := findParagraphByText(reopened, "第三方费用由甲方另行承担，并在采购前单独确认。")
	if vendorParagraph == nil {
		return fmt.Errorf("第三方费用条款被误删")
	}
	nextParagraph := paragraphImmediatelyAfterText(reopened, "第三方费用由甲方另行承担，并在采购前单独确认。")
	vendorContainsApproval := strings.Contains(vendorParagraph.PlainText(), "采购审批") || strings.Contains(vendorParagraph.PlainText(), "审批备案")
	nextContainsApproval := nextParagraph != nil && (strings.Contains(nextParagraph.PlainText(), "采购审批") || strings.Contains(nextParagraph.PlainText(), "审批备案"))
	if !vendorContainsApproval && !nextContainsApproval {
		return fmt.Errorf("第三方费用条款后未找到采购审批说明")
	}
	if paragraphImmediatelyAfterText(reopened, "差旅费用由乙方内部报销，不计入本次报价。") == nextParagraph && nextContainsApproval {
		return fmt.Errorf("采购审批说明被错误插入到差旅费用后")
	}
	if !tableContainsExactText(reopened, "无需人工复核") || tableContainsExactText(reopened, "需人工复核") {
		return fmt.Errorf("风险提示右侧结果未正确更新")
	}
	if !tableContainsExactText(reopened, "85%") {
		return fmt.Errorf("通过率被误修改")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("验证说明被误修改")
	}
	if result == nil || len(result.AppliedActions) < 3 {
		return fmt.Errorf("多区域相对定位批处理实际执行动作不足 3 条")
	}
	if !resultHasActionType(result, "replace") || !resultHasActionType(result, "insert") || !resultHasActionType(result, "update_table_cell") {
		return fmt.Errorf("多区域相对定位批处理缺少预期动作类型")
	}
	return nil
}

func verifyLLMAmbiguousInstructionClarifyThenEditDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if findParagraphByText(reopened, "审阅说明：当前版本仍需人工复核。") == nil {
		return fmt.Errorf("澄清后的目标说明未正确改写")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") != nil {
		return fmt.Errorf("旧的验证说明仍然存在")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return fmt.Errorf("附录说明被误修改")
	}
	if result == nil || len(result.AppliedActions) != 1 {
		return fmt.Errorf("歧义澄清场景动作数异常")
	}
	if !resultHasActionType(result, "replace") {
		return fmt.Errorf("歧义澄清场景缺少 replace 动作")
	}
	return nil
}

func verifyLLMMultiSectionBodyEditPreservesSectionsDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if findParagraphByText(reopened, "第二节正文已复核") == nil {
		return fmt.Errorf("第二节正文未更新")
	}
	if findParagraphByText(reopened, "第一节末段") == nil {
		return fmt.Errorf("第一节正文被误修改")
	}
	if len(reopened.Meta.Sections) < 2 {
		return fmt.Errorf("多节元数据未保留")
	}
	if !strings.EqualFold(reopened.Meta.Sections[0].Layout.Orientation, "landscape") {
		return fmt.Errorf("第一节方向未保留 landscape")
	}
	findSectionPart := func(section goword.DocumentSection, kind, variant string) *goword.HeaderFooterPart {
		for index := range section.HeaderFooters {
			part := &section.HeaderFooters[index]
			if strings.EqualFold(strings.TrimSpace(part.Kind), kind) && strings.EqualFold(strings.TrimSpace(part.Variant), variant) {
				return part
			}
		}
		return nil
	}
	firstSectionHeader := findSectionPart(reopened.Meta.Sections[0], "header", "default")
	if firstSectionHeader == nil || !headerFooterPartContainsText(firstSectionHeader, "第一节页眉") {
		return fmt.Errorf("第一节页眉未保留")
	}
	secondSectionHeader := findSectionPart(reopened.Meta.Sections[1], "header", "default")
	if secondSectionHeader == nil || !headerFooterPartContainsText(secondSectionHeader, "第二节页眉") {
		return fmt.Errorf("第二节页眉未保留")
	}
	if len(reopened.Paragraphs) == 0 || reopened.Paragraphs[0].SectionBreakLayout == nil || !strings.EqualFold(reopened.Paragraphs[0].SectionBreakLayout.BreakType, "nextpage") {
		return fmt.Errorf("第一节段尾的 section break 未保留")
	}
	if result == nil || len(result.AppliedActions) != 1 || !resultHasActionType(result, "replace") {
		return fmt.Errorf("多节保真场景动作类型异常")
	}
	return nil
}

func verifyLLMLargeDocumentLongChainDocument(reopened *goword.Document, result *executionResult) error {
	if reopened == nil {
		return fmt.Errorf("reopened document is nil")
	}
	if findParagraphByText(reopened, "第 042 节验证结论：当前批次已完成自动复核。") == nil {
		return fmt.Errorf("第 042 节验证结论未更新")
	}
	if findParagraphByText(reopened, "第 057 节验证结论：当前批次需要补齐灰度记录。") == nil {
		return fmt.Errorf("第 057 节验证结论未更新")
	}
	if findParagraphByText(reopened, "第 043 节验证结论：当前批次需要人工复核。") == nil {
		return fmt.Errorf("相邻节正文被误修改")
	}
	if result == nil || len(result.AppliedActions) != 2 {
		return fmt.Errorf("长文档场景动作数异常")
	}
	if !resultHasActionType(result, "replace") {
		return fmt.Errorf("长文档场景缺少 replace 动作")
	}
	if result.ReopenedLayout == nil {
		return fmt.Errorf("长文档场景缺少保存后 layout 证据")
	}
	if len(result.ReopenedLayout.Pages) < 50 {
		return fmt.Errorf("长文档布局页数不足 50 页: got=%d", len(result.ReopenedLayout.Pages))
	}
	return nil
}

func resultHasActionType(result *executionResult, actionType string) bool {
	if result == nil {
		return false
	}
	trimmedType := strings.TrimSpace(strings.ToLower(actionType))
	for _, action := range result.AppliedActions {
		if action == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(action.Action)) == trimmedType {
			return true
		}
	}
	return false
}

func clipDetailText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "..."
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func collectReportSummary(result *report) {
	if result == nil {
		return
	}

	uniqueActions := make(map[string]struct{})
	for _, item := range result.Scenarios {
		if item.Passed {
			result.Passed++
		} else {
			result.Failed++
		}
		if item.AutoTuned {
			result.AutoTuningApplied++
		}
		for _, action := range item.Suggestions {
			if strings.TrimSpace(action) == "" {
				continue
			}
			uniqueActions[action] = struct{}{}
		}
		for _, action := range item.TuningAdjustments {
			if strings.TrimSpace(action) == "" {
				continue
			}
			uniqueActions[action] = struct{}{}
		}
	}

	for action := range uniqueActions {
		result.TuningActions = append(result.TuningActions, action)
	}
	sort.Strings(result.TuningActions)

	if result.Failed == 0 {
		if result.AutoTuningApplied > 0 {
			result.Summary = fmt.Sprintf("全部 %d 个场景已通过，其中 %d 个场景在自动调优重试后通过。", len(result.Scenarios), result.AutoTuningApplied)
			return
		}
		result.Summary = fmt.Sprintf("全部 %d 个场景已一次性通过，当前无需额外调优。", len(result.Scenarios))
		return
	}

	result.Summary = fmt.Sprintf("共 %d 个场景，%d 个通过，%d 个失败。已生成 %d 条自动调优建议。", len(result.Scenarios), result.Passed, result.Failed, len(result.TuningActions))
}

func runReaderScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	response, err := ctx.runner.runReader(ctx.inputDocPath, "", "现状分析", "all")
	if err != nil {
		return failScenario("reader_real_doc_context", started, err, []string{
			"补强 auramate-word-reader 的真实文档读取稳定性，优先检查最小 go-word API 服务和 session/open 链路。",
			"若 style_catalog 为空，优先排查 DOCX 样式导出与原生 auramate-word-reader 对 meta.styles 的映射链路。",
		}, nil)
	}

	details := map[string]any{
		"paragraph_count": response.Document.ParagraphCount,
		"table_count":     response.Document.TableCount,
		"heading_count":   response.Document.HeadingCount,
		"style_count":     len(response.StyleCatalog.Styles),
		"hit_count":       response.Query.HitCount,
	}

	if response.Status != "ok" {
		return failScenario("reader_real_doc_context", started, fmt.Errorf("reader status = %s", response.Status), []string{
			"确保 reader 在 path 模式下能正确打开真实 DOCX 并返回 status=ok。",
		}, details)
	}
	if response.Document.HeadingCount < 2 || response.Document.TableCount < 1 {
		return failScenario("reader_real_doc_context", started, fmt.Errorf("reader 未识别出足够的标题或表格结构"), []string{
			"补强 reader 的 heading/table 识别规则，避免只返回全文而缺少结构层信息。",
		}, details)
	}
	if len(response.StyleCatalog.Styles) < 3 {
		return failScenario("reader_real_doc_context", started, fmt.Errorf("style_catalog 数量不足: %d", len(response.StyleCatalog.Styles)), []string{
			"确保 style_catalog 至少包含 Title、Heading1/Heading2、Normal 等样式，便于后续自动生成 format_basis。",
		}, details)
	}
	if response.Query.HitCount == 0 || len(response.Query.Hits) == 0 {
		return failScenario("reader_real_doc_context", started, fmt.Errorf("query_hits 为空"), []string{
			"优先检查 /v1/goword/search 的 contains 检索链路和 reader 的 query 参数透传。",
		}, details)
	}
	if strings.TrimSpace(response.Query.Hits[0].ParagraphStyle.StyleID) == "" {
		return failScenario("reader_real_doc_context", started, fmt.Errorf("query_hit 未附带 paragraph_style"), []string{
			"保持 query_hits 附带 paragraph_style 与 dominant_text_style，避免 agent 无法构造可靠的 format_basis。",
		}, details)
	}

	return passScenario("reader_real_doc_context", started, "reader 已返回真实文档结构、样式目录和命中样式信息。", details, "")
}

func runCopilotGuardScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	_, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:             "replace",
		TargetText:         "当前系统已经支持全文检索、结构识别与样式继承。",
		NewText:            "当前系统已经支持全文检索、结构识别、样式继承与自动验证。",
		ContextScope:       "paragraph",
		EnableTrackChanges: true,
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("缺少 reader 依据时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对 replace/insert/delete 的 reader_context_summary 与 format_basis 强约束，禁止回退成软约束。",
		}, nil)
	}

	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("copilot_reader_guard", started, err, []string{"优先修复 openDocument 失败，确保真实文档场景可进入后续 bridge 验证。"}, nil)
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "现状分析", "paragraph")
	if err != nil {
		return failScenario("copilot_reader_guard", started, err, []string{"先修复 reader 读取失败，否则无法验证 copilot 的强约束。"}, nil)
	}
	contextSummary, formatBasis := buildHeadingEvidence(reader, 2)
	_, rawSignal, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "一、现状分析",
		NewText:              "二、验证策略",
		ParagraphStylePatch:  goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100},
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return failScenario("copilot_reader_guard", started, err, []string{"bridge 在提供 reader 依据后仍失败，应先排查参数解析与证据校验逻辑。"}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "一、现状分析",
		NewText:              "二、验证策略",
		ParagraphStylePatch:  goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100},
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          "保持原格式，保持一致。",
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("format_basis 过于空泛时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对 format_basis 具体性的强约束，至少要求 style_id、字体字号、对齐缩进或 layout 依据。",
		}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:             "insert_line_break",
		TargetText:         "一、现状分析",
		Position:           "after",
		ContextScope:       "paragraph",
		EnableTrackChanges: true,
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("段内断开动作缺少 reader 依据时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对 insert_paragraph_break/insert_line_break 的 reader_context_summary 与 format_basis 强约束，禁止让段内断开动作绕过 reader。",
		}, nil)
	}
	breakGuardErr := err
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "insert_paragraph_break",
		TargetText:           "一、现状分析",
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return failScenario("copilot_reader_guard", started, err, []string{"bridge 在提供 reader 依据后仍拒绝段内断开动作，应先排查 break 动作的证据校验逻辑。"}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "一、现状分析",
		NewText:              "",
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		EditIntent:           "在这句后面按回车另起一段",
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("用 insert 冒充段落断开时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 的动作语义约束：整段新增用 insert，Enter/Shift+Enter 必须分别使用 insert_paragraph_break 或 insert_line_break。",
		}, nil)
	}
	insertBreakMismatchErr := err

	targetHit := findReaderQueryHitContaining(reader, "现状分析")
	if targetHit == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("reader 未返回现状分析命中"), []string{"先修复 reader query_hits，否则无法验证相对定位时的精确锚点约束。"}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           "一、现状分析",
		NewText:              "一、现状核查",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		EditIntent:           "把中间那条说明改一下",
		Constraints:          "只改中间那条，不要碰其他标题。",
		PreferredParagraphID: targetHit.ParagraphID,
		ReaderContextSummary: "用户使用了相对定位描述“中间那条”，当前命中在正文结构中需要依赖精确段落 ID 才能安全编辑。",
		FormatBasis:          formatBasis,
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("相对定位缺少 exploration_summary 时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对模糊定位二次 exploration 的强约束，禁止只补 paragraph_id 而不补落点确认摘要。",
		}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           "一、现状分析",
		NewText:              "一、现状核查",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		EditIntent:           "把中间那条说明改一下",
		Constraints:          "只改中间那条，不要碰其他标题。",
		ReaderContextSummary: "用户使用了相对定位描述“中间那条”，当前命中在正文结构中需要依赖精确段落 ID 才能安全编辑。",
		FormatBasis:          formatBasis,
		ExplorationSummary:   "已围绕正文命中展开前后邻段，确认需要修改的是正文中部的‘现状分析’标题，而不是其它说明段。",
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("相对定位缺少 preferred_paragraph_id 时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对‘中间/最后/那条/那段/那格’这类相对定位描述的精确锚点强约束，禁止只靠 target_text 模糊编辑。",
		}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           "一、现状分析",
		NewText:              "一、现状核查",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		EditIntent:           "把中间那条说明改一下",
		Constraints:          "只改中间那条，不要碰其他标题。",
		PreferredParagraphID: targetHit.ParagraphID,
		ReaderContextSummary: "用户使用了相对定位描述“中间那条”，当前命中在正文结构中需要依赖精确段落 ID 才能安全编辑。",
		FormatBasis:          formatBasis,
		ExplorationSummary:   "已围绕正文命中展开前后邻段，确认需要修改的是正文中部的‘现状分析’标题，而不是其它说明段。",
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("精确锚点缺少 reader_session_id 时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对精确锚点回写同一 reader_session_id 的强约束，避免跨会话复用旧锚点误写。",
		}, nil)
	}
	tableReader, err := ctx.runner.runReader("", session.ID, "通过率", "table")
	if err != nil {
		return failScenario("copilot_reader_guard", started, err, []string{"先修复 reader 表格最小读取路径，否则无法验证表格坐标强约束。"}, nil)
	}
	tableHit := findReaderQueryHitContaining(tableReader, "通过率")
	if tableHit == nil || strings.TrimSpace(tableHit.TableID) == "" {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("reader 未返回带 table_id 的通过率命中"), []string{"先修复 reader query_hits 对表格值命中的 table_id/row_index/cell_index 回传。"}, nil)
	}
	_, _, err = ctx.runner.runBridge(bridgeRequest{
		Action:               "update_table_cell",
		TargetText:           "通过率",
		TableID:              tableHit.TableID,
		ReaderSessionID:      session.ID,
		ContextScope:         "table",
		EnableTrackChanges:   true,
		ReaderContextSummary: "目标位于表格中的通过率单元格。",
		FormatBasis:          formatStyleBasis(tableHit.ParagraphStyle, tableHit.DominantTextStyle, "表格正文样式"),
		ExplorationSummary:   fmt.Sprintf("已确认目标位于表格 %s 的通过率单元格，需更新该格内容。", tableHit.TableID),
		TableCell:            &goword.TableCellInput{Paragraphs: []goword.ParagraphInput{{Text: "92%"}}},
	})
	if err == nil {
		return failScenario("copilot_reader_guard", started, fmt.Errorf("update_table_cell 缺少 row_index/cell_index 时 bridge 未拒绝执行"), []string{
			"保持 auramate-word-copilot 对表格单元格写入显式 row_index/cell_index 的强约束，禁止默认落回第 0 行或第 0 列。",
		}, nil)
	}

	return passScenario("copilot_reader_guard", started, "copilot 已正确拦截缺少 reader 依据、动作语义错误与相对定位缺证据的调用，并在补齐证据后允许通过。", map[string]any{
		"break_guard_error":     errString(breakGuardErr),
		"insert_mismatch_error": errString(insertBreakMismatchErr),
		"relative_anchor_error": errString(err),
		"signal_size":           len(rawSignal),
	}, "")

}

func runReplaceBodyScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	targetText := "当前系统已经支持全文检索、结构识别与样式继承。"
	newText := "当前系统已经支持全文检索、结构识别、样式继承与自动验证。"
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("replace_body_real_doc", started, err, replaceSuggestions(), nil)
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, targetText, "paragraph")
	if err != nil {
		return failScenario("replace_body_real_doc", started, err, replaceSuggestions(), nil)
	}
	contextSummary, formatBasis := buildParagraphEvidence(reader, targetText)
	contextSummary, formatBasis = appendExpandedRewriteSameParagraphEvidence(contextSummary, formatBasis, "正文")
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           targetText,
		NewText:              newText,
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return failScenario("replace_body_real_doc", started, err, replaceSuggestions(), nil)
	}

	result, execErr := executeEditorAction(ctx.client, session.ID, action)
	if execErr != nil {
		return failScenario("replace_body_real_doc", started, execErr, replaceSuggestions(), nil)
	}
	artifactPath := filepath.Join(ctx.config.Artifacts, "replace-body.docx")
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return failScenario("replace_body_real_doc", started, err, replaceSuggestions(), nil)
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened

	paragraph := findParagraphByText(reopened, newText)
	if paragraph == nil {
		return failScenario("replace_body_real_doc", started, fmt.Errorf("保存后未找到替换文本"), replaceSuggestions(), map[string]any{"artifact": artifactPath})
	}

	return passScenario("replace_body_real_doc", started, "正文替换已通过真实文档读写链路验证。", map[string]any{
		"reopened_style_id": paragraph.Style.StyleID,
		"font_family":       firstTextRunStyleValue(paragraph).FontFamily,
		"font_size_hp":      firstTextRunStyleValue(paragraph).FontSizeHP,
	}, artifactPath)
}

func runWordPDFVisualBaselineScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	leftPDFPath := filepath.Join(ctx.config.Artifacts, "goword-agent-real-doc.word-oracle.source.pdf")
	roundtripDOCXPath := filepath.Join(ctx.config.Artifacts, "goword-agent-real-doc.word-oracle.roundtrip.docx")
	rightPDFPath := filepath.Join(ctx.config.Artifacts, "goword-agent-real-doc.word-oracle.roundtrip.pdf")
	diffOutputDir := filepath.Join(ctx.config.Artifacts, "word-pdf-visual-diff")

	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("打开 roundtrip 会话失败: %w", err), nil, map[string]any{
			"source_docx": ctx.inputDocPath,
		})
	}
	defer ctx.client.closeSession(session.ID)

	saved, err := ctx.client.saveDocument(session.ID, roundtripDOCXPath)
	if err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("保存 roundtrip docx 失败: %w", err), nil, map[string]any{
			"source_docx":         ctx.inputDocPath,
			"roundtrip_docx_path": roundtripDOCXPath,
		})
	}

	if err := exportDOCXToPDFViaWord(ctx.inputDocPath, leftPDFPath); err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, err, []string{
			"确认本机已安装 Microsoft Word，且当前用户具备 COM 自动化导出 PDF 的权限。",
			"若 Word 不可用，再补 LibreOffice soffice 回退路径，不要继续把视觉基线停留在语义布局层。",
		}, map[string]any{
			"source_docx": ctx.inputDocPath,
			"pdf_path":    leftPDFPath,
		})
	}
	if err := exportDOCXToPDFViaWord(saved.Path, rightPDFPath); err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, err, []string{
			"若原始 DOCX 导出成功但 roundtrip DOCX 导出失败，应先检查 SaveDOCX 生成的文件是否仍被 Word 正常接受。",
			"若 Word 报错集中在对象区，优先检查 textbox/shape/anchor 相关 OOXML 是否仍满足 Word 打开约束。",
		}, map[string]any{
			"source_docx":         ctx.inputDocPath,
			"roundtrip_docx_path": saved.Path,
			"roundtrip_pdf_path":  rightPDFPath,
		})
	}

	leftPDFBytes, err := os.ReadFile(leftPDFPath)
	if err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("读取原始 PDF 失败: %w", err), nil, map[string]any{
			"source_docx": ctx.inputDocPath,
			"pdf_path":    leftPDFPath,
		})
	}
	rightPDFBytes, err := os.ReadFile(rightPDFPath)
	if err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("读取 roundtrip PDF 失败: %w", err), nil, map[string]any{
			"source_docx":        ctx.inputDocPath,
			"roundtrip_pdf_path": rightPDFPath,
		})
	}
	leftPDFHash := sha256.Sum256(leftPDFBytes)
	rightPDFHash := sha256.Sum256(rightPDFBytes)

	diffReport, diffReportPath, err := runPDFVisualDiff(ctx.config, leftPDFPath, rightPDFPath, diffOutputDir)
	if err != nil {
		return failScenario("word_pdf_visual_baseline_oracle", started, err, []string{
			"若页图渲染失败，先确认 --python 指向的解释器可导入 pypdfium2 与 Pillow。",
			"若 diff 工具缺依赖，应在当前评测环境补齐 pypdfium2 后重跑，不要把视觉基线退回到仅 PDF 导出。",
		}, map[string]any{
			"source_docx":         ctx.inputDocPath,
			"roundtrip_docx_path": saved.Path,
			"source_pdf_path":     leftPDFPath,
			"roundtrip_pdf_path":  rightPDFPath,
			"diff_output_dir":     diffOutputDir,
		})
	}

	details := map[string]any{
		"source_docx":              ctx.inputDocPath,
		"roundtrip_docx_path":      saved.Path,
		"oracle_backend":           "word_com+pypdfium2",
		"source_pdf_path":          leftPDFPath,
		"source_pdf_size_bytes":    len(leftPDFBytes),
		"source_pdf_sha256":        fmt.Sprintf("%x", leftPDFHash[:]),
		"source_page_count":        diffReport.LeftPageCount,
		"roundtrip_pdf_path":       rightPDFPath,
		"roundtrip_pdf_size_bytes": len(rightPDFBytes),
		"roundtrip_pdf_sha256":     fmt.Sprintf("%x", rightPDFHash[:]),
		"roundtrip_page_count":     diffReport.RightPageCount,
		"compared_page_count":      diffReport.ComparedPageCount,
		"identical_page_count":     countIdenticalPDFPages(diffReport),
		"changed_pages":            changedPDFPages(diffReport),
		"max_pixel_diff_ratio":     maxPDFPixelDiffRatio(diffReport),
		"diff_report_path":         diffReportPath,
		"diff_output_dir":          diffOutputDir,
		"visual_status":            "page-diff-ready",
		"visual_oracle_stage":      "page-image-diff",
	}
	if diffReport.LeftPageCount != diffReport.RightPageCount {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("页数不一致: source=%d roundtrip=%d", diffReport.LeftPageCount, diffReport.RightPageCount), []string{
			"若 roundtrip 后页数变化，应先检查 section layout、表格分页和对象锚点是否改变了 Word 的最终分页。",
			"优先打开生成的 diff 页图，确认是分页漂移、对象重排还是样式变化导致。",
		}, details)
	}
	if !diffReport.AllPagesIdentical {
		return failScenario("word_pdf_visual_baseline_oracle", started, fmt.Errorf("检测到 %d 页存在视觉差异", len(changedPDFPages(diffReport))), []string{
			"优先检查 changed_pages 对应页的 diff 图片，区分是正文样式漂移、表格分页变化，还是 textbox/shape/anchor 对象重排。",
			"若只在单页出现微小偏差，先核对该页的段落 spacing、列表编号和 section layout 写回是否被 roundtrip 放大。",
		}, details)
	}
	return passScenario("word_pdf_visual_baseline_oracle", started, "真实 Word 导出的 source/roundtrip PDF 已完成逐页页图渲染与 diff，当前样本各页视觉一致。", details, diffReportPath)
}

func runOOXMLObjectSummaryComplexScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	inputPath := filepath.Join(ctx.config.Artifacts, "scenario-inputs", "ooxml-object-summary-complex.docx")
	if err := buildOOXMLComplexStructureFixture(inputPath); err != nil {
		return failScenario("ooxml_object_summary_complex_structures", started, err, nil, nil)
	}
	session, err := ctx.server.service.OpenDocument(inputPath)
	if err != nil {
		return failScenario("ooxml_object_summary_complex_structures", started, err, nil, map[string]any{"input_path": inputPath})
	}
	defer ctx.server.service.Close(session.ID)
	if session.Document == nil || session.Document.Meta.ObjectSummary == nil {
		return failScenario("ooxml_object_summary_complex_structures", started, fmt.Errorf("对象摘要为空"), nil, map[string]any{"input_path": inputPath})
	}
	summary := *session.Document.Meta.ObjectSummary
	if summary.ComplexTables != 1 || summary.MergedTableCells != 1 || summary.NestedTables != 1 || summary.Hyperlinks != 1 || summary.Bookmarks != 1 || summary.SimpleFields != 1 || summary.ComplexFields != 1 || summary.FootnoteReferences != 1 || summary.EndnoteReferences != 1 || summary.CommentReferences != 1 || summary.Insertions != 1 || summary.Deletions != 1 {
		return failScenario("ooxml_object_summary_complex_structures", started, fmt.Errorf("对象摘要不符合预期: %+v", summary), nil, map[string]any{"input_path": inputPath, "summary": summary})
	}
	artifactPath := filepath.Join(ctx.config.Artifacts, "ooxml-object-summary-complex-saved.docx")
	saved, err := ctx.server.service.Save(session.ID, artifactPath)
	if err != nil {
		return failScenario("ooxml_object_summary_complex_structures", started, err, nil, map[string]any{"input_path": inputPath})
	}
	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		return failScenario("ooxml_object_summary_complex_structures", started, err, nil, map[string]any{"artifact_path": saved.Path})
	}
	if reopened.Meta.ObjectSummary == nil || *reopened.Meta.ObjectSummary != summary {
		return failScenario("ooxml_object_summary_complex_structures", started, fmt.Errorf("保存复开后对象摘要漂移: before=%+v after=%+v", summary, reopened.Meta.ObjectSummary), nil, map[string]any{"artifact_path": saved.Path})
	}
	return passScenario("ooxml_object_summary_complex_structures", started, "复杂表格、字段、脚注批注与修订对象摘要已接入并通过 no-op save 保真验证。", map[string]any{
		"complex_tables":      summary.ComplexTables,
		"merged_table_cells":  summary.MergedTableCells,
		"nested_tables":       summary.NestedTables,
		"hyperlinks":          summary.Hyperlinks,
		"bookmarks":           summary.Bookmarks,
		"simple_fields":       summary.SimpleFields,
		"complex_fields":      summary.ComplexFields,
		"footnote_references": summary.FootnoteReferences,
		"endnote_references":  summary.EndnoteReferences,
		"comment_references":  summary.CommentReferences,
		"insertions":          summary.Insertions,
		"deletions":           summary.Deletions,
	}, artifactPath)
}

func runOOXMLObjectSummaryImageScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	inputPath := filepath.Join(ctx.config.Artifacts, "scenario-inputs", "ooxml-object-summary-images.docx")
	if err := buildOOXMLImageFixture(inputPath); err != nil {
		return failScenario("ooxml_object_summary_image_variants", started, err, nil, nil)
	}
	session, err := ctx.server.service.OpenDocument(inputPath)
	if err != nil {
		return failScenario("ooxml_object_summary_image_variants", started, err, nil, map[string]any{"input_path": inputPath})
	}
	defer ctx.server.service.Close(session.ID)
	if session.Document == nil || session.Document.Meta.ObjectSummary == nil {
		return failScenario("ooxml_object_summary_image_variants", started, fmt.Errorf("图片对象摘要为空"), nil, map[string]any{"input_path": inputPath})
	}
	summary := *session.Document.Meta.ObjectSummary
	if summary.InlineImages != 1 || summary.AnchoredImages != 2 || summary.LegacyImages != 1 || summary.LinkedImages != 1 || summary.TextBoxes != 1 || summary.Shapes != 1 {
		return failScenario("ooxml_object_summary_image_variants", started, fmt.Errorf("图片对象摘要不符合预期: %+v", summary), nil, map[string]any{"input_path": inputPath, "summary": summary})
	}
	artifactPath := filepath.Join(ctx.config.Artifacts, "ooxml-object-summary-images-saved.docx")
	saved, err := ctx.server.service.Save(session.ID, artifactPath)
	if err != nil {
		return failScenario("ooxml_object_summary_image_variants", started, err, nil, map[string]any{"input_path": inputPath})
	}
	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		return failScenario("ooxml_object_summary_image_variants", started, err, nil, map[string]any{"artifact_path": saved.Path})
	}
	if reopened.Meta.ObjectSummary == nil || *reopened.Meta.ObjectSummary != summary {
		return failScenario("ooxml_object_summary_image_variants", started, fmt.Errorf("保存复开后图片对象摘要漂移: before=%+v after=%+v", summary, reopened.Meta.ObjectSummary), nil, map[string]any{"artifact_path": saved.Path})
	}
	return passScenario("ooxml_object_summary_image_variants", started, "inline、anchor、textbox 与 shape 对象摘要已接入并通过 no-op save 保真验证。", map[string]any{
		"inline_images":   summary.InlineImages,
		"anchored_images": summary.AnchoredImages,
		"legacy_images":   summary.LegacyImages,
		"linked_images":   summary.LinkedImages,
		"text_boxes":      summary.TextBoxes,
		"shapes":          summary.Shapes,
	}, artifactPath)
}

func exportDOCXToPDFViaWord(docxPath, pdfPath string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("当前平台不是 Windows，暂不支持 Word COM 导出")
	}
	if strings.TrimSpace(docxPath) == "" || strings.TrimSpace(pdfPath) == "" {
		return fmt.Errorf("docxPath 和 pdfPath 不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(pdfPath), 0o755); err != nil {
		return fmt.Errorf("创建 PDF 输出目录失败: %w", err)
	}
	if err := os.RemoveAll(pdfPath); err != nil {
		return fmt.Errorf("清理旧 PDF 失败: %w", err)
	}
	script := strings.Join([]string{
		"$ErrorActionPreference='Stop'",
		"$ProgressPreference='SilentlyContinue'",
		"$docx=" + powershellSingleQuoted(docxPath),
		"$pdf=" + powershellSingleQuoted(pdfPath),
		"$word=$null",
		"$doc=$null",
		"try {",
		"  $word = New-Object -ComObject Word.Application",
		"  $word.Visible = $false",
		"  $word.DisplayAlerts = 0",
		"  $doc = $word.Documents.Open($docx, $false, $true)",
		"  $doc.ExportAsFixedFormat($pdf, 17)",
		"} finally {",
		"  if ($doc -ne $null) { try { $doc.Close([ref]$false) } catch {} }",
		"  if ($word -ne $null) { try { $word.Quit() } catch {} }",
		"}",
		"if (!(Test-Path $pdf)) { throw 'Word 导出后未生成 PDF 文件' }",
	}, "; ")
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return fmt.Errorf("Word 导出 PDF 失败: %w", err)
		}
		return fmt.Errorf("Word 导出 PDF 失败: %w: %s", err, trimmed)
	}
	return nil
}

func runPDFVisualDiff(cfg config, leftPDFPath, rightPDFPath, outputDir string) (*pdfVisualDiffReport, string, error) {
	pythonPath, err := resolvePythonPath(cfg)
	if err != nil {
		return nil, "", err
	}
	scriptPath := filepath.Join(cfg.RepoRoot, "scripts", "goword_agent_eval", "pdf_page_diff.py")
	if info, statErr := os.Stat(scriptPath); statErr != nil || info.IsDir() {
		return nil, "", fmt.Errorf("未找到 PDF diff 脚本: %s", scriptPath)
	}
	if err := os.RemoveAll(outputDir); err != nil {
		return nil, "", fmt.Errorf("清理 PDF diff 输出目录失败: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("创建 PDF diff 输出目录失败: %w", err)
	}
	reportPath := filepath.Join(outputDir, "page-diff-report.json")
	cmd := exec.Command(pythonPath, scriptPath, "--left-pdf", leftPDFPath, "--right-pdf", rightPDFPath, "--output-dir", outputDir, "--report-json", reportPath)
	cmd.Dir = cfg.RepoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return nil, reportPath, fmt.Errorf("执行 PDF 页图 diff 失败: %w", err)
		}
		return nil, reportPath, fmt.Errorf("执行 PDF 页图 diff 失败: %w: %s", err, trimmed)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, reportPath, fmt.Errorf("读取 PDF diff 报告失败: %w", err)
	}
	var report pdfVisualDiffReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		return nil, reportPath, fmt.Errorf("解析 PDF diff 报告失败: %w", err)
	}
	return &report, reportPath, nil
}

func countIdenticalPDFPages(report *pdfVisualDiffReport) int {
	if report == nil {
		return 0
	}
	count := 0
	for _, page := range report.Pages {
		if page.ImagesIdentical {
			count++
		}
	}
	return count
}

func changedPDFPages(report *pdfVisualDiffReport) []int {
	if report == nil {
		return nil
	}
	pages := make([]int, 0)
	for _, page := range report.Pages {
		if !page.ImagesIdentical {
			pages = append(pages, page.PageIndex+1)
		}
	}
	return pages
}

func maxPDFPixelDiffRatio(report *pdfVisualDiffReport) float64 {
	if report == nil {
		return 0
	}
	maxRatio := 0.0
	for _, page := range report.Pages {
		if page.PixelDiffRatio > maxRatio {
			maxRatio = page.PixelDiffRatio
		}
	}
	return maxRatio
}

func approximatePDFPageCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	matches := regexp.MustCompile(`/Type\s*/Page([^s]|$)`).FindAll(content, -1)
	return len(matches)
}

func powershellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func runInsertBodyScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "insert-body.docx")
	result, tuned, adjustments, err := runInsertBodyAttempt(ctx, artifactPath, false)
	if err != nil {
		result, tuned, adjustments, err = runInsertBodyAttempt(ctx, artifactPath, true)
		if err != nil {
			return failScenario("insert_body_after_heading", started, err, []string{
				"若标题后插正文仍沿用标题样式，应继续收紧模板选择：优先取当前章节紧邻正文，其次回退最近正文。",
				"若 body 插入依赖弱证据失败，应增强 format_basis，显式写出 style_id=Normal 与正文字体字号。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	details := map[string]any{
		"template_role":     result.TemplateRole,
		"inserted_style_id": result.InsertedParagraph.Style.StyleID,
		"font_family":       firstTextRunStyleValue(result.InsertedParagraph).FontFamily,
		"font_size_hp":      firstTextRunStyleValue(result.InsertedParagraph).FontSizeHP,
	}
	message := "标题后插入正文已正确沿用正文模板。"
	if tuned {
		message = "标题后插入正文在自动增强 format_basis 后通过验证。"
	}
	return scenarioResult{
		Name:              "insert_body_after_heading",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runInsertHeadingScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "insert-heading.docx")
	result, tuned, adjustments, err := runInsertHeadingAttempt(ctx, artifactPath, false)
	if err != nil {
		result, tuned, adjustments, err = runInsertHeadingAttempt(ctx, artifactPath, true)
		if err != nil {
			return failScenario("insert_peer_heading", started, err, []string{
				"若同级标题插入没有继承同级标题样式，应加强 format_basis 中的 style_id=HeadingN 和 reader_context_summary 的层级描述。",
				"若插入标题被错误当成正文，应继续收紧标题判定规则，优先使用 format_basis 和 new_text 的双重判断。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	details := map[string]any{
		"template_role":     result.TemplateRole,
		"inserted_style_id": result.InsertedParagraph.Style.StyleID,
		"font_family":       firstTextRunStyleValue(result.InsertedParagraph).FontFamily,
		"font_size_hp":      firstTextRunStyleValue(result.InsertedParagraph).FontSizeHP,
	}
	message := "同级标题插入已正确沿用标题模板。"
	if tuned {
		message = "同级标题插入在自动增强层级证据后通过验证。"
	}
	return scenarioResult{
		Name:              "insert_peer_heading",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runSemanticPreciseAnchorScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "semantic-precise-anchor.docx")
	result, tuned, adjustments, err := runPreciseAnchorRewriteAttempt(ctx, artifactPath, false)
	if err != nil {
		result, tuned, adjustments, err = runPreciseAnchorRewriteAttempt(ctx, artifactPath, true)
		if err != nil {
			return failScenario("semantic_precise_anchor_appendix", started, err, []string{
				"当文档中存在多处‘说明’类段落时，应优先基于 query_hits 选择正确的 paragraph_id，再执行改写。",
				"若模糊 query 导致命中错段，应增强 reader_context_summary，显式说明‘附录末尾说明段’，并透传 preferred_paragraph_id。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	appendixParagraph := findParagraphByText(result.ReopenedDocument, "附录说明：本报告用于自动化回归、结果复盘与投产前验收。")
	details := map[string]any{
		"search_scope":         result.SearchResult.Scope,
		"search_total":         result.SearchResult.Total,
		"appendix_style_id":    paragraphStyleID(appendixParagraph),
		"other_note_unchanged": findParagraphByText(result.ReopenedDocument, "验证说明：当前版本仍需人工复核。") != nil,
	}
	message := "多处说明类段落中的附录改写已正确命中目标段落。"
	if tuned {
		message = "多处说明类段落中的附录改写在自动补齐 paragraph_id 后通过验证。"
	}
	return scenarioResult{
		Name:              "semantic_precise_anchor_appendix",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runSummaryInferredAnchorScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "summary-inferred-anchor.docx")
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, err, []string{
			"先修复 openDocument，否则无法验证 summary 自动补锚点链路。",
		}, nil)
	}
	defer ctx.client.closeSession(session.ID)

	appendixText := "附录说明：本报告用于自动化回归。"
	reader, err := ctx.runner.runReader("", session.ID, appendixText, "paragraph")
	if err != nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, err, []string{
			"若 reader 无法命中文末附录说明，应先修复 paragraph scope 下的精确检索。",
		}, nil)
	}
	appendixHit := findReaderQueryHitContaining(reader, appendixText)
	if appendixHit == nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("未找到附录说明命中"), []string{
			"确保 reader 对附录说明段返回可复用的 paragraph_id，便于后续 summary 自动补锚点。",
		}, nil)
	}

	newText := "附加说明：保存后必须写回源文档。"
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:             "insert",
		TargetText:         "说明",
		NewText:            newText,
		Position:           "after",
		ContextScope:       "paragraph",
		EnableTrackChanges: true,
		ReaderSessionID:    session.ID,
		ReaderContextSummary: fmt.Sprintf(
			"最终目标是文末附录说明段，段落 ID: %s；需要在该段后补一句正式说明，不能改动前面的交付说明或验证说明。",
			appendixHit.ParagraphID,
		),
		FormatBasis: formatStyleBasis(appendixHit.ParagraphStyle, appendixHit.DominantTextStyle, "附录说明正文样式"),
		ExplorationSummary: fmt.Sprintf(
			"已确认该命中是最后那条说明，前文还有交付说明与验证说明，必须锁定文末附录说明段。",
		),
	})
	if err != nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, err, []string{
			"若 bridge 无法在 summary-only 输入下产生有效动作，应优先检查 copilot 对 reader_context_summary 的透传。",
		}, nil)
	}
	if action.PreferredParagraphID != appendixHit.ParagraphID {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("summary 未补齐 preferred_paragraph_id: got=%q want=%q", action.PreferredParagraphID, appendixHit.ParagraphID), []string{
			"确保 bridge 输出的 __WORD_EDITOR_SIG__ 统一经过后端 signal 归一化，再交给执行层。",
		}, map[string]any{"target_text": action.TargetText})
	}
	if action.ReaderSessionID != session.ID {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("reader_session_id 透传异常: got=%q want=%q", action.ReaderSessionID, session.ID), []string{
			"当前合同下，精确锚点写入必须显式透传 reader_session_id；评估器应校验 bridge 输出没有丢失该字段。",
		}, nil)
	}

	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, err, []string{
			"若 summary 自动补锚点后仍执行失败，应优先检查 insert 对 preferred_paragraph_id 的优先级是否高于模糊检索命中。",
		}, map[string]any{"preferred_paragraph_id": action.PreferredParagraphID, "reader_session_id": action.ReaderSessionID})
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, err, []string{
			"若 save/reopen 失败，应检查自动补锚点后的写入结果是否被完整持久化。",
		}, nil)
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, newText)
	if inserted == nil {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("保存复开后未找到新增说明"), []string{
			"若文档预览有变但复开后缺字，优先检查保存链路是否真正落盘。",
		}, map[string]any{"artifact": artifactPath})
	}
	if paragraphImmediatelyAfterText(reopened, appendixText) != inserted {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("新增说明未插入到附录说明后方"), []string{
			"模糊 target_text 仍出现错位时，执行层必须优先使用从 summary 推断出的 preferred_paragraph_id。",
		}, map[string]any{"preferred_paragraph_id": action.PreferredParagraphID, "artifact": artifactPath})
	}
	if paragraphImmediatelyAfterText(reopened, "交付说明：交付物以验收版本为准。") == inserted {
		return failScenario("summary_inferred_anchor_roundtrip", started, fmt.Errorf("新增说明错误插入到了交付说明后方"), []string{
			"出现错段时，应优先检查模糊 query 的多命中场景是否被 preferred_paragraph_id 覆盖。",
		}, nil)
	}

	return passScenario("summary_inferred_anchor_roundtrip", started, "summary 中的 paragraph_id 已被真实 signal 解析链路自动补齐，且显式透传的 reader_session_id 在保存复开后保持正确落点。", map[string]any{
		"preferred_paragraph_id": action.PreferredParagraphID,
		"reader_session_id":      action.ReaderSessionID,
		"inserted_style_id":      paragraphStyleID(inserted),
		"search_total":           result.SearchResult.Total,
		"target_text":            action.TargetText,
	}, artifactPath)
}

func runFormatEmphasisScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "format-emphasis.docx")
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("format_inline_emphasis", started, err, []string{
			"先修复 openDocument，否则无法验证真实 DOCX 中的字体、字号、加粗等格式编辑。",
		}, nil)
	}
	defer ctx.client.closeSession(session.ID)

	paragraphText := "本报告用于验证 Agent 对真实 Word 文档的结构理解、格式保持与自动化回归能力。"
	targetText := "自动化回归能力"
	reader, err := ctx.runner.runReader("", session.ID, targetText, "paragraph")
	if err != nil {
		return failScenario("format_inline_emphasis", started, err, []string{
			"若格式场景无法读取正文，应优先检查 reader 在 paragraph scope 下的检索稳定性。",
		}, nil)
	}
	hit := firstQueryHit(reader)
	if hit == nil {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("未找到待强调短语"), []string{
			"确保 reader 对短语级 query 返回 paragraph_id、paragraph_style 和 dominant_text_style，便于格式修改准确落点。",
		}, nil)
	}

	contextSummary := "目标短语位于导语末尾，需要在不改正文内容的前提下突出显示，用于企业级验收汇报。"
	formatBasis := formatStyleBasis(hit.ParagraphStyle, hit.DominantTextStyle, "导语正文样式") + "；仅将命中短语设置为黑体、加粗、字号=26、颜色=C00000。"
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "format",
		TargetText:           targetText,
		PreferredParagraphID: hit.ParagraphID,
		ReaderSessionID:      session.ID,
		StylePatch: goword.TextStyle{
			FontFamily: "黑体",
			FontSizeHP: 26,
			Bold:       true,
			BoldSet:    true,
			Color:      "C00000",
		},
		ContextScope:         "paragraph",
		RetrievalQuery:       targetText,
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
		ExplorationSummary: fmt.Sprintf(
			"已围绕导语段命中展开前后邻段，最终锁定 paragraph_id: %s；命中短语位于导语末尾，仅需修改该短语样式，段落正文内容保持不变。",
			hit.ParagraphID,
		),
	})
	if err != nil {
		return failScenario("format_inline_emphasis", started, err, []string{
			"若 format 动作构造失败，应优先检查 bridge 对 style_patch JSON 和 format_basis 的参数校验。",
		}, nil)
	}

	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return failScenario("format_inline_emphasis", started, err, []string{
			"若正文短语格式修改失败，应优先检查 update_text_style 的 paragraph_id、offset 与 style_patch 是否来自同一次命中。",
		}, nil)
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return failScenario("format_inline_emphasis", started, err, []string{
			"若格式修改保存后丢失，应优先检查 SaveDOCX 对 run.rPr 的写回完整性。",
		}, nil)
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened

	paragraph := findParagraphByText(reopened, paragraphText)
	if paragraph == nil {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("保存后未找到导语段落"), nil, map[string]any{"artifact": artifactPath})
	}
	if strings.TrimSpace(paragraph.PlainText()) != paragraphText {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("格式修改意外改变了正文内容"), nil, map[string]any{"artifact": artifactPath})
	}
	styledRun := findTextRunContaining(paragraph, targetText)
	if styledRun == nil {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("保存后未找到被强调短语 run"), nil, map[string]any{"artifact": artifactPath})
	}
	if !textStyleSatisfies(styledRun.Style, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true, Color: "C00000"}) {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("强调短语样式不符合预期: %+v", styledRun.Style), nil, map[string]any{"artifact": artifactPath})
	}
	unaffectedRun := findTextRunContaining(paragraph, "结构理解、格式保持与")
	if unaffectedRun == nil {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("未找到未修改的相邻文本 run"), nil, map[string]any{"artifact": artifactPath})
	}
	if unaffectedRun.Style.Bold {
		return failScenario("format_inline_emphasis", started, fmt.Errorf("格式修改污染了相邻正文样式"), nil, map[string]any{"artifact": artifactPath})
	}

	return passScenario("format_inline_emphasis", started, "正文短语强调已通过真实 DOCX 样式编辑与保存复开验证。", map[string]any{
		"styled_font_family": styledRun.Style.FontFamily,
		"styled_font_size":   styledRun.Style.FontSizeHP,
		"styled_bold":        styledRun.Style.Bold,
		"styled_color":       styledRun.Style.Color,
	}, artifactPath)
}

func runDialogueMiddleNoteScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "dialogue-middle-note.docx")
	result, err := runDialogueMiddleNoteAttempt(ctx, artifactPath)
	if err != nil {
		return failScenario("dialogue_middle_note_relabel", started, err, []string{
			"当用户只说‘中间那条说明’这类模糊话术时，应结合 reader_context_summary 中的语义线索和命中内容进行排序，而不是机械取第一条命中。",
			"若多条说明都命中同一 query，应优先用上下文中的关键词，如‘人工复核’、‘附录’、‘第三方费用’等增强命中评分。",
		}, map[string]any{"artifact": artifactPath})
	}

	editedParagraph := findParagraphByText(result.ReopenedDocument, "验证说明（审阅项）：当前版本仍需人工复核。")
	return passScenario("dialogue_middle_note_relabel", started, "真实对话场景下，agent 已通过模糊语义命中人工复核那条说明并完成精准改写。", map[string]any{
		"edited_style_id":         paragraphStyleID(editedParagraph),
		"search_total":            result.SearchResult.Total,
		"delivery_note_unchanged": findParagraphByText(result.ReopenedDocument, "交付说明：交付物以验收版本为准。") != nil,
		"appendix_note_unchanged": findParagraphByText(result.ReopenedDocument, "附录说明：本报告用于自动化回归。") != nil,
	}, artifactPath)
}

func runDialogueFuzzyFeeInsertScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "dialogue-fuzzy-fee-insert.docx")
	result, err := runDialogueFuzzyFeeInsertAttempt(ctx, artifactPath)
	if err != nil {
		return failScenario("dialogue_fuzzy_fee_insert", started, err, []string{
			"当用户说‘第三方费用那条后面补一句’时，不能只根据通用词‘费用’落点，应结合上下文中的‘第三方’等语义关键词排序命中结果。",
			"若模糊语义下的插入仍落到错误段落，应继续增强 search hit 评分，优先匹配真实对话中的业务短语，而不是只看 query 命中顺序。",
		}, map[string]any{"artifact": artifactPath})
	}

	insertedParagraph := findParagraphByText(result.ReopenedDocument, "需在采购前完成审批备案。")
	return passScenario("dialogue_fuzzy_fee_insert", started, "真实对话场景下，agent 已通过模糊语义命中第三方费用条款，并将补充说明插入到正确位置。", map[string]any{
		"inserted_style_id":            paragraphStyleID(insertedParagraph),
		"search_total":                 result.SearchResult.Total,
		"inserted_after_vendor_clause": paragraphImmediatelyAfterText(result.ReopenedDocument, "第三方费用由甲方另行承担，并在采购前单独确认。") == insertedParagraph,
		"travel_fee_clause_unchanged":  findParagraphByText(result.ReopenedDocument, "差旅费用由乙方内部报销，不计入本次报价。") != nil,
	}, artifactPath)
}

func runDialogueTailRemarkScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "dialogue-tail-remark.docx")
	result, err := runDialogueTailRemarkAttempt(ctx, artifactPath)
	if err != nil {
		return failScenario("dialogue_tail_remark_relabel", started, err, []string{
			"当用户只说‘最后那条备注’时，应结合末尾序位词和上下文中的‘附录’‘归档’等语义线索排序命中，而不是继续停留在前面的说明段落。",
			"若尾部备注改写仍命中错误段落，应继续增强 ordinal cue 对‘最后/末尾/结尾’的加权，并压低正文请求对标题命中的得分。",
		}, map[string]any{"artifact": artifactPath})
	}

	editedParagraph := findParagraphByText(result.ReopenedDocument, "附录说明（归档备注）：本报告用于自动化回归。")
	return passScenario("dialogue_tail_remark_relabel", started, "真实对话场景下，agent 已按‘最后那条备注’命中附录说明并完成正式化改写。", map[string]any{
		"edited_style_id":         paragraphStyleID(editedParagraph),
		"search_total":            result.SearchResult.Total,
		"delivery_note_unchanged": findParagraphByText(result.ReopenedDocument, "交付说明：交付物以验收版本为准。") != nil,
		"middle_note_unchanged":   findParagraphByText(result.ReopenedDocument, "验证说明：当前版本仍需人工复核。") != nil,
	}, artifactPath)
}

func runDialogueRiskCellInsertScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "dialogue-risk-cell-insert.docx")
	result, err := runDialogueRiskCellInsertAttempt(ctx, artifactPath)
	if err != nil {
		return failScenario("dialogue_risk_cell_insert", started, err, []string{
			"当用户说‘风险提示那格下面补一句’时，应把‘那格’理解为表格单元格范围，在同一单元格内选择正文模板，而不是把新增内容插到别的行或继承标题样式。",
			"若表格弱锚点插入仍依赖精确 target_text 才能成功，应继续增强同一单元格正文模板回退和 table scope 命中排序。",
		}, map[string]any{"artifact": artifactPath})
	}

	insertedParagraph := findParagraphByText(result.ReopenedDocument, "上线前需先确认灰度范围。")
	return passScenario("dialogue_risk_cell_insert", started, "真实对话场景下，agent 已在风险提示那格内补充说明，并保持表格正文样式。", map[string]any{
		"inserted_style_id":     paragraphStyleID(insertedParagraph),
		"search_total":          result.SearchResult.Total,
		"same_cell":             tableCellContainsTexts(result.ReopenedDocument, "风险提示", "上线前需先确认灰度范围。"),
		"throughput_unchanged":  tableContainsText(result.ReopenedDocument, "85%"),
		"review_flag_unchanged": tableContainsText(result.ReopenedDocument, "需人工复核"),
	}, artifactPath)
}

func runReplaceTableScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "replace-table.docx")
	targetText := "85%"
	newText := "96%"
	result, tuned, adjustments, err := runReplaceTableAttempt(ctx, artifactPath, targetText, newText, false)
	if err != nil {
		result, tuned, adjustments, err = runReplaceTableAttempt(ctx, artifactPath, targetText, newText, true)
		if err != nil {
			return failScenario("replace_table_cell", started, err, []string{
				"若表格命中失败，应优先扩大检索 scope 到 table/all，并保留 row_index/cell_index 辅助信息。",
				"若替换写回失败，应检查表格单元格段落的段落 ID 与 offset 是否在 search 命中后被正确使用。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	details := map[string]any{
		"template_role": result.TemplateRole,
		"hit_scope":     result.SearchResult.Scope,
		"hit_total":     result.SearchResult.Total,
	}
	message := "表格单元格替换已通过真实文档验证。"
	if tuned {
		message = "表格单元格替换在自动扩大检索范围后通过验证。"
	}
	return scenarioResult{
		Name:              "replace_table_cell",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runInsertTableParagraphScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "insert-table-paragraph.docx")
	result, tuned, adjustments, err := runInsertTableParagraphAttempt(ctx, artifactPath, false)
	if err != nil {
		result, tuned, adjustments, err = runInsertTableParagraphAttempt(ctx, artifactPath, true)
		if err != nil {
			return failScenario("insert_table_cell_paragraph", started, err, []string{
				"若表格内插入说明正文时仍沿用单元格标题样式，应优先在同一单元格内寻找非标题/非标签段落作为模板。",
				"若单元格插入必须依赖强证据才能成功，应继续加强 format_basis，对同一单元格正文样式给出明确 style_id、字体和字号。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	details := map[string]any{
		"template_role":     result.TemplateRole,
		"inserted_style_id": result.InsertedParagraph.Style.StyleID,
		"font_family":       firstTextRunStyleValue(result.InsertedParagraph).FontFamily,
		"font_size_hp":      firstTextRunStyleValue(result.InsertedParagraph).FontSizeHP,
		"same_cell":         tableCellContainsTexts(result.ReopenedDocument, "风险提示", "需在回归前确认样式模板稳定。"),
	}
	message := "表格单元格内插入正文已正确沿用同一单元格正文样式。"
	if tuned {
		message = "表格单元格内插入正文在自动增强样式模板证据后通过验证。"
	}
	return scenarioResult{
		Name:              "insert_table_cell_paragraph",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runInsertListExplanationScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	artifactPath := filepath.Join(ctx.config.Artifacts, "insert-list-explanation.docx")
	result, tuned, adjustments, err := runInsertListExplanationAttempt(ctx, artifactPath, false)
	if err != nil {
		result, tuned, adjustments, err = runInsertListExplanationAttempt(ctx, artifactPath, true)
		if err != nil {
			return failScenario("insert_list_explanation", started, err, []string{
				"若编号步骤后的说明正文仍继承了列表编号，应优先把列表项排除出正文模板候选。",
				"若列表正文插入依赖强证据才能通过，应继续在 format_basis 中显式声明非编号正文、style_id=Normal 及字体字号。",
			}, map[string]any{"tuning_attempted": true})
		}
	}

	details := map[string]any{
		"template_role":     result.TemplateRole,
		"inserted_style_id": result.InsertedParagraph.Style.StyleID,
		"font_family":       firstTextRunStyleValue(result.InsertedParagraph).FontFamily,
		"font_size_hp":      firstTextRunStyleValue(result.InsertedParagraph).FontSizeHP,
		"has_numbering":     result.InsertedParagraph.Style.Numbering != nil,
	}
	message := "编号步骤后的说明正文已保持非编号正文样式。"
	if tuned {
		message = "编号步骤后的说明正文在自动增强非编号证据后通过验证。"
	}
	return scenarioResult{
		Name:              "insert_list_explanation",
		Passed:            true,
		AutoTuned:         tuned,
		DurationMillis:    time.Since(started).Milliseconds(),
		Message:           message,
		ArtifactPath:      artifactPath,
		Details:           details,
		TuningAdjustments: adjustments,
	}
}

func runEnterpriseWorkflowScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"先修复 openDocument，否则无法执行多步骤真实工作流验证。",
		}, nil)
	}
	defer ctx.client.closeSession(session.ID)

	replaceTarget := "当前系统已经支持全文检索、结构识别与样式继承。"
	replaceNewText := "当前系统已经支持全文检索、结构识别、样式继承、自动验证与审计留痕。"
	if err := executeWorkflowBridgeAction(ctx, session.ID, bridgeRequest{
		Action:               "replace",
		TargetText:           replaceTarget,
		NewText:              replaceNewText,
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: "目标内容位于正文段落中，本次替换属于审计增强型正文修订；已检查目标前后相邻正文后确认仍保持同一段，不新增换行或拆段。",
		FormatBasis:          "沿用命中正文段落样式，style_id=Normal，字体=宋体，字号=24；参考前后相邻正文句子节奏后，确认本次是同段扩写，不改换行或断段。",
	}); err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的正文替换失败时，应优先检查 replace 的检索与 offset 定位链路。",
		}, map[string]any{"step": "replace_body"})
	}

	if err := executeWorkflowBridgeAction(ctx, session.ID, bridgeRequest{
		Action:               "format",
		TargetText:           "自动验证与审计留痕",
		StylePatch:           goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true, Color: "C00000"},
		ContextScope:         "paragraph",
		RetrievalQuery:       "审计留痕",
		EnableTrackChanges:   true,
		ReaderContextSummary: "目标短语位于现状分析正文中，需要在不改变正文内容的前提下突出显示审计重点。",
		FormatBasis:          "沿用命中正文段落样式，style_id=Normal，字体=宋体，字号=24；仅将命中短语设置为黑体、加粗、字号=26、颜色=C00000。",
	}); err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的重点强调失败时，应优先检查 format 动作的 style_patch、命中短语范围和保存复开后的 run 样式写回。",
		}, map[string]any{"step": "format_phrase"})
	}

	if err := executeWorkflowBridgeAction(ctx, session.ID, bridgeRequest{
		Action:               "insert",
		TargetText:           "二、实施步骤",
		NewText:              "三、质量门禁",
		ParagraphStylePatch:  goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100},
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: "目标位置属于二级标题区域，新增内容是同级标题。",
		FormatBasis:          "沿用同级 Heading2 标题样式，style_id=Heading2，字体=黑体，字号=26。",
	}); err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的同级标题插入失败时，应优先检查标题层级识别与模板选择逻辑。",
		}, map[string]any{"step": "insert_heading"})
	}

	tableReader, err := ctx.runner.runReader("", session.ID, "85%", "table")
	if err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的表格替换失败前，应先保证 reader 能稳定返回目标表格值单元格。",
		}, map[string]any{"step": "replace_table_reader"})
	}
	tableHit := findReaderQueryHitContaining(tableReader, "85%")
	if tableHit == nil || strings.TrimSpace(tableHit.TableID) == "" {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("reader 未返回 85%% 所在单元格命中"), []string{
			"多步骤工作流中的表格替换应先锁定真实 table_id / row_index / cell_index。",
		}, map[string]any{"step": "replace_table_reader"})
	}
	if err := executeWorkflowBridgeAction(ctx, session.ID, bridgeRequest{
		Action:               "update_table_cell",
		TargetText:           "85%",
		TableID:              tableHit.TableID,
		RowIndex:             tableHit.RowIndex,
		RowIndexSet:          true,
		CellIndex:            tableHit.CellIndex,
		CellIndexSet:         true,
		TableCell:            singleParagraphTableCellInput("98%", "TableBody"),
		ContextScope:         "table",
		EnableTrackChanges:   true,
		ReaderSessionID:      session.ID,
		ReaderContextSummary: "目标内容位于表格单元格中，需保持表格正文样式一致。",
		FormatBasis:          formatStyleBasis(tableHit.ParagraphStyle, tableHit.DominantTextStyle, "命中单元格正文样式") + "；最终单元格仍为单段 TableBody 正文。",
		ExplorationSummary:   fmt.Sprintf("已锁定表格 %s 第 %d 行第 %d 列的通过率单元格，仅将 85%% 更新为 98%%。", tableHit.TableID, tableHit.RowIndex, tableHit.CellIndex),
	}); err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的表格替换失败时，应优先检查表格命中范围和单元格 offset 定位。",
		}, map[string]any{"step": "replace_table"})
	}

	listAction, err := executeWorkflowBridgeActionWithResult(ctx, session.ID, bridgeRequest{
		Action:               "insert",
		TargetText:           "收集需求清单",
		NewText:              "本步骤需附带访谈纪要与责任人签字。",
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: "目标位置位于编号步骤后，新增内容属于该步骤下方的非编号说明正文。",
		FormatBasis:          "沿用列表后的说明正文样式，style_id=Normal，字体=宋体，字号=24，不得继承编号。",
	})
	if err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"多步骤工作流中的列表说明插入失败时，应优先排查列表项模板选择与非编号正文回退逻辑。",
		}, map[string]any{"step": "insert_list_body"})
	}

	artifactPath := filepath.Join(ctx.config.Artifacts, "enterprise-workflow.docx")
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"若企业工作流在最终 save/reopen 失败，应优先检查多步骤编辑后的 DOCX 重写完整性。",
		}, nil)
	}

	if findParagraphByText(reopened, replaceNewText) == nil || findParagraphByText(reopened, "三、质量门禁") == nil || !tableContainsText(reopened, "98%") {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("多步骤工作流保存后存在缺失编辑结果"), []string{
			"确保多步骤工作流中的每一步修改都在最终 save/reopen 后持久化。",
		}, map[string]any{"artifact": artifactPath})
	}
	insertedListParagraph := findParagraphByText(reopened, "本步骤需附带访谈纪要与责任人签字。")
	if insertedListParagraph == nil {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("多步骤工作流保存后未找到列表说明正文"), []string{
			"确保列表说明正文在多步骤场景中不会被后续操作覆盖。",
		}, map[string]any{"artifact": artifactPath})
	}
	if insertedListParagraph.Style.Numbering != nil {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("列表说明正文错误继承了编号"), []string{
			"确保企业工作流中的说明正文不继承列表编号。",
		}, map[string]any{"artifact": artifactPath})
	}
	if err := assertEnterpriseDocumentIntegrity(reopened); err != nil {
		return failScenario("enterprise_workflow_real_doc", started, err, []string{
			"若工作流完成后控制样式发生漂移，应优先检查模板选择和样式复制是否污染未修改内容。",
		}, map[string]any{"artifact": artifactPath})
	}
	formattedBodyParagraph := findParagraphByText(reopened, replaceNewText)
	if formattedBodyParagraph == nil {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("多步骤工作流保存后未找到格式化后的正文段落"), []string{
			"确保 format 动作不会破坏正文段落的持久化与后续 reopen 校验。",
		}, map[string]any{"artifact": artifactPath})
	}
	formattedRun := findTextRunContaining(formattedBodyParagraph, "自动验证与审计留痕")
	if formattedRun == nil || !textStyleSatisfies(formattedRun.Style, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true, Color: "C00000"}) {
		return failScenario("enterprise_workflow_real_doc", started, fmt.Errorf("多步骤工作流中的重点短语样式不符合预期"), []string{
			"若 workflow 中的格式强调未持久化，应检查 update_text_style 与 SaveDOCX 的联动是否遗漏 run.rPr。",
		}, map[string]any{"artifact": artifactPath})
	}

	return passScenario("enterprise_workflow_real_doc", started, "企业级多步骤真实工作流已完成并通过完整性校验。", map[string]any{
		"saved_path":                  saved.Path,
		"list_template_role":          listAction.TemplateRole,
		"inserted_list_style_id":      insertedListParagraph.Style.StyleID,
		"inserted_list_has_numbering": insertedListParagraph.Style.Numbering != nil,
		"control_title_style":         findParagraphByText(reopened, "Auramate 智能编辑综合验证报告").Style.StyleID,
		"formatted_phrase_bold":       formattedRun.Style.Bold,
	}, artifactPath)
}

func runSessionRecoveryScenario(ctx *evalContext) scenarioResult {
	started := time.Now()
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"优先修复 openDocument 失败，否则无法验证真实文档会话恢复链路。",
		}, nil)
	}

	targetText := "附录说明：本报告用于自动化回归。"
	newText := "附录说明：本报告用于自动化回归与调优验证。"
	reader, err := ctx.runner.runReader("", session.ID, targetText, "paragraph")
	if err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"修复 reader 获取不到附录正文的问题，保证恢复场景同样依赖 reader 证据。",
		}, nil)
	}
	contextSummary, formatBasis := buildParagraphEvidence(reader, targetText)
	contextSummary, formatBasis = appendExpandedRewriteSameParagraphEvidence(contextSummary, formatBasis, "附录说明")
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           targetText,
		NewText:              newText,
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"若 bridge 无法发出恢复场景动作，应优先检查 reader 证据构造逻辑。",
		}, nil)
	}
	if _, err := executeEditorAction(ctx.client, session.ID, action); err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"若恢复前编辑失败，应先确保基础 replace 流程稳定。",
		}, nil)
	}

	if err := ctx.client.closeSession(session.ID); err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"若 closeSession 失败，应先修复会话关闭接口。",
		}, nil)
	}

	artifactPath := filepath.Join(ctx.config.Artifacts, "session-recovery.docx")
	saved, err := ctx.client.saveDocument(session.ID, artifactPath)
	if err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"若关闭会话后 save 失败，应继续加强 snapshot 落盘与 getOrRestoreSessionLocked 恢复链路。",
		}, nil)
	}
	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		return failScenario("session_recovery_after_real_edit", started, err, []string{
			"若 save 成功但 reopen 失败，应检查真实 DOCX 重写路径与落盘后的包完整性。",
		}, nil)
	}
	if findParagraphByText(reopened, newText) == nil {
		return failScenario("session_recovery_after_real_edit", started, fmt.Errorf("恢复保存后未保留编辑结果"), []string{
			"若恢复后的内容未保存，应优先检查 snapshot 是否覆盖到最新文档修订。",
		}, map[string]any{"artifact": artifactPath})
	}

	return passScenario("session_recovery_after_real_edit", started, "关闭会话后的保存已从快照恢复并保持真实文档修改结果。", map[string]any{
		"saved_path": artifactPath,
	}, artifactPath)
}

func runInsertBodyAttempt(ctx *evalContext, artifactPath string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "第一章 项目背景", "paragraph")
	if err != nil {
		return nil, false, nil, err
	}
	contextSummary, formatBasis := buildBodyInsertEvidence(reader)
	adjustments := []string{}
	if strongEvidence {
		formatBasis += "；明确要求沿用 style_id=Normal，字体=宋体，字号=24。"
		contextSummary += "，且新增内容应使用该章节正文样式。"
		adjustments = append(adjustments, "自动追加 style_id=Normal 与正文字体字号，增强 body 模板证据。")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "第一章 项目背景",
		NewText:              "本章节新增自动化验证说明段落。",
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, action.NewText)
	if inserted == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("保存后未找到新增正文")
	}
	result.InsertedParagraph = inserted
	if paragraphLooksLikeHeading(*inserted) || !strings.EqualFold(normalizeStyleID(inserted.Style.StyleID), "normal") {
		return nil, strongEvidence, adjustments, fmt.Errorf("新增正文样式异常: style_id=%s", inserted.Style.StyleID)
	}
	return result, strongEvidence, adjustments, nil
}

func runInsertHeadingAttempt(ctx *evalContext, artifactPath string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "一、现状分析", "paragraph")
	if err != nil {
		return nil, false, nil, err
	}
	contextSummary, formatBasis := buildHeadingEvidence(reader, 2)
	adjustments := []string{}
	if strongEvidence {
		formatBasis += "；明确要求沿用同级 style_id=Heading2，字体=黑体，字号=26。"
		contextSummary += "，新增内容属于该小节的同级标题。"
		adjustments = append(adjustments, "自动追加 Heading2 同级标题证据，增强标题模板选择。")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "一、现状分析",
		NewText:              "二、验证策略",
		ParagraphStylePatch:  goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100},
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, action.NewText)
	if inserted == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("保存后未找到新增标题")
	}
	result.InsertedParagraph = inserted
	if !strings.EqualFold(normalizeStyleID(inserted.Style.StyleID), "heading2") {
		return nil, strongEvidence, adjustments, fmt.Errorf("新增标题样式异常: style_id=%s", inserted.Style.StyleID)
	}
	return result, strongEvidence, adjustments, nil
}

func runPreciseAnchorRewriteAttempt(ctx *evalContext, artifactPath string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	targetText := "附录说明：本报告用于自动化回归。"
	newText := "附录说明：本报告用于自动化回归、结果复盘与投产前验收。"
	reader, err := ctx.runner.runReader("", session.ID, "说明", "paragraph")
	if err != nil {
		return nil, false, nil, err
	}
	appendixHit := findReaderQueryHitContaining(reader, "附录说明")
	if appendixHit == nil {
		return nil, false, nil, fmt.Errorf("reader 未返回附录说明命中")
	}

	adjustments := []string{}
	request := bridgeRequest{
		Action:               "replace",
		TargetText:           targetText,
		NewText:              newText,
		ContextScope:         "paragraph",
		RetrievalQuery:       "说明",
		EnableTrackChanges:   true,
		ReaderSessionID:      session.ID,
		ReaderContextSummary: "文档中存在多处说明类段落，本次需要改写附录末尾的说明段，保持附录正文格式，并继续保持同一段附录说明，不新增换行或拆段。",
		FormatBasis:          formatStyleBasis(appendixHit.ParagraphStyle, appendixHit.DominantTextStyle, "附录正文样式") + "；已核对前后相邻段落后确认本次只是同段扩写，仍保持单段附录说明，不改换行或断段。",
		ExplorationSummary: fmt.Sprintf(
			"已围绕多条说明命中展开局部上下文，最终锁定文末附录说明段，paragraph_id: %s；其前方仍保留交付说明与验证说明，不能误改。",
			appendixHit.ParagraphID,
		),
	}
	if strongEvidence {
		request.PreferredParagraphID = appendixHit.ParagraphID
		request.ReaderContextSummary += "，并显式透传附录说明段的 paragraph_id。"
		adjustments = append(adjustments, "自动补齐附录说明段的 preferred_paragraph_id，避免模糊 query 命中错误说明段落。")
	}

	action, _, err := ctx.runner.runBridge(request)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	if findParagraphByText(reopened, newText) == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("保存后未找到附录改写结果")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("非目标说明段被错误修改")
	}
	return result, strongEvidence, adjustments, nil
}

func runReplaceTableAttempt(ctx *evalContext, artifactPath, targetText, newText string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	scope := "table"
	adjustments := []string{}
	if strongEvidence {
		scope = "all"
		adjustments = append(adjustments, "自动将表格替换检索范围从 table 扩大为 all，降低命中丢失概率。")
	}
	reader, err := ctx.runner.runReader("", session.ID, targetText, scope)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	targetHit := findReaderQueryHitContaining(reader, targetText)
	if targetHit == nil || strings.TrimSpace(targetHit.TableID) == "" {
		return nil, strongEvidence, adjustments, fmt.Errorf("reader 未返回目标表格单元格命中")
	}
	contextSummary, formatBasis := buildTableEvidence(reader, targetText)
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "update_table_cell",
		TargetText:           targetText,
		TableID:              targetHit.TableID,
		RowIndex:             targetHit.RowIndex,
		RowIndexSet:          true,
		CellIndex:            targetHit.CellIndex,
		CellIndexSet:         true,
		TableCell:            singleParagraphTableCellInput(newText, "TableBody"),
		ContextScope:         scope,
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		ReaderSessionID:      session.ID,
		FormatBasis:          formatBasis + "；最终单元格仍为单段表格正文，style_id=TableBody。",
		ExplorationSummary:   fmt.Sprintf("已锁定表格 %s 第 %d 行第 %d 列的目标值单元格，当前内容为 %s，仅需更新该格内容。", targetHit.TableID, targetHit.RowIndex, targetHit.CellIndex, targetText),
	})
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	if !tableContainsText(reopened, newText) {
		return nil, strongEvidence, adjustments, fmt.Errorf("表格中未找到替换后的值 %s", newText)
	}
	return result, strongEvidence, adjustments, nil
}

func runInsertTableParagraphAttempt(ctx *evalContext, artifactPath string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	targetText := "风险提示"
	newText := "需在回归前确认样式模板稳定。"
	reader, err := ctx.runner.runReader("", session.ID, targetText, "table")
	if err != nil {
		return nil, false, nil, err
	}
	targetHit := findReaderQueryHitContaining(reader, targetText)
	if targetHit == nil || strings.TrimSpace(targetHit.TableID) == "" {
		return nil, strongEvidence, nil, fmt.Errorf("reader 未返回风险提示单元格命中")
	}
	contextSummary, formatBasis := buildTableInsertEvidence(reader, targetText)
	adjustments := []string{}
	if strongEvidence {
		contextSummary += "，新增内容属于同一单元格中的说明正文，而不是该单元格标题。"
		formatBasis += "；明确要求沿用同一单元格正文 style_id=TableBody，字体=仿宋，字号=22。"
		adjustments = append(adjustments, "自动追加 TableBody 样式证据，驱动单元格内正文模板选择。")
	}

	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "update_table_cell",
		TargetText:           "当前为灰度验证阶段。",
		TableID:              targetHit.TableID,
		RowIndex:             targetHit.RowIndex,
		RowIndexSet:          true,
		CellIndex:            targetHit.CellIndex,
		CellIndexSet:         true,
		TableCell:            riskPromptTableCellInput(newText),
		ContextScope:         "table",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		ReaderSessionID:      session.ID,
		FormatBasis:          formatBasis + "；最终单元格需保持标题段 style_id=TableHeader，正文段与新增说明均使用 style_id=TableBody。",
		ExplorationSummary:   fmt.Sprintf("已锁定表格 %s 第 %d 行第 %d 列的风险提示单元格；最终整格内容应保留标题段“风险提示”和原正文，再在同格追加新说明。", targetHit.TableID, targetHit.RowIndex, targetHit.CellIndex),
	})
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, newText)
	if inserted == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("保存后未找到新增的单元格正文")
	}
	result.InsertedParagraph = inserted
	if !tableCellContainsTexts(reopened, targetText, newText) {
		return nil, strongEvidence, adjustments, fmt.Errorf("新增正文未插入到目标单元格中")
	}
	if normalizeStyleID(inserted.Style.StyleID) != "tablebody" {
		return nil, strongEvidence, adjustments, fmt.Errorf("单元格正文样式异常: style_id=%s", inserted.Style.StyleID)
	}
	insertedRunStyle := firstTextRunStyleValue(inserted)
	if insertedRunStyle.FontFamily != "仿宋" || insertedRunStyle.FontSizeHP != 22 {
		return nil, strongEvidence, adjustments, fmt.Errorf("单元格正文字体样式异常: font=%s size=%d", insertedRunStyle.FontFamily, insertedRunStyle.FontSizeHP)
	}
	return result, strongEvidence, adjustments, nil
}

func runInsertListExplanationAttempt(ctx *evalContext, artifactPath string, strongEvidence bool) (*executionResult, bool, []string, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, false, nil, err
	}
	defer ctx.client.closeSession(session.ID)

	targetText := "收集需求清单"
	newText := "本步骤需附带访谈纪要。"
	reader, err := ctx.runner.runReader("", session.ID, targetText, "paragraph")
	if err != nil {
		return nil, false, nil, err
	}
	contextSummary, formatBasis := buildListBodyInsertEvidence(reader, targetText)
	adjustments := []string{}
	if strongEvidence {
		contextSummary += "，新增内容属于步骤后的非编号说明正文。"
		formatBasis += "；明确要求沿用非编号正文 style_id=Normal，字体=宋体，字号=24，不得继承编号。"
		adjustments = append(adjustments, "自动追加非编号正文证据，避免说明段落继承列表编号。")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           targetText,
		NewText:              newText,
		Position:             "after",
		ContextScope:         "paragraph",
		EnableTrackChanges:   true,
		ReaderContextSummary: contextSummary,
		FormatBasis:          formatBasis,
	})
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, strongEvidence, adjustments, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, newText)
	if inserted == nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("保存后未找到新增说明正文")
	}
	result.InsertedParagraph = inserted
	if inserted.Style.Numbering != nil {
		return nil, strongEvidence, adjustments, fmt.Errorf("说明正文错误继承了编号: %+v", inserted.Style.Numbering)
	}
	if normalizeStyleID(inserted.Style.StyleID) != "normal" {
		return nil, strongEvidence, adjustments, fmt.Errorf("说明正文样式异常: style_id=%s", inserted.Style.StyleID)
	}
	return result, strongEvidence, adjustments, nil
}

func runDialogueMiddleNoteAttempt(ctx *evalContext, artifactPath string) (*executionResult, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "说明", "paragraph")
	if err != nil {
		return nil, err
	}
	targetHit := findReaderQueryHitContaining(reader, "人工复核")
	if targetHit == nil {
		return nil, fmt.Errorf("reader 未返回人工复核说明命中")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           "说明：",
		PreferredParagraphID: targetHit.ParagraphID,
		ReaderSessionID:      session.ID,
		NewText:              "说明（审阅项）：",
		ContextScope:         "paragraph",
		RetrievalQuery:       "说明",
		EnableTrackChanges:   true,
		EditIntent:           "用户原话：把中间那条说明改成审阅说明，内容别变。",
		Constraints:          "只改提到人工复核的那条说明，正文内容保持不变。",
		ReaderContextSummary: "真实对话语义：用户要改的是提到人工复核的那条中间说明，不新增段落，只调整该条说明前缀。",
		FormatBasis:          formatStyleBasis(targetHit.ParagraphStyle, targetHit.DominantTextStyle, "命中说明正文样式"),
		ExplorationSummary: fmt.Sprintf(
			"已围绕多条说明命中展开局部上下文，最终锁定提到人工复核的中间说明段，paragraph_id: %s；前后邻段分别是交付说明与第三方费用条款。",
			targetHit.ParagraphID,
		),
	})
	if err != nil {
		return nil, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	if findParagraphByText(reopened, "验证说明（审阅项）：当前版本仍需人工复核。") == nil {
		return nil, fmt.Errorf("未正确改写人工复核说明段")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return nil, fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "附录说明：本报告用于自动化回归。") == nil {
		return nil, fmt.Errorf("附录说明被误修改")
	}
	return result, nil
}

func runDialogueFuzzyFeeInsertAttempt(ctx *evalContext, artifactPath string) (*executionResult, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "费用", "paragraph")
	if err != nil {
		return nil, err
	}
	targetHit := findReaderQueryHitContaining(reader, "第三方费用")
	if targetHit == nil {
		return nil, fmt.Errorf("reader 未返回第三方费用条款命中")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "insert",
		TargetText:           "费用",
		PreferredParagraphID: targetHit.ParagraphID,
		ReaderSessionID:      session.ID,
		NewText:              "需在采购前完成审批备案。",
		Position:             "after",
		ContextScope:         "paragraph",
		RetrievalQuery:       "费用",
		EnableTrackChanges:   true,
		EditIntent:           "用户原话：把交付说明里第三方费用那条后面补一句，需要提前走采购审批。",
		Constraints:          "新增内容应插入到第三方费用条款后，不能插到差旅费用条款后面。",
		ReaderContextSummary: "真实对话语义：目标位于交付说明正文里，命中的是第三方费用那条，并在该条后补充采购审批要求。",
		FormatBasis:          formatStyleBasis(targetHit.ParagraphStyle, targetHit.DominantTextStyle, "交付说明正文样式"),
		ExplorationSummary: fmt.Sprintf(
			"已围绕多条费用命中展开局部上下文，最终锁定第三方费用条款，paragraph_id: %s；其相邻另一条费用说明是差旅费用，不应作为插入锚点。",
			targetHit.ParagraphID,
		),
	})
	if err != nil {
		return nil, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, "需在采购前完成审批备案。")
	if inserted == nil {
		return nil, fmt.Errorf("保存后未找到新增审批说明")
	}
	result.InsertedParagraph = inserted
	if paragraphImmediatelyAfterText(reopened, "第三方费用由甲方另行承担，并在采购前单独确认。") != inserted {
		return nil, fmt.Errorf("新增审批说明未插入到第三方费用条款后")
	}
	if paragraphImmediatelyAfterText(reopened, "差旅费用由乙方内部报销，不计入本次报价。") == inserted {
		return nil, fmt.Errorf("新增审批说明错误插入到差旅费用条款后")
	}
	return result, nil
}

func runDialogueTailRemarkAttempt(ctx *evalContext, artifactPath string) (*executionResult, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "说明", "paragraph")
	if err != nil {
		return nil, err
	}
	targetHit := findReaderQueryHitContaining(reader, "附录说明")
	if targetHit == nil {
		return nil, fmt.Errorf("reader 未返回附录说明命中")
	}
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "replace",
		TargetText:           "说明：",
		PreferredParagraphID: targetHit.ParagraphID,
		ReaderSessionID:      session.ID,
		NewText:              "说明（归档备注）：",
		ContextScope:         "paragraph",
		RetrievalQuery:       "说明",
		EnableTrackChanges:   true,
		EditIntent:           "用户原话：把最后那条备注改正式一点，标成归档备注。",
		Constraints:          "只改最后那条备注，不改交付说明和验证说明。",
		ReaderContextSummary: "真实对话语义：目标是文档最后那条备注，也就是附录说明，需要将前缀改成归档备注；这次只在同一段内扩写前缀，不新增换行或拆段。",
		FormatBasis:          formatStyleBasis(targetHit.ParagraphStyle, targetHit.DominantTextStyle, "附录说明正文样式") + "；参考附录说明前后的相邻正文后，确认本次仍保持单段附录说明，不改换行或断段。",
		ExplorationSummary: fmt.Sprintf(
			"已围绕多条说明命中展开局部上下文，最终锁定文档最后一条附录说明，paragraph_id: %s；前方仍有交付说明和验证说明，不能误改。",
			targetHit.ParagraphID,
		),
	})
	if err != nil {
		return nil, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	if findParagraphByText(reopened, "附录说明（归档备注）：本报告用于自动化回归。") == nil {
		return nil, fmt.Errorf("未正确改写附录备注段")
	}
	if findParagraphByText(reopened, "交付说明：交付物以验收版本为准。") == nil {
		return nil, fmt.Errorf("交付说明被误修改")
	}
	if findParagraphByText(reopened, "验证说明：当前版本仍需人工复核。") == nil {
		return nil, fmt.Errorf("验证说明被误修改")
	}
	return result, nil
}

func runDialogueRiskCellInsertAttempt(ctx *evalContext, artifactPath string) (*executionResult, error) {
	session, err := ctx.client.openDocument(ctx.inputDocPath)
	if err != nil {
		return nil, err
	}
	defer ctx.client.closeSession(session.ID)

	reader, err := ctx.runner.runReader("", session.ID, "风险", "table")
	if err != nil {
		return nil, err
	}
	targetHit := findReaderQueryHitContaining(reader, "风险提示")
	if targetHit == nil {
		return nil, fmt.Errorf("reader 未返回风险提示单元格命中")
	}
	contextSummary, formatBasis := buildTableInsertEvidence(reader, "风险")
	formatBasis += "；新增内容属于同一单元格中的说明正文，应沿用 style_id=TableBody，字体=仿宋，字号=22。"
	action, _, err := ctx.runner.runBridge(bridgeRequest{
		Action:               "update_table_cell",
		TargetText:           "当前为灰度验证阶段。",
		ContextScope:         "table",
		RetrievalQuery:       "风险",
		EnableTrackChanges:   true,
		EditIntent:           "用户原话：在风险提示那格下面补一句，上线前先确认灰度范围。",
		Constraints:          "新增内容必须插入到风险提示所在单元格，不要改动通过率，也不要改动右侧的需人工复核。",
		ReaderSessionID:      session.ID,
		ReaderContextSummary: contextSummary + "，真实对话中的‘那格’指向风险提示所在单元格。",
		FormatBasis:          formatBasis + "；最终单元格需保留标题段 style_id=TableHeader，正文段与新增说明均使用 style_id=TableBody。",
		ExplorationSummary: fmt.Sprintf(
			"已围绕表格命中展开 row_window 与 column_cells，最终锁定 table_id: %s 的风险提示单元格，focus_row_index: %d，focus_cell_index: %d；新增说明必须留在同一单元格正文区域。",
			targetHit.TableID,
			targetHit.RowIndex,
			targetHit.CellIndex,
		),
		PreferredParagraphID: targetHit.ParagraphID,
		TableID:              targetHit.TableID,
		RowIndex:             targetHit.RowIndex,
		RowIndexSet:          true,
		CellIndex:            targetHit.CellIndex,
		CellIndexSet:         true,
		TableCell:            riskPromptTableCellInput("上线前需先确认灰度范围。"),
	})
	if err != nil {
		return nil, err
	}
	result, err := executeEditorAction(ctx.client, session.ID, action)
	if err != nil {
		return nil, err
	}
	saved, reopened, err := saveAndReopen(ctx.client, session.ID, artifactPath)
	if err != nil {
		return nil, err
	}
	result.SavedResult = saved
	result.ReopenedDocument = reopened
	inserted := findParagraphByText(reopened, "上线前需先确认灰度范围。")
	if inserted == nil {
		return nil, fmt.Errorf("保存后未找到新增风险说明")
	}
	result.InsertedParagraph = inserted
	if !tableCellContainsTexts(reopened, "风险提示", "上线前需先确认灰度范围。") {
		return nil, fmt.Errorf("新增风险说明未插入到目标单元格")
	}
	if normalizeStyleID(inserted.Style.StyleID) != "tablebody" {
		return nil, fmt.Errorf("新增风险说明样式异常: style_id=%s", inserted.Style.StyleID)
	}
	if !tableContainsText(reopened, "85%") {
		return nil, fmt.Errorf("通过率单元格被误修改")
	}
	if !tableContainsText(reopened, "需人工复核") {
		return nil, fmt.Errorf("右侧风控提示被误修改")
	}
	return result, nil
}

func executeWorkflowBridgeAction(ctx *evalContext, sessionID string, request bridgeRequest) error {
	_, err := executeWorkflowBridgeActionWithResult(ctx, sessionID, request)
	return err
}

func executeWorkflowBridgeActionWithResult(ctx *evalContext, sessionID string, request bridgeRequest) (*executionResult, error) {
	action, _, err := ctx.runner.runBridge(request)
	if err != nil {
		return nil, err
	}
	return executeEditorAction(ctx.client, sessionID, action)
}

func buildRealDocumentFixture(outputPath string) error {
	document := &goword.Document{
		ID:       "agent_real_doc_eval",
		Name:     filepath.Base(outputPath),
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_title", "Auramate 智能编辑综合验证报告", goword.ParagraphStyle{StyleID: "Title", Alignment: "center", SpacingAfter: 200}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 32, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_intro", "本报告用于验证 Agent 对真实 Word 文档的结构理解、格式保持与自动化回归能力。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_h1", "第一章 项目背景", goword.ParagraphStyle{StyleID: "Heading1", SpacingBefore: 240, SpacingAfter: 120}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 28, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_body_1", "本项目旨在验证 Agent 对真实文档的理解与编辑能力。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_h2", "一、现状分析", goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_body_2", "当前系统已经支持全文检索、结构识别与样式继承。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_h2_steps", "二、实施步骤", goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_list_1", "收集需求清单", goword.ParagraphStyle{StyleID: "ListParagraph", LeftIndent: 720, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 0}}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_list_1_sub_1", "核对标题层级", goword.ParagraphStyle{StyleID: "ListParagraphLevel2", LeftIndent: 1440, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 1}}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_list_1_sub_2", "核对表格样式", goword.ParagraphStyle{StyleID: "ListParagraphLevel2", LeftIndent: 1440, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 1}}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_list_2", "完成样式评审", goword.ParagraphStyle{StyleID: "ListParagraph", LeftIndent: 720, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 0}}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_list_note", "以上步骤需保留审计记录。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_h2_delivery", "三、交付说明", goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_delivery_note", "交付说明：交付物以验收版本为准。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_note_mid", "验证说明：当前版本仍需人工复核。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_delivery_fee_travel", "差旅费用由乙方内部报销，不计入本次报价。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_delivery_fee_vendor", "第三方费用由甲方另行承担，并在采购前单独确认。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeTable, Table: &goword.Table{ID: "tbl_metrics", Rows: []goword.TableRow{
				{ID: "row_1", RepeatHeader: true, RepeatHeaderSet: true, CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "cell_1_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_1_1", "指标", goword.ParagraphStyle{StyleID: "TableHeader"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
					{ID: "cell_1_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_1_2", "当前值", goword.ParagraphStyle{StyleID: "TableHeader"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
				}},
				{ID: "row_2", Cells: []goword.TableCell{
					{ID: "cell_2_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_2_1", "通过率", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "cell_2_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_2_2", "85%", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "row_3", Cells: []goword.TableCell{
					{ID: "cell_3_1", Blocks: []goword.Block{
						{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_3_1", "风险提示", goword.ParagraphStyle{StyleID: "TableHeader"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})},
						{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_3_2", "当前为灰度验证阶段。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})},
					}},
					{ID: "cell_3_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_table_3_3", "需人工复核", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
			}}},
			{Type: goword.BlockTypeTable, Table: &goword.Table{ID: "tbl_release_batches", Rows: []goword.TableRow{
				{ID: "release_row_1", RepeatHeader: true, RepeatHeaderSet: true, CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_1_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_1_1", "发布批次", goword.ParagraphStyle{StyleID: "TableHeader"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
					{ID: "release_cell_1_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_1_2", "要求", goword.ParagraphStyle{StyleID: "TableHeader"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
				}},
				{ID: "release_row_2", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_2_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_2_1", "灰度批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_2_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_2_2", "需关注灰度开关与监控告警。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_3", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_3_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_3_1", "正式批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_3_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_3_2", "需附签发邮件与发布时间窗。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_4", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_4_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_4_1", "回滚批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_4_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_4_2", "需保留回滚脚本与审批记录。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_5", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_5_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_5_1", "联调批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_5_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_5_2", "需确认接口白名单与联调窗口。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_6", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_6_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_6_1", "预演批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_6_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_6_2", "需同步演练脚本与现场签到。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_7", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_7_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_7_1", "割接批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_7_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_7_2", "需确认停机窗口与值班通讯录。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_8", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_8_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_8_1", "监控校验批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_8_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_8_2", "需核对告警阈值与看板截图。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_9", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_9_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_9_1", "正式扩容批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_9_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_9_2", "需附扩容清单与资源核验单。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_10", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_10_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_10_1", "夜间巡检批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_10_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_10_2", "需同步巡检路径与值守截图。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_11", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_11_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_11_1", "渠道同步批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_11_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_11_2", "需确认渠道侧缓存刷新时间。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_12", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_12_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_12_1", "回归复测批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_12_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_12_2", "需附复测结论与缺陷闭环说明。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_13", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_13_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_13_1", "灾备演练批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_13_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_13_2", "需演练双活切换与值班交接。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
				{ID: "release_row_14", CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "release_cell_14_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_14_1", "归档核验批次", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
					{ID: "release_cell_14_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_release_14_2", "需附归档清单与审计留痕。", goword.ParagraphStyle{StyleID: "TableBody"}, goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22})}}},
				}},
			}}},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_tail", "附录说明：本报告用于自动化回归。", goword.ParagraphStyle{StyleID: "Normal", SpacingBefore: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
		},
		Meta: goword.DocumentMeta{
			Format: "docx",
			SectionLayout: goword.SectionLayout{
				TitlePage:    true,
				TitlePageSet: true,
			},
			HeaderFooters: []goword.HeaderFooterPart{
				{
					Kind:    "header",
					Variant: "first",
					Blocks: []goword.Block{{
						Type:      goword.BlockTypeParagraph,
						Paragraph: paragraph("p_header_first", "Auramate 首页资料", goword.ParagraphStyle{StyleID: "Header", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 20}),
					}},
				},
				{
					Kind:    "header",
					Variant: "default",
					Blocks: []goword.Block{{
						Type:      goword.BlockTypeParagraph,
						Paragraph: paragraph("p_header_default", "Auramate 内部资料", goword.ParagraphStyle{StyleID: "Header", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 20}),
					}},
				},
				{
					Kind:    "footer",
					Variant: "default",
					Blocks: []goword.Block{{
						Type:      goword.BlockTypeParagraph,
						Paragraph: paragraph("p_footer_default", "第 1 页", goword.ParagraphStyle{StyleID: "Footer", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 18}),
					}},
				},
			},
			DefaultParagraphStyle: &goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360},
			DefaultTextStyle:      &goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24},
			Theme:                 &goword.DocumentTheme{Fonts: map[string]string{"minorEastAsia": "宋体", "minorAscii": "Calibri", "majorEastAsia": "黑体", "majorAscii": "Calibri"}},
			Numbering: []goword.NumberingDefinition{{
				NumID:      "9",
				AbstractID: "9",
				Levels: []goword.NumberingLevel{
					{
						Level:  0,
						Start:  1,
						Format: "decimal",
						Text:   "%1.",
						Paragraph: goword.ParagraphStyle{
							LeftIndent:      720,
							FirstLineIndent: -360,
						},
						TextStyle: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24},
					},
					{
						Level:  1,
						Start:  1,
						Format: "decimal",
						Text:   "%1.%2",
						Paragraph: goword.ParagraphStyle{
							LeftIndent:      1440,
							FirstLineIndent: -360,
						},
						TextStyle: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24},
					},
				},
			}},
			Styles: []goword.DocumentStyle{
				{ID: "Normal", Name: "Normal", Type: "paragraph", IsDefault: true, Paragraph: goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
				{ID: "Header", Name: "header", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Header", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 20}},
				{ID: "Footer", Name: "footer", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Footer", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 18}},
				{ID: "Title", Name: "Title", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Title", Alignment: "center", SpacingAfter: 200}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 32, Bold: true, BoldSet: true}},
				{ID: "Heading1", Name: "heading 1", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Heading1", SpacingBefore: 240, SpacingAfter: 120}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 28, Bold: true, BoldSet: true}},
				{ID: "Heading2", Name: "heading 2", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true}},
				{ID: "ListParagraph", Name: "list paragraph", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "ListParagraph", LeftIndent: 720, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 0}}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
				{ID: "ListParagraphLevel2", Name: "list paragraph level 2", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "ListParagraphLevel2", LeftIndent: 1440, FirstLineIndent: -360, Numbering: &goword.NumberingRef{NumID: "9", Level: 1}}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
				{ID: "TableHeader", Name: "table header", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "TableHeader", SpacingAfter: 60}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true}},
				{ID: "TableBody", Name: "table body", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "TableBody", SpacingAfter: 40, LineSpacing: 300}, Text: goword.TextStyle{FontFamily: "仿宋", FontSizeHP: 22}},
			},
		},
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return goword.SaveDOCX(document, outputPath)
}

func buildBoldLabelInsertFixture(outputPath string) error {
	document := &goword.Document{
		ID:       "agent_real_doc_eval_bold_label_insert",
		Name:     filepath.Base(outputPath),
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_title", "Auramate 标签段正文插入验证", goword.ParagraphStyle{StyleID: "Title", Alignment: "center", SpacingAfter: 200}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 32, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_intro", "本样本用于验证短加粗标签段后新增正文时的样式继承与空格保留。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_label", "基本原则", goword.ParagraphStyle{StyleID: "Normal", SpacingBefore: 80, SpacingAfter: 40}, goword.TextStyle{FontFamily: "Microsoft YaHei", FontSizeHP: 28, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_body", "坚持问题导向，确保制度可执行。", goword.ParagraphStyle{StyleID: "Normal", SpacingBefore: 120, SpacingAfter: 80, FirstLineIndent: 420, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24, Color: "1F1F1F"})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_tail", "执行要求：新增内容需与正文保持一致。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
		},
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return goword.SaveDOCX(document, outputPath)
}

func buildTableRowInsertFixture(outputPath string) error {
	document := &goword.Document{
		ID:       "agent_real_doc_eval_table_row_insert",
		Name:     filepath.Base(outputPath),
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_title", "程序员远程工作管理与考核标准", goword.ParagraphStyle{StyleID: "Title", Alignment: "center", SpacingAfter: 200}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 30, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_heading", "一、考核维度与权重", goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true})},
			{Type: goword.BlockTypeTable, Table: &goword.Table{ID: "tbl_203", Rows: []goword.TableRow{
				{ID: "row_metrics_header", RepeatHeader: true, RepeatHeaderSet: true, CantSplit: true, CantSplitSet: true, Cells: []goword.TableCell{
					{ID: "cell_metrics_header_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_header_1", "考核维度", goword.ParagraphStyle{StyleID: "TableHeader", Alignment: "center"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
					{ID: "cell_metrics_header_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_header_2", "权重", goword.ParagraphStyle{StyleID: "TableHeader", Alignment: "center"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
					{ID: "cell_metrics_header_3", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_header_3", "考核周期", goword.ParagraphStyle{StyleID: "TableHeader", Alignment: "center"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
					{ID: "cell_metrics_header_4", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_header_4", "考核方式", goword.ParagraphStyle{StyleID: "TableHeader", Alignment: "center"}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true})}}},
				}},
				{ID: "row_metrics_1", Cells: []goword.TableCell{
					{ID: "cell_metrics_1_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_1_1", "工作业绩", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_1_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_1_2", "50%", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_1_3", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_1_3", "月度", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_1_4", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_1_4", "任务完成率、交付质量", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
				}},
				{ID: "row_metrics_2", Cells: []goword.TableCell{
					{ID: "cell_metrics_2_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_2_1", "工作能力", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_2_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_2_2", "25%", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_2_3", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_2_3", "季度", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_2_4", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_2_4", "技术能力、问题解决", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
				}},
				{ID: "row_metrics_3", Cells: []goword.TableCell{
					{ID: "cell_metrics_3_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_3_1", "工作态度", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_3_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_3_2", "15%", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_3_3", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_3_3", "月度", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_3_4", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_3_4", "响应速度、协作精神", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
				}},
				{ID: "row_metrics_4", Cells: []goword.TableCell{
					{ID: "cell_metrics_4_1", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_4_1", "团队贡献", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_4_2", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_4_2", "10%", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_4_3", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_4_3", "季度", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
					{ID: "cell_metrics_4_4", Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_metrics_4_4", "知识分享、代码 Review", goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center"}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})}}},
				}},
			}}},
		},
		Meta: goword.DocumentMeta{
			Format:                "docx",
			DefaultParagraphStyle: &goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360},
			DefaultTextStyle:      &goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24},
			Theme:                 &goword.DocumentTheme{Fonts: map[string]string{"minorEastAsia": "宋体", "minorAscii": "Calibri", "majorEastAsia": "黑体", "majorAscii": "Calibri"}},
			Styles: []goword.DocumentStyle{
				{ID: "Normal", Name: "Normal", Type: "paragraph", IsDefault: true, Paragraph: goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
				{ID: "Title", Name: "Title", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Title", Alignment: "center", SpacingAfter: 200}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 30, Bold: true, BoldSet: true}},
				{ID: "Heading2", Name: "heading 2", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true}},
				{ID: "TableHeader", Name: "table header", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "TableHeader", Alignment: "center", SpacingAfter: 40}, Text: goword.TextStyle{FontFamily: "黑体", FontSizeHP: 24, Bold: true, BoldSet: true}},
				{ID: "TableBody", Name: "table body", Type: "paragraph", BasedOn: "Normal", Paragraph: goword.ParagraphStyle{StyleID: "TableBody", Alignment: "center", SpacingAfter: 40, LineSpacing: 300}, Text: goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24}},
			},
		},
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return goword.SaveDOCX(document, outputPath)
}

func buildHeaderFooterVariantFixture(outputPath string) error {
	if err := buildRealDocumentFixture(outputPath); err != nil {
		return err
	}
	document, err := goword.OpenDOCX(outputPath)
	if err != nil {
		return err
	}
	document.Name = filepath.Base(outputPath)
	document.Meta.HeaderFooters = append(document.Meta.HeaderFooters, goword.HeaderFooterPart{
		Kind:    "footer",
		Variant: "even",
		Blocks: []goword.Block{{
			Type:      goword.BlockTypeParagraph,
			Paragraph: paragraph("p_footer_even", "偶数页页脚", goword.ParagraphStyle{StyleID: "Footer", Alignment: "center", SpacingAfter: 0, LineSpacing: 240}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 18}),
		}},
	})
	return goword.SaveDOCX(document, outputPath)
}

func buildMultiSectionFixture(outputPath string) error {
	parts := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/word/header2.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId10" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/><Relationship Id="rId11" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header2.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId10"/><w:type w:val="nextPage"/><w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/><w:pgMar w:top="1440" w:right="1200" w:bottom="1440" w:left="1200"/></w:sectPr></w:pPr><w:r><w:t>第一节末段</w:t></w:r></w:p><w:p><w:r><w:t>第二节正文</w:t></w:r></w:p><w:sectPr><w:headerReference w:type="default" r:id="rId11"/><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800"/></w:sectPr></w:body></w:document>`,
		"word/header1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>第一节页眉</w:t></w:r></w:p></w:hdr>`,
		"word/header2.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>第二节页眉</w:t></w:r></w:p></w:hdr>`,
	}
	return writeDOCXParts(outputPath, parts)
}

func buildLargeDocumentFixture(outputPath string) error {
	if err := buildRealDocumentFixture(outputPath); err != nil {
		return err
	}
	document, err := goword.OpenDOCX(outputPath)
	if err != nil {
		return err
	}
	document.Name = filepath.Base(outputPath)
	for index := 1; index <= 80; index++ {
		sectionID := fmt.Sprintf("%03d", index)
		document.Blocks = append(document.Blocks,
			goword.Block{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_long_h_"+sectionID, "附录节 "+sectionID+" 长文档稳定性验证", goword.ParagraphStyle{StyleID: "Heading2", SpacingBefore: 180, SpacingAfter: 100}, goword.TextStyle{FontFamily: "黑体", FontSizeHP: 26, Bold: true, BoldSet: true})},
			goword.Block{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_long_verdict_"+sectionID, "第 "+sectionID+" 节验证结论：当前批次需要人工复核。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			goword.Block{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_long_body_a_"+sectionID, "本节用于验证长文档条件下的局部检索、稳定改写与保存复开能力，要求在不扩大无关上下文的前提下，仍能锁定唯一锚点并保持样式一致。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			goword.Block{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_long_body_b_"+sectionID, "该段重复提供较长正文，用于拉高布局页数，并观察 reader 在大样本上是否仍按最小范围检索，而不是退化成全文无差别展开。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
			goword.Block{Type: goword.BlockTypeParagraph, Paragraph: paragraph("p_long_body_c_"+sectionID, "如果真实模型在长文档里无法维持精确锚点，本基线会直接暴露出误命中、错段替换或跨节串改问题，因此该样本同时覆盖正确性与规模稳定性。", goword.ParagraphStyle{StyleID: "Normal", SpacingAfter: 120, LineSpacing: 360}, goword.TextStyle{FontFamily: "宋体", FontSizeHP: 24})},
		)
	}
	return goword.SaveDOCX(document, outputPath)
}

func buildOOXMLComplexStructureFixture(outputPath string) error {
	parts := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/footnotes.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"/><Override PartName="/word/endnotes.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"/><Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdHyper1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com" TargetMode="External"/><Relationship Id="rIdFootnotes" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footnotes" Target="footnotes.xml"/><Relationship Id="rIdEndnotes" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/endnotes" Target="endnotes.xml"/><Relationship Id="rIdComments" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:hyperlink r:id="rIdHyper1"><w:r><w:t>链接</w:t></w:r></w:hyperlink><w:bookmarkStart w:id="0" w:name="bm1"/><w:r><w:t>书签文本</w:t></w:r><w:bookmarkEnd w:id="0"/></w:p><w:p><w:fldSimple w:instr="DATE"><w:r><w:t>2026-01-01</w:t></w:r></w:fldSimple></w:p><w:p><w:r><w:fldChar w:fldCharType="begin"/><w:instrText xml:space="preserve"> REF bm1 </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>书签引用</w:t><w:fldChar w:fldCharType="end"/></w:r></w:p><w:p><w:r><w:t>脚注</w:t><w:footnoteReference w:id="2"/></w:r><w:r><w:t>尾注</w:t><w:endnoteReference w:id="2"/></w:r><w:commentRangeStart w:id="0"/><w:r><w:t>批注锚点</w:t><w:commentReference w:id="0"/></w:r><w:commentRangeEnd w:id="0"/></w:p><w:p><w:ins w:id="1" w:author="tester"><w:r><w:t>新增文本</w:t></w:r></w:ins><w:del w:id="2" w:author="tester"><w:r><w:delText>删除文本</w:delText></w:r></w:del></w:p><w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>跨列标题</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>外层单元格</w:t></w:r></w:p><w:tbl><w:tblPr><w:tblW w:w="3000" w:type="pct"/></w:tblPr><w:tblGrid><w:gridCol w:w="3000"/></w:tblGrid><w:tr><w:tc><w:p><w:r><w:t>内层表格</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc><w:tc><w:p><w:r><w:t>右侧单元格</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
		"word/footnotes.xml":           `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote><w:footnote w:id="2"><w:p><w:r><w:t>脚注内容</w:t></w:r></w:p></w:footnote></w:footnotes>`,
		"word/endnotes.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:endnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:endnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:endnote><w:endnote w:id="2"><w:p><w:r><w:t>尾注内容</w:t></w:r></w:p></w:endnote></w:endnotes>`,
		"word/comments.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:comment w:id="0" w:author="tester"><w:p><w:r><w:t>批注内容</w:t></w:r></w:p></w:comment></w:comments>`,
	}
	return writeDOCXParts(outputPath, parts)
}

func buildOOXMLImageFixture(outputPath string) error {
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/><Relationship Id="rIdImage2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><w:body><w:p><w:r><w:drawing><wp:inline><wp:docPr id="1" name="内联图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="2" name="浮动图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:link="rIdImage2"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r><w:r><w:pict><v:shape type="#_x0000_t75"><v:imagedata r:id="rIdImage1" o:title="旧式图片"/></v:shape></w:pict></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="3" name="文本框形状"/><a:graphic><a:graphicData uri="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"><wps:wsp><wps:txbx><w:txbxContent><w:p><w:r><w:t>文本框内容</w:t></w:r></w:p></w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNGBytes(),
		"word/media/image2.png":        minimalPNGBytes(),
	}
	return writeDOCXBinaryParts(outputPath, parts)
}

func buildDrawingObjectsEditFixture(outputPath string) error {
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/><Relationship Id="rIdImage2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><w:body><w:p><w:r><w:t>对象区提示：当前版本仅供内部评审。</w:t></w:r></w:p><w:p><w:r><w:t>对象区说明：后续文本框和形状对象必须原样保留。</w:t></w:r></w:p><w:p><w:r><w:t>对象锚点正文：</w:t></w:r><w:r><w:drawing><wp:inline><wp:docPr id="1" name="内联图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="2" name="浮动图"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:blipFill><a:blip r:link="rIdImage2"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r><w:r><w:pict><v:shape type="#_x0000_t75"><v:imagedata r:id="rIdImage1" o:title="旧式图片"/></v:shape></w:pict></w:r><w:r><w:drawing><wp:anchor><wp:docPr id="3" name="文本框形状"/><a:graphic><a:graphicData uri="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"><wps:wsp><wps:txbx><w:txbxContent><w:p><w:r><w:t>文本框内容</w:t></w:r></w:p></w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r><w:r><w:t>对象层不应被破坏。</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        minimalPNGBytes(),
		"word/media/image2.png":        minimalPNGBytes(),
	}
	return writeDOCXBinaryParts(outputPath, parts)
}

func writeDOCXParts(outputPath string, parts map[string]string) error {
	binaryParts := make(map[string][]byte, len(parts))
	for name, content := range parts {
		binaryParts[name] = []byte(content)
	}
	return writeDOCXBinaryParts(outputPath, binaryParts)
}

func writeDOCXBinaryParts(outputPath string, parts map[string][]byte) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for name, content := range parts {
		entryWriter, err := writer.Create(name)
		if err != nil {
			writer.Close()
			return err
		}
		if _, err := entryWriter.Write(content); err != nil {
			writer.Close()
			return err
		}
	}
	return writer.Close()
}

func copyBinaryFile(sourcePath, outputPath string) error {
	if strings.TrimSpace(sourcePath) == "" {
		return fmt.Errorf("sourcePath 不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return nil
}

func minimalPNGBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D, 0xB1, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}
}

func docxEntryContains(docxPath, entryName, snippet string) (bool, error) {
	reader, err := zip.OpenReader(docxPath)
	if err != nil {
		return false, fmt.Errorf("打开 docx 失败: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != entryName {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return false, fmt.Errorf("打开 zip entry 失败: %w", err)
		}
		content, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
			return false, fmt.Errorf("读取 zip entry 失败: %w", readErr)
		}
		return strings.Contains(string(content), snippet), nil
	}
	return false, fmt.Errorf("docx entry 不存在: %s", entryName)
}

func paragraph(id, text string, style goword.ParagraphStyle, runStyle goword.TextStyle) *goword.Paragraph {
	return &goword.Paragraph{
		ID:    id,
		Style: style,
		Runs:  []goword.Run{{ID: id + "_run", Text: text, Style: runStyle}},
	}
}

func newMiniGoWordServer(rootDir string) *miniGoWordServer {
	service := goword.NewService(rootDir)
	mux := http.NewServeMux()
	server := &miniGoWordServer{service: service}
	mux.HandleFunc("/v1/goword/open", server.handleOpen)
	mux.HandleFunc("/v1/goword/document", server.handleDocument)
	mux.HandleFunc("/v1/goword/search", server.handleSearch)
	mux.HandleFunc("/v1/goword/apply", server.handleApply)
	mux.HandleFunc("/v1/goword/layout", server.handleLayout)
	mux.HandleFunc("/v1/goword/save", server.handleSave)
	mux.HandleFunc("/v1/goword/close", server.handleClose)
	server.server = httptest.NewServer(mux)
	return server
}

func (s *miniGoWordServer) Close() {
	if s.server != nil {
		s.server.Close()
	}
}

func (s *miniGoWordServer) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	session, err := s.service.OpenDocument(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, session)
}

func (s *miniGoWordServer) handleDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	session, err := s.service.Snapshot(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, session)
}

func (s *miniGoWordServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Query     string `json:"query"`
		Scope     string `json:"scope"`
		Mode      string `json:"mode"`
		Limit     int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.service.Search(req.SessionID, goword.SearchRequest{Query: req.Query, Scope: req.Scope, Mode: req.Mode, Limit: req.Limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result)
}

func (s *miniGoWordServer) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID  string             `json:"session_id"`
		Operations []goword.Operation `json:"operations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.service.Apply(req.SessionID, req.Operations)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result)
}

func (s *miniGoWordServer) handleLayout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string               `json:"session_id"`
		Options   goword.LayoutOptions `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	layout, err := s.service.LayoutSnapshot(req.SessionID, req.Options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, layout)
}

func (s *miniGoWordServer) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.service.Save(req.SessionID, req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result)
}

func (s *miniGoWordServer) handleClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.service.Close(req.SessionID)
	writeJSON(w, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func newAPIClient(baseURL string) *apiClient {
	return &apiClient{baseURL: baseURL, http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *apiClient) openDocument(path string) (*goword.Session, error) {
	var session goword.Session
	err := c.doJSON(http.MethodPost, "/v1/goword/open", map[string]string{"path": path}, &session)
	return &session, err
}

func (c *apiClient) getDocument(sessionID string) (*goword.Session, error) {
	var session goword.Session
	err := c.doJSON(http.MethodGet, "/v1/goword/document?session_id="+urlEscape(sessionID), nil, &session)
	return &session, err
}

func (c *apiClient) layoutDocument(sessionID string, options goword.LayoutOptions) (*goword.DocumentLayout, error) {
	var layout goword.DocumentLayout
	err := c.doJSON(http.MethodPost, "/v1/goword/layout", map[string]any{
		"session_id": sessionID,
		"options":    options,
	}, &layout)
	return &layout, err
}

func (c *apiClient) searchDocument(sessionID, query, scope string, limit int) (*goword.SearchResult, error) {
	var result goword.SearchResult
	err := c.doJSON(http.MethodPost, "/v1/goword/search", map[string]any{
		"session_id": sessionID,
		"query":      query,
		"scope":      scope,
		"mode":       "contains",
		"limit":      limit,
	}, &result)
	return &result, err
}

func (c *apiClient) applyOperations(sessionID string, operations []goword.Operation) (*applyResponse, error) {
	var result applyResponse
	err := c.doJSON(http.MethodPost, "/v1/goword/apply", map[string]any{"session_id": sessionID, "operations": operations}, &result)
	return &result, err
}

func (c *apiClient) saveDocument(sessionID, path string) (*goword.SaveResult, error) {
	var result goword.SaveResult
	err := c.doJSON(http.MethodPost, "/v1/goword/save", map[string]string{"session_id": sessionID, "path": path}, &result)
	return &result, err
}

func (c *apiClient) closeSession(sessionID string) error {
	return c.doJSON(http.MethodPost, "/v1/goword/close", map[string]string{"session_id": sessionID}, nil)
}

func (c *apiClient) doJSON(method, path string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return errors.New(strings.TrimSpace(string(content)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}

func (r *skillRunner) runReader(path, sessionID, query, scope string) (*wordReaderResponse, error) {
	params := map[string]any{}
	if strings.TrimSpace(path) != "" {
		params["path"] = path
	}
	if strings.TrimSpace(sessionID) != "" {
		params["session_id"] = sessionID
	}
	if strings.TrimSpace(query) != "" {
		params["query"] = query
	}
	if strings.TrimSpace(scope) != "" {
		params["context_scope"] = scope
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("reader 参数序列化失败: %w", err)
	}
	toolResponse, err := r.readerTool.Run(context.Background(), toolruntime.ToolCall{
		Name:  toolruntime.WordReaderToolName,
		Input: string(encoded),
	})
	if err != nil {
		return nil, fmt.Errorf("reader 执行失败: %w", err)
	}
	if toolResponse.IsError {
		return nil, fmt.Errorf("reader 执行失败: %s", strings.TrimSpace(toolResponse.Content))
	}
	var readerResponse wordReaderResponse
	if err := json.Unmarshal([]byte(toolResponse.Content), &readerResponse); err != nil {
		return nil, fmt.Errorf("解析 reader 输出失败: %w", err)
	}
	if readerResponse.Status == "error" {
		return nil, errors.New(readerResponse.Message)
	}
	return &readerResponse, nil
}

func (r *skillRunner) runBridge(request bridgeRequest) (*editorActionInput, string, error) {
	params := map[string]any{
		"action":                 request.Action,
		"target_text":            request.TargetText,
		"new_text":               request.NewText,
		"position":               defaultString(request.Position, "after"),
		"context_scope":          defaultString(request.ContextScope, "paragraph"),
		"edit_intent":            defaultString(request.EditIntent, "精准修改"),
		"constraints":            request.Constraints,
		"retrieval_query":        defaultString(request.RetrievalQuery, request.TargetText),
		"enable_track_changes":   request.EnableTrackChanges,
		"preview_only":           request.PreviewOnly,
		"reader_session_id":      request.ReaderSessionID,
		"reader_context_summary": request.ReaderContextSummary,
		"format_basis":           request.FormatBasis,
		"exploration_summary":    request.ExplorationSummary,
	}
	if strings.TrimSpace(request.PreferredParagraphID) != "" {
		params["preferred_paragraph_id"] = request.PreferredParagraphID
	}
	if strings.TrimSpace(request.TableID) != "" {
		params["table_id"] = request.TableID
	}
	if (request.Action == "insert_table_row" || request.Action == "delete_table_row" || request.Action == "update_table_cell") && request.RowIndexSet {
		params["row_index"] = request.RowIndex
	}
	if (request.Action == "insert_table_column" || request.Action == "delete_table_column" || request.Action == "update_table_cell") && request.CellIndexSet {
		params["cell_index"] = request.CellIndex
	}
	if hasStylePatch(request.StylePatch) {
		params["style_patch"] = request.StylePatch
	}
	if hasParagraphStylePatch(request.ParagraphStylePatch) {
		params["paragraph_style_patch"] = request.ParagraphStylePatch
	}
	if request.TableSpec != nil {
		params["table_spec"] = request.TableSpec
	}
	if request.TableRow != nil {
		params["table_row"] = request.TableRow
	}
	if request.TableColumn != nil {
		params["table_column"] = request.TableColumn
	}
	if request.TableCell != nil {
		params["table_cell"] = request.TableCell
	}
	if hasHeaderFooterInput(request.HeaderFooter) {
		params["header_footer"] = request.HeaderFooter
	}
	if hasSectionLayoutPatch(request.SectionLayoutPatch) {
		params["section_layout_patch"] = request.SectionLayoutPatch
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, "", fmt.Errorf("序列化 copilot 参数失败: %w", err)
	}
	response, err := r.copilotTool.Run(context.Background(), toolruntime.ToolCall{
		Name:  toolruntime.WordCopilotToolName,
		Input: string(encoded),
	})
	if err != nil {
		return nil, "", err
	}
	if response.IsError {
		return nil, response.Content, errors.New(strings.TrimSpace(response.Content))
	}
	action, err := parseBridgeSignal(response.Content)
	if err != nil {
		return nil, response.Content, err
	}
	return action, response.Content, nil
}

func runCommand(executable string, args []string, extraEnv map[string]string) (string, string, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func parseBridgeSignal(output string) (*editorActionInput, error) {
	actions, err := parseBridgeSignals(output)
	if err != nil {
		return nil, err
	}
	return actions[len(actions)-1], nil
}

func parseBridgeSignals(output string) ([]*editorActionInput, error) {
	actions, _, err := extractBridgeSignalsDetailed(output)
	return actions, err
}

func appendUniqueActions(existing []*editorActionInput, incoming []*editorActionInput) []*editorActionInput {
	if len(incoming) == 0 {
		return existing
	}
	result := append([]*editorActionInput{}, existing...)
	seen := make(map[string]struct{}, len(existing))
	for _, action := range existing {
		if action == nil {
			continue
		}
		seen[actionSignature(action)] = struct{}{}
	}
	for _, action := range incoming {
		if action == nil {
			continue
		}
		signature := actionSignature(action)
		if _, exists := seen[signature]; exists {
			continue
		}
		seen[signature] = struct{}{}
		result = append(result, action)
	}
	return result
}

func extractBridgeSignalsDetailed(output string) ([]*editorActionInput, []map[string]any, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil, fmt.Errorf("bridge 输出中未找到 word_editor_action 信号")
	}
	lines := strings.Split(trimmed, "\n")
	actions := make([]*editorActionInput, 0, len(lines))
	rawSignals := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		candidate := strings.TrimSpace(line)
		if !strings.HasPrefix(candidate, "__WORD_EDITOR_SIG__:") {
			continue
		}
		payloads, _ := wordserver.ExtractWordEditorSignals(candidate)
		if len(payloads) == 0 || payloads[0] == nil {
			continue
		}
		encoded, err := json.Marshal(payloads[0])
		if err != nil {
			return nil, nil, fmt.Errorf("序列化后端 signal payload 失败: %w", err)
		}
		var action editorActionInput
		if err := json.Unmarshal(encoded, &action); err != nil {
			return nil, nil, fmt.Errorf("解析后端 signal payload 失败: %w", err)
		}
		rawPayload := strings.TrimSpace(strings.TrimPrefix(candidate, "__WORD_EDITOR_SIG__:"))
		var rawEnvelope map[string]any
		if err := json.Unmarshal([]byte(rawPayload), &rawEnvelope); err != nil {
			return nil, nil, fmt.Errorf("解析原始 signal payload 失败: %w", err)
		}
		rawData := rawEnvelope
		if rawAction, ok := rawEnvelope["action"]; !ok || strings.TrimSpace(fmt.Sprintf("%v", rawAction)) == "" {
			if data, ok := rawEnvelope["data"].(map[string]any); ok {
				rawData = data
			}
		}
		actions = append(actions, &action)
		rawSignals = append(rawSignals, rawData)
	}
	if len(actions) == 0 {
		return nil, nil, fmt.Errorf("bridge 输出中未找到可执行的 word_editor_action 信号")
	}
	return actions, rawSignals, nil
}

func appendUniqueActionsWithRaw(existing []*editorActionInput, existingRaw []map[string]any, incoming []*editorActionInput, incomingRaw []map[string]any) ([]*editorActionInput, []map[string]any) {
	if len(incoming) == 0 {
		return existing, existingRaw
	}
	resultActions := append([]*editorActionInput{}, existing...)
	resultRaw := append([]map[string]any{}, existingRaw...)
	seen := make(map[string]struct{}, len(existing))
	for _, action := range existing {
		if action == nil {
			continue
		}
		seen[actionSignature(action)] = struct{}{}
	}
	for index, action := range incoming {
		if action == nil {
			continue
		}
		signature := actionSignature(action)
		if _, exists := seen[signature]; exists {
			continue
		}
		seen[signature] = struct{}{}
		resultActions = append(resultActions, action)
		if index < len(incomingRaw) {
			resultRaw = append(resultRaw, incomingRaw[index])
		} else {
			resultRaw = append(resultRaw, nil)
		}
	}
	return resultActions, resultRaw
}

func formatBasisTooGeneric(formatBasis string) bool {
	normalized := strings.TrimSpace(formatBasis)
	if normalized == "" {
		return false
	}
	lowered := strings.ToLower(normalized)
	hasGenericCue := false
	for _, cue := range llmGenericFormatBasisCues {
		if strings.Contains(normalized, cue) {
			hasGenericCue = true
			break
		}
	}
	if !hasGenericCue {
		return false
	}
	for _, cue := range llmConcreteFormatBasisCues {
		if strings.Contains(lowered, cue) || strings.Contains(normalized, cue) {
			return false
		}
	}
	return true
}

func actionNeedsReaderSessionID(action *editorActionInput) bool {
	if action == nil {
		return false
	}
	if strings.TrimSpace(action.PreferredParagraphID) != "" || strings.TrimSpace(action.TableID) != "" || strings.TrimSpace(action.ExplorationSummary) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(action.Action)) {
	case "insert_table_row", "delete_table_row", "insert_table_column", "delete_table_column", "update_table_cell":
		return true
	default:
		return false
	}
}

func rawSignalHasField(raw map[string]any, field string) bool {
	if raw == nil {
		return false
	}
	_, ok := raw[field]
	return ok
}

func validateLLMActionContracts(result *llmDialogueRunResult) error {
	if result == nil {
		return nil
	}
	for index, action := range result.Actions {
		if action == nil {
			continue
		}
		if formatBasisTooGeneric(action.FormatBasis) {
			return fmt.Errorf("第 %d 条动作的 format_basis 过于空泛，缺少具体样式或 layout 依据", index+1)
		}
		if actionNeedsReaderSessionID(action) && strings.TrimSpace(action.ReaderSessionID) == "" {
			return fmt.Errorf("第 %d 条动作缺少 reader_session_id：已依赖精确锚点、表格坐标或 exploration 结果", index+1)
		}
		raw := map[string]any(nil)
		if index < len(result.RawActions) {
			raw = result.RawActions[index]
		}
		switch strings.ToLower(strings.TrimSpace(action.Action)) {
		case "insert_table_row", "delete_table_row":
			if !rawSignalHasField(raw, "row_index") {
				return fmt.Errorf("第 %d 条表格行动作缺少显式 row_index", index+1)
			}
		case "insert_table_column", "delete_table_column":
			if !rawSignalHasField(raw, "cell_index") {
				return fmt.Errorf("第 %d 条表格列动作缺少显式 cell_index", index+1)
			}
		case "update_table_cell":
			if !rawSignalHasField(raw, "row_index") || !rawSignalHasField(raw, "cell_index") {
				return fmt.Errorf("第 %d 条 update_table_cell 缺少显式 row_index/cell_index", index+1)
			}
		}
	}
	return nil
}

func actionSignature(action *editorActionInput) string {
	if action == nil {
		return ""
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return fmt.Sprintf("%s|%s|%s|%d|%d", action.Action, action.TargetText, action.TableID, action.RowIndex, action.CellIndex)
	}
	return string(encoded)
}

func executeEditorAction(client *apiClient, sessionID string, action *editorActionInput) (*executionResult, error) {
	if action == nil {
		return nil, fmt.Errorf("action is nil")
	}
	snapshot, err := client.getDocument(sessionID)
	if err != nil {
		return nil, err
	}
	document := snapshot.Document
	if document == nil {
		return nil, fmt.Errorf("session document is nil")
	}

	var matchedHit *goword.SearchHit
	var searchResult *goword.SearchResult
	if action.RequireRetrieval {
		query := strings.TrimSpace(action.RetrievalQuery)
		if query == "" {
			query = strings.TrimSpace(action.TargetText)
		}
		searchResult, err = client.searchDocument(sessionID, query, defaultString(action.ContextScope, "paragraph"), 8)
		if err != nil {
			return nil, err
		}
		matchedHit = selectBestSearchHit(action, searchResult.Hits)
	}

	paragraph := locateParagraphByID(document, strings.TrimSpace(action.PreferredParagraphID))
	if paragraph == nil {
		paragraph = locateParagraphByID(document, matchedHitParagraphID(matchedHit))
	}
	if paragraph == nil {
		paragraph = locateParagraph(document, action.TargetText)
	}

	result := &executionResult{BeforeDocument: document.Clone(), SearchResult: searchResult}
	tableID := strings.TrimSpace(action.TableID)
	if tableID == "" && matchedHit != nil {
		tableID = strings.TrimSpace(matchedHit.TableID)
	}
	rowIndex := action.RowIndex
	if rowIndex == 0 && matchedHit != nil {
		rowIndex = matchedHit.RowIndex
	}
	cellIndex := action.CellIndex
	if cellIndex == 0 && matchedHit != nil {
		cellIndex = matchedHit.CellIndex
	}

	switch strings.ToLower(strings.TrimSpace(action.Action)) {
	case "scroll":
		return result, nil
	case "replace", "delete":
		if paragraph == nil {
			return nil, fmt.Errorf("未找到目标段落: %s", action.TargetText)
		}
		start, end, ok := resolveRuneRange(paragraph.PlainText(), action.TargetText, matchedHit)
		if !ok {
			return nil, fmt.Errorf("无法定位替换范围: %s", action.TargetText)
		}
		text := action.NewText
		if strings.EqualFold(action.Action, "delete") {
			text = ""
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationReplaceText,
			ParagraphID:  paragraph.ID,
			StartOffset:  start,
			EndOffset:    end,
			Text:         text,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "insert":
		if strings.TrimSpace(action.NewText) == "" {
			return nil, fmt.Errorf("insert 缺少 new_text")
		}
		resolvedTemplate, role := resolveInsertTemplate(document, paragraph, action)
		result.TemplateParagraph = resolvedTemplate
		result.TemplateRole = role
		style := goword.ParagraphStyle{}
		runStyle := goword.TextStyle{}
		if resolvedTemplate != nil {
			style = resolvedTemplate.Style
			runStyle = firstTextRunStyleValue(resolvedTemplate)
		}
		if hasParagraphStylePatch(action.ParagraphStylePatch) {
			style = mergeParagraphStyle(style, action.ParagraphStylePatch)
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:     goword.OperationInsertParagraph,
			AnchorID: paragraphID(paragraph),
			Position: defaultString(action.Position, "after"),
			Paragraph: &goword.ParagraphInput{
				Text:  action.NewText,
				Style: style,
				Runs: []goword.Run{{
					ID:    fmt.Sprintf("eval_run_%d", time.Now().UnixNano()),
					Text:  action.NewText,
					Style: runStyle,
				}},
			},
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		if apply.Document != nil {
			result.InsertedParagraph = findParagraphByText(apply.Document, action.NewText)
		}
		return result, nil
	case "insert_paragraph_break", "insert_line_break":
		if paragraph == nil {
			return nil, fmt.Errorf("未找到目标段落: %s", action.TargetText)
		}
		start, end, ok := resolveRuneRange(paragraph.PlainText(), action.TargetText, matchedHit)
		if !ok {
			return nil, fmt.Errorf("无法定位断开位置: %s", action.TargetText)
		}
		breakOffset := end
		if strings.EqualFold(action.Position, "before") {
			breakOffset = start
		}
		operationType := goword.OperationInsertLineBreak
		if strings.EqualFold(action.Action, "insert_paragraph_break") {
			operationType = goword.OperationInsertParagraphBreak
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         operationType,
			ParagraphID:  paragraph.ID,
			StartOffset:  breakOffset,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "insert_table":
		if action.TableSpec == nil || len(action.TableSpec.Rows) == 0 {
			return nil, fmt.Errorf("insert_table 缺少 table_spec")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationInsertTable,
			AnchorID:     paragraphID(paragraph),
			Position:     defaultString(action.Position, "after"),
			Table:        action.TableSpec,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "insert_table_row":
		if strings.TrimSpace(tableID) == "" {
			return nil, fmt.Errorf("insert_table_row 缺少 table_id")
		}
		if action.TableRow == nil || len(action.TableRow.Cells) == 0 {
			return nil, fmt.Errorf("insert_table_row 缺少 table_row")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationInsertTableRow,
			TableID:      tableID,
			RowIndex:     rowIndex,
			Position:     defaultString(action.Position, "after"),
			TableRow:     action.TableRow,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "delete_table_row":
		if strings.TrimSpace(tableID) == "" {
			return nil, fmt.Errorf("delete_table_row 缺少 table_id")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationDeleteTableRow,
			TableID:      tableID,
			RowIndex:     rowIndex,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "insert_table_column":
		if strings.TrimSpace(tableID) == "" {
			return nil, fmt.Errorf("insert_table_column 缺少 table_id")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationInsertTableColumn,
			TableID:      tableID,
			CellIndex:    cellIndex,
			Position:     defaultString(action.Position, "after"),
			TableColumn:  action.TableColumn,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "delete_table_column":
		if strings.TrimSpace(tableID) == "" {
			return nil, fmt.Errorf("delete_table_column 缺少 table_id")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationDeleteTableColumn,
			TableID:      tableID,
			CellIndex:    cellIndex,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "update_table_cell":
		if strings.TrimSpace(tableID) == "" {
			return nil, fmt.Errorf("update_table_cell 缺少 table_id")
		}
		tableID = resolveDocumentTableID(document, tableID, action.TargetText)
		if action.TableCell == nil {
			return nil, fmt.Errorf("update_table_cell 缺少 table_cell")
		}
		rowIndex = normalizeTableRowIndex(document, tableID, rowIndex, action.TargetText)
		resolvedRowByTarget := false
		if resolvedRow, ok := tableRowIndexByTarget(document, tableID, action.TargetText); ok {
			rowIndex = resolvedRow
			resolvedRowByTarget = true
		}
		if adjustedRow, adjustedCell := refineTableCellCoordinates(client, sessionID, tableID, rowIndex, cellIndex, action.TargetText); adjustedRow >= 0 {
			if !resolvedRowByTarget {
				rowIndex = adjustedRow
			}
			if adjustedCell >= 0 {
				cellIndex = adjustedCell
			}
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationUpdateTableCell,
			TableID:      tableID,
			RowIndex:     rowIndex,
			CellIndex:    cellIndex,
			TableCell:    action.TableCell,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "format":
		if paragraph == nil {
			return nil, fmt.Errorf("未找到目标段落: %s", action.TargetText)
		}
		if !hasStylePatch(action.StylePatch) {
			return nil, fmt.Errorf("format 缺少 style_patch")
		}
		start, end, ok := resolveRuneRange(paragraph.PlainText(), action.TargetText, matchedHit)
		if !ok {
			return nil, fmt.Errorf("无法定位格式修改范围: %s", action.TargetText)
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationUpdateTextStyle,
			ParagraphID:  paragraph.ID,
			StartOffset:  start,
			EndOffset:    end,
			StylePatch:   action.StylePatch,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "format_paragraph":
		if paragraph == nil {
			return nil, fmt.Errorf("未找到目标段落: %s", action.TargetText)
		}
		if !hasParagraphStylePatch(action.ParagraphStylePatch) {
			return nil, fmt.Errorf("format_paragraph 缺少 paragraph_style_patch")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:                goword.OperationUpdateParagraphStyle,
			ParagraphID:         paragraph.ID,
			ParagraphStylePatch: action.ParagraphStylePatch,
			TrackChanges:        action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "format_section":
		if !hasSectionLayoutPatch(action.SectionLayoutPatch) {
			return nil, fmt.Errorf("format_section 缺少 section_layout_patch")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:               goword.OperationUpdateSectionLayout,
			SectionLayoutPatch: action.SectionLayoutPatch,
			TrackChanges:       action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	case "update_header_footer":
		if !hasHeaderFooterInput(action.HeaderFooter) {
			return nil, fmt.Errorf("update_header_footer 缺少 header_footer")
		}
		apply, err := client.applyOperations(sessionID, []goword.Operation{{
			Type:         goword.OperationUpdateHeaderFooter,
			HeaderFooter: action.HeaderFooter,
			TrackChanges: action.EnableTrackChanges,
		}})
		if err != nil {
			return nil, err
		}
		result.ApplyResult = apply
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported action: %s", action.Action)
	}
}

func refineTableCellCoordinates(client *apiClient, sessionID, tableID string, rowIndex, cellIndex int, targetText string) (int, int) {
	if client == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(tableID) == "" {
		return rowIndex, cellIndex
	}
	query := strings.TrimSpace(targetText)
	if query == "" {
		return rowIndex, cellIndex
	}
	search, err := client.searchDocument(sessionID, query, "table", 16)
	if err != nil || search == nil || len(search.Hits) == 0 {
		return rowIndex, cellIndex
	}
	best := pickBestTableHit(search.Hits, tableID, rowIndex, cellIndex)
	if best == nil {
		return rowIndex, cellIndex
	}
	resolvedRow := rowIndex
	if best.RowIndex >= 0 {
		resolvedRow = best.RowIndex
	}
	resolvedCell := cellIndex
	if resolvedCell < 0 && best.CellIndex >= 0 {
		resolvedCell = best.CellIndex
	}
	return resolvedRow, resolvedCell
}

func pickBestTableHit(hits []goword.SearchHit, tableID string, rowIndex, cellIndex int) *goword.SearchHit {
	var best *goword.SearchHit
	bestScore := -1 << 30
	for index := range hits {
		hit := &hits[index]
		if strings.TrimSpace(hit.TableID) == "" || strings.TrimSpace(hit.TableID) != strings.TrimSpace(tableID) {
			continue
		}
		score := 0
		if rowIndex >= 0 {
			score -= absInt(hit.RowIndex - rowIndex)
		}
		if cellIndex >= 0 {
			score -= absInt(hit.CellIndex - cellIndex)
		}
		if hit.RowIndex == rowIndex {
			score += 6
		}
		if hit.CellIndex == cellIndex {
			score += 3
		}
		if score > bestScore {
			bestScore = score
			best = hit
		}
	}
	return best
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func executeEditorActions(client *apiClient, sessionID string, actions []*editorActionInput) (*executionResult, error) {
	if len(actions) == 0 {
		return nil, fmt.Errorf("actions is empty")
	}
	batchResults := make([]*executionResult, 0, len(actions))
	totalDiffCount := 0
	for index, action := range actions {
		result, err := executeEditorAction(client, sessionID, action)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条动作执行失败 (%s): %w", index+1, action.Action, err)
		}
		batchResults = append(batchResults, result)
		if result != nil && result.ApplyResult != nil {
			totalDiffCount += len(result.ApplyResult.Diffs)
		}
	}
	finalResult := batchResults[len(batchResults)-1]
	if finalResult == nil {
		return nil, fmt.Errorf("final execution result is nil")
	}
	if batchResults[0] != nil && batchResults[0].BeforeDocument != nil {
		finalResult.BeforeDocument = batchResults[0].BeforeDocument
	}
	finalResult.AppliedActions = actions
	finalResult.BatchResults = batchResults
	finalResult.TotalDiffCount = totalDiffCount
	return finalResult, nil
}

func prepareLLMScenarioInputDocument(ctx *evalContext, scenario llmDialogueScenario) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("eval context is nil")
	}
	if scenario.InputDocBuilder == nil {
		return ctx.inputDocPath, nil
	}
	inputPath := filepath.Join(ctx.config.Artifacts, "scenario-inputs", scenario.Name+"-input.docx")
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o755); err != nil {
		return "", err
	}
	if err := scenario.InputDocBuilder(inputPath); err != nil {
		return "", err
	}
	return inputPath, nil
}

func saveAndReopen(client *apiClient, sessionID, path string) (*goword.SaveResult, *goword.Document, error) {
	saved, err := client.saveDocument(sessionID, path)
	if err != nil {
		return nil, nil, err
	}
	reopened, err := goword.OpenDOCX(saved.Path)
	if err != nil {
		return nil, nil, err
	}
	return saved, reopened, nil
}

func loadSavedDocumentLayout(client *apiClient, path string) (*goword.DocumentLayout, error) {
	if client == nil {
		return nil, fmt.Errorf("api client is nil")
	}
	session, err := client.openDocument(path)
	if err != nil {
		return nil, err
	}
	defer client.closeSession(session.ID)
	return client.layoutDocument(session.ID, goword.LayoutOptions{})
}

func buildParagraphEvidence(reader *wordReaderResponse, targetText string) (string, string) {
	if reader == nil {
		return "", ""
	}
	hit := firstQueryHit(reader)
	if hit == nil {
		return "目标内容位于正文段落中。", formatStyleBasis(reader.StyleCatalog.DefaultParagraphStyle, reader.StyleCatalog.DefaultTextStyle, "默认正文样式")
	}
	context := fmt.Sprintf("命中正文段落，段落预览：%s", previewText(hit.Preview))
	basis := formatStyleBasis(hit.ParagraphStyle, hit.DominantTextStyle, "命中正文段落样式")
	if strings.TrimSpace(hit.ParagraphStyle.StyleID) == "" {
		basis = formatStyleBasis(reader.StyleCatalog.DefaultParagraphStyle, reader.StyleCatalog.DefaultTextStyle, "默认正文样式")
	}
	if strings.TrimSpace(targetText) != "" {
		context = fmt.Sprintf("目标内容位于正文段落中，原文片段：%s。", targetText)
	}
	return context, basis
}

func appendExpandedRewriteSameParagraphEvidence(context, basis, region string) (string, string) {
	trimmedRegion := strings.TrimSpace(region)
	if trimmedRegion == "" {
		trimmedRegion = "正文"
	}
	trimmedContext := strings.TrimSpace(context)
	if trimmedContext == "" {
		trimmedContext = fmt.Sprintf("目标内容位于%s段落中。", trimmedRegion)
	}
	trimmedBasis := strings.TrimSpace(basis)
	if trimmedBasis == "" {
		trimmedBasis = fmt.Sprintf("沿用%s原有样式。", trimmedRegion)
	}
	trimmedContext += fmt.Sprintf(" 已检查目标前后相邻段落与句子节奏，确认这次修改继续保持同一%s段落，不新增换行或拆段。", trimmedRegion)
	trimmedBasis += fmt.Sprintf("；参考目标前后相邻段落和句子节奏后，确认本次仍保持同一%s段落，不改换行或断段。", trimmedRegion)
	return trimmedContext, trimmedBasis
}

func buildBodyInsertEvidence(reader *wordReaderResponse) (string, string) {
	hit := firstQueryHit(reader)
	context := "目标位置位于一级标题后，本次新增内容属于该章节正文首段。"
	reference := nearestOutlineParagraph(reader, hitParagraphID(hit), "paragraph")
	if reference != nil {
		context = fmt.Sprintf("目标位置位于标题“%s”之后，新增内容应沿用该章节正文段落格式。", previewText(hit.Preview))
		return context, formatStyleBasis(reference.ParagraphStyle, reference.DominantTextStyle, "章节正文模板")
	}
	return context, formatStyleBasis(reader.StyleCatalog.DefaultParagraphStyle, reader.StyleCatalog.DefaultTextStyle, "默认正文样式")
}

func buildHeadingEvidence(reader *wordReaderResponse, level int) (string, string) {
	hit := firstQueryHit(reader)
	context := fmt.Sprintf("目标位置属于 %d 级标题区域，新增内容是同级标题。", level)
	if hit != nil {
		context = fmt.Sprintf("命中标题“%s”，新增内容属于同级标题。", previewText(hit.Preview))
		return context, formatStyleBasis(hit.ParagraphStyle, hit.DominantTextStyle, fmt.Sprintf("同级 Heading%d 标题样式", level))
	}
	reference := firstOutlineOfType(reader, "heading")
	if reference != nil {
		return context, formatStyleBasis(reference.ParagraphStyle, reference.DominantTextStyle, fmt.Sprintf("同级 Heading%d 标题样式", level))
	}
	return context, fmt.Sprintf("沿用 style_catalog 中的 Heading%d 标题样式。", level)
}

func buildTableEvidence(reader *wordReaderResponse, targetText string) (string, string) {
	hit := firstQueryHit(reader)
	context := fmt.Sprintf("目标内容位于表格单元格中，需保持表格内文字样式一致，原值为 %s。", targetText)
	if hit == nil {
		return context, formatStyleBasis(reader.StyleCatalog.DefaultParagraphStyle, reader.StyleCatalog.DefaultTextStyle, "默认表格正文样式")
	}
	basis := formatStyleBasis(hit.ParagraphStyle, hit.DominantTextStyle, "命中单元格段落样式")
	return context, basis
}

func buildTableInsertEvidence(reader *wordReaderResponse, targetText string) (string, string) {
	context := fmt.Sprintf("目标位置位于表格单元格中，需在“%s”后追加说明内容，并保持单元格格式一致。", targetText)
	return context, "保持表格单元格格式一致，避免破坏现有标签与说明结构。"
}

func singleParagraphTableCellInput(text, styleID string) *goword.TableCellInput {
	return &goword.TableCellInput{Paragraphs: []goword.ParagraphInput{{Text: text, Style: goword.ParagraphStyle{StyleID: styleID}}}}
}

func riskPromptTableCellInput(extraText string) *goword.TableCellInput {
	paragraphs := []goword.ParagraphInput{
		{Text: "风险提示", Style: goword.ParagraphStyle{StyleID: "TableHeader"}},
		{Text: "当前为灰度验证阶段。", Style: goword.ParagraphStyle{StyleID: "TableBody"}},
	}
	if strings.TrimSpace(extraText) != "" {
		paragraphs = append(paragraphs, goword.ParagraphInput{Text: extraText, Style: goword.ParagraphStyle{StyleID: "TableBody"}})
	}
	return &goword.TableCellInput{Paragraphs: paragraphs}
}

func buildListBodyInsertEvidence(reader *wordReaderResponse, targetText string) (string, string) {
	context := fmt.Sprintf("目标位置位于编号步骤“%s”之后，需要补充说明内容。", targetText)
	return context, "保持步骤语义清晰，避免破坏现有编号结构。"
}

func formatStyleBasis(paragraphStyle readerParagraphStyle, textStyle readerTextStyle, label string) string {
	parts := []string{label}
	if strings.TrimSpace(paragraphStyle.StyleID) != "" {
		parts = append(parts, fmt.Sprintf("style_id=%s", paragraphStyle.StyleID))
	}
	if strings.TrimSpace(textStyle.FontFamily) != "" {
		parts = append(parts, fmt.Sprintf("字体=%s", textStyle.FontFamily))
	}
	if textStyle.FontSizeHP != 0 {
		parts = append(parts, fmt.Sprintf("字号=%d", textStyle.FontSizeHP))
	}
	if paragraphStyle.Alignment != "" {
		parts = append(parts, fmt.Sprintf("对齐=%s", paragraphStyle.Alignment))
	}
	return strings.Join(parts, "，")
}

func hasStylePatch(style goword.TextStyle) bool {
	return strings.TrimSpace(style.StyleID) != "" ||
		strings.TrimSpace(style.FontFamily) != "" ||
		strings.TrimSpace(style.ThemeFontASCII) != "" ||
		strings.TrimSpace(style.ThemeFontEastAsia) != "" ||
		style.FontSizeHP != 0 ||
		style.BoldSet || style.Bold ||
		style.ItalicSet || style.Italic ||
		style.UnderlineSet || style.Underline ||
		strings.TrimSpace(style.Color) != "" ||
		strings.TrimSpace(style.ThemeColor) != ""
}

func hasParagraphStylePatch(style goword.ParagraphStyle) bool {
	return strings.TrimSpace(style.StyleID) != "" ||
		strings.TrimSpace(style.Alignment) != "" ||
		style.SpacingBefore != 0 ||
		style.SpacingAfter != 0 ||
		style.LineSpacing != 0 ||
		style.FirstLineIndent != 0 ||
		style.LeftIndent != 0 ||
		style.Numbering != nil
}

func hasSectionLayoutPatch(layout goword.SectionLayout) bool {
	return layout.PageWidth > 0 ||
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

func hasHeaderFooterInput(input *goword.HeaderFooterInput) bool {
	return input != nil && strings.TrimSpace(input.Kind) != "" && len(input.Blocks) > 0
}

func mergeParagraphStyle(base goword.ParagraphStyle, patch goword.ParagraphStyle) goword.ParagraphStyle {
	merged := base
	if strings.TrimSpace(patch.StyleID) != "" {
		merged.StyleID = patch.StyleID
	}
	if strings.TrimSpace(patch.Alignment) != "" {
		merged.Alignment = patch.Alignment
	}
	if patch.SpacingBefore != 0 {
		merged.SpacingBefore = patch.SpacingBefore
	}
	if patch.SpacingAfter != 0 {
		merged.SpacingAfter = patch.SpacingAfter
	}
	if patch.LineSpacing != 0 {
		merged.LineSpacing = patch.LineSpacing
	}
	if patch.FirstLineIndent != 0 {
		merged.FirstLineIndent = patch.FirstLineIndent
	}
	if patch.LeftIndent != 0 {
		merged.LeftIndent = patch.LeftIndent
	}
	if patch.Numbering != nil {
		numbering := *patch.Numbering
		merged.Numbering = &numbering
	}
	return merged
}

func firstQueryHit(reader *wordReaderResponse) *readerQueryHit {
	if reader == nil || len(reader.Query.Hits) == 0 {
		return nil
	}
	return &reader.Query.Hits[0]
}

func findReaderQueryHitContaining(reader *wordReaderResponse, needle string) *readerQueryHit {
	if reader == nil {
		return nil
	}
	trimmedNeedle := strings.TrimSpace(needle)
	for index := range reader.Query.Hits {
		hit := &reader.Query.Hits[index]
		if strings.Contains(strings.TrimSpace(hit.Preview), trimmedNeedle) {
			return hit
		}
	}
	for index := range reader.Query.Hits {
		hit := &reader.Query.Hits[index]
		if strings.Contains(normalizeMatchText(hit.Preview), normalizeMatchText(trimmedNeedle)) {
			return hit
		}
	}
	return nil
}

func firstOutlineOfType(reader *wordReaderResponse, itemType string) *readerOutlineItem {
	if reader == nil {
		return nil
	}
	for index := range reader.Outline {
		if strings.EqualFold(reader.Outline[index].Type, itemType) {
			return &reader.Outline[index]
		}
	}
	return nil
}

func nearestOutlineParagraph(reader *wordReaderResponse, paragraphID, itemType string) *readerOutlineItem {
	if reader == nil || len(reader.Outline) == 0 {
		return nil
	}
	anchorIndex := -1
	for index, item := range reader.Outline {
		if item.ParagraphID == paragraphID {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		return firstOutlineOfType(reader, itemType)
	}
	for index := anchorIndex + 1; index < len(reader.Outline); index++ {
		if strings.EqualFold(reader.Outline[index].Type, itemType) {
			return &reader.Outline[index]
		}
		if strings.EqualFold(reader.Outline[index].Type, "heading") && strings.EqualFold(itemType, "paragraph") {
			break
		}
	}
	return firstOutlineOfType(reader, itemType)
}

func selectBestSearchHit(action *editorActionInput, hits []goword.SearchHit) *goword.SearchHit {
	if len(hits) == 0 {
		return nil
	}
	if action != nil && strings.TrimSpace(action.PreferredParagraphID) != "" {
		for index := range hits {
			if hits[index].ParagraphID == strings.TrimSpace(action.PreferredParagraphID) {
				return &hits[index]
			}
		}
	}
	normalizedQuery := normalizeMatchText(defaultString(action.TargetText, action.RetrievalQuery))
	var paragraphHits []goword.SearchHit
	for _, hit := range hits {
		if strings.TrimSpace(hit.ParagraphID) != "" {
			paragraphHits = append(paragraphHits, hit)
		}
	}
	if len(paragraphHits) == 0 {
		return &hits[0]
	}
	bestIndex := 0
	bestScore := scoreSearchHit(action, &paragraphHits[0], 0, len(paragraphHits), normalizedQuery)
	for index := 1; index < len(paragraphHits); index++ {
		score := scoreSearchHit(action, &paragraphHits[index], index, len(paragraphHits), normalizedQuery)
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	if bestScore > 0 {
		return &paragraphHits[bestIndex]
	}
	return &paragraphHits[0]
}

func scoreSearchHit(action *editorActionInput, hit *goword.SearchHit, index, total int, normalizedQuery string) int {
	if hit == nil {
		return 0
	}
	normalizedHit := normalizeMatchText(hit.Text)
	cue := semanticCueText(action)
	score := 0
	if normalizedQuery != "" && strings.Contains(normalizedHit, normalizedQuery) {
		queryLen := len([]rune(normalizedQuery))
		switch {
		case queryLen >= 5:
			score += 90
		case queryLen >= 3:
			score += 42
		default:
			score += 16
		}
	}
	retrievalQuery := ""
	if action != nil {
		retrievalQuery = normalizeMatchText(action.RetrievalQuery)
	}
	if retrievalQuery != "" && retrievalQuery != normalizedQuery && strings.Contains(normalizedHit, retrievalQuery) {
		retrievalLen := len([]rune(retrievalQuery))
		if retrievalLen > 6 {
			retrievalLen = 6
		}
		score += retrievalLen * 4
	}
	if textLooksLikeHeading(hit.Text) {
		if action == nil || !textLooksLikeHeading(action.TargetText) {
			score -= 14
		}
		if strings.Contains(cue, "正文") || strings.Contains(cue, "那条说明") {
			score -= 10
		}
	} else if strings.Contains(cue, "正文") || strings.Contains(cue, "那条说明") {
		score += 6
	}
	score += longestCommonSubstringWeight(normalizedHit, cue)
	score += ordinalCueBonus(cue, index, total)
	return score
}

func semanticCueText(action *editorActionInput) string {
	if action == nil {
		return ""
	}
	return normalizeMatchText(strings.Join([]string{action.ReaderContextSummary, action.EditIntent, action.Constraints}, " "))
}

func longestCommonSubstringWeight(left, right string) int {
	length := longestCommonSubstringLength(left, right)
	switch {
	case length >= 6:
		return 48
	case length >= 5:
		return 38
	case length >= 4:
		return 28
	case length >= 3:
		return 16
	case length >= 2:
		return 6
	default:
		return 0
	}
}

func longestCommonSubstringLength(left, right string) int {
	if left == "" || right == "" {
		return 0
	}
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	dp := make([]int, len(rightRunes)+1)
	best := 0
	for i := 1; i <= len(leftRunes); i++ {
		previous := 0
		for j := 1; j <= len(rightRunes); j++ {
			temp := dp[j]
			if leftRunes[i-1] == rightRunes[j-1] {
				dp[j] = previous + 1
				if dp[j] > best {
					best = dp[j]
				}
			} else {
				dp[j] = 0
			}
			previous = temp
		}
	}
	return best
}

func ordinalCueBonus(cue string, index, total int) int {
	if cue == "" || total <= 0 {
		return 0
	}
	bonus := 0
	if (strings.Contains(cue, "最后") || strings.Contains(cue, "末尾") || strings.Contains(cue, "结尾")) && index == total-1 {
		bonus += 22
	}
	if (strings.Contains(cue, "第一条") || strings.Contains(cue, "第一段") || strings.Contains(cue, "首段") || strings.Contains(cue, "开头")) && index == 0 {
		bonus += 22
	}
	if (strings.Contains(cue, "中间") || strings.Contains(cue, "中段")) && (index == total/2 || (total%2 == 0 && index == total/2-1)) {
		bonus += 26
	}
	if (strings.Contains(cue, "第二条") || strings.Contains(cue, "第二段")) && index == 1 {
		bonus += 18
	}
	if (strings.Contains(cue, "第三条") || strings.Contains(cue, "第三段")) && index == 2 {
		bonus += 18
	}
	if strings.Contains(cue, "倒数第二") && index == total-2 {
		bonus += 18
	}
	return bonus
}

func locateParagraph(document *goword.Document, targetText string) *goword.Paragraph {
	if document == nil {
		return nil
	}
	normalizedTarget := strings.TrimSpace(targetText)
	if normalizedTarget == "" {
		if len(document.Paragraphs) == 0 {
			return nil
		}
		return &document.Paragraphs[len(document.Paragraphs)-1]
	}
	for index := range document.Paragraphs {
		if strings.Contains(document.Paragraphs[index].PlainText(), normalizedTarget) {
			return &document.Paragraphs[index]
		}
	}
	compact := normalizeMatchText(normalizedTarget)
	for index := range document.Paragraphs {
		if strings.Contains(normalizeMatchText(document.Paragraphs[index].PlainText()), compact) {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func locateParagraphByID(document *goword.Document, paragraphID string) *goword.Paragraph {
	if document == nil || strings.TrimSpace(paragraphID) == "" {
		return nil
	}
	for index := range document.Paragraphs {
		if document.Paragraphs[index].ID == paragraphID {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func resolveRuneRange(content, target string, hit *goword.SearchHit) (int, int, bool) {
	if hit != nil && hit.MatchEnd > hit.MatchStart {
		runes := []rune(content)
		if hit.MatchStart >= 0 && hit.MatchEnd <= len(runes) && hit.MatchStart < hit.MatchEnd {
			matched := string(runes[hit.MatchStart:hit.MatchEnd])
			if normalizeMatchText(matched) == normalizeMatchText(target) {
				return hit.MatchStart, hit.MatchEnd, true
			}
		}
	}
	return findRuneRange(content, target)
}

func findRuneRange(content, target string) (int, int, bool) {
	source := []rune(content)
	needle := []rune(target)
	if len(needle) == 0 || len(source) < len(needle) {
		return 0, 0, false
	}
	for index := 0; index <= len(source)-len(needle); index++ {
		matched := true
		for offset := 0; offset < len(needle); offset++ {
			if source[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index, index + len(needle), true
		}
	}
	return 0, 0, false
}

func resolveInsertTemplate(document *goword.Document, anchor *goword.Paragraph, action *editorActionInput) (*goword.Paragraph, string) {
	paragraphs := documentParagraphs(document)
	anchorIndex := findParagraphIndex(paragraphs, anchor)
	role := inferInsertTemplateRole(action, anchor)
	preferredLevel := parsePreferredHeadingLevel(strings.Join([]string{action.FormatBasis, action.ReaderContextSummary, action.NewText}, " "))
	if role != "heading" {
		if tableTemplate := resolveTableCellTemplateParagraph(document, anchor, action); tableTemplate != nil {
			return tableTemplate, "body"
		}
		if adjacentTemplate := resolveAdjacentBodyTemplateParagraph(paragraphs, anchorIndex, anchor, action); adjacentTemplate != nil {
			return adjacentTemplate, "body"
		}
		if listTemplate := resolveListBodyTemplateParagraph(paragraphs, anchorIndex, anchor, action); listTemplate != nil {
			return listTemplate, "body"
		}
	}

	if role == "heading" {
		if anchor != nil && paragraphLooksLikeHeading(*anchor) {
			anchorLevel := extractHeadingLevel(anchor)
			if preferredLevel == nil || (anchorLevel != nil && *anchorLevel == *preferredLevel) {
				return anchor, role
			}
		}
		return findNearestHeadingParagraph(paragraphs, anchorIndex, preferredLevel), role
	}

	if anchor == nil {
		return findNearestBodyParagraph(paragraphs, -1), role
	}
	if !paragraphLooksLikeHeading(*anchor) {
		return anchor, role
	}
	if strings.EqualFold(action.Position, "before") {
		if paragraph := findAdjacentBodyBeforeHeading(paragraphs, anchorIndex); paragraph != nil {
			return paragraph, role
		}
		return findNearestBodyParagraph(paragraphs, anchorIndex-1), role
	}
	if paragraph := findAdjacentBodyAfterHeading(paragraphs, anchorIndex); paragraph != nil {
		return paragraph, role
	}
	return findNearestBodyParagraph(paragraphs, anchorIndex+1), role
}

func resolveListBodyTemplateParagraph(paragraphs []goword.Paragraph, anchorIndex int, anchor *goword.Paragraph, action *editorActionInput) *goword.Paragraph {
	if anchor == nil || !paragraphLooksLikeNumberedListItem(*anchor) || !shouldPreferListBodyTemplate(action) {
		return nil
	}
	if strings.EqualFold(action.Position, "before") {
		for index := anchorIndex - 1; index >= 0; index-- {
			if paragraphLooksLikeHeading(paragraphs[index]) {
				break
			}
			if !paragraphLooksLikeNumberedListItem(paragraphs[index]) {
				return &paragraphs[index]
			}
		}
	} else {
		for index := anchorIndex + 1; index < len(paragraphs); index++ {
			if paragraphLooksLikeHeading(paragraphs[index]) {
				break
			}
			if !paragraphLooksLikeNumberedListItem(paragraphs[index]) {
				return &paragraphs[index]
			}
		}
	}
	for index := 0; index < len(paragraphs); index++ {
		if !paragraphLooksLikeNumberedListItem(paragraphs[index]) && !paragraphLooksLikeHeading(paragraphs[index]) {
			return &paragraphs[index]
		}
	}
	return nil
}

func resolveTableCellTemplateParagraph(document *goword.Document, anchor *goword.Paragraph, action *editorActionInput) *goword.Paragraph {
	if document == nil || anchor == nil || !shouldPreferTableBodyTemplate(anchor, action) {
		return nil
	}
	for blockIndex := range document.Blocks {
		table := document.Blocks[blockIndex].Table
		if table == nil {
			continue
		}
		for rowIndex := range table.Rows {
			for cellIndex := range table.Rows[rowIndex].Cells {
				paragraphs := make([]*goword.Paragraph, 0)
				anchorPos := -1
				for innerIndex := range table.Rows[rowIndex].Cells[cellIndex].Blocks {
					paragraph := table.Rows[rowIndex].Cells[cellIndex].Blocks[innerIndex].Paragraph
					if paragraph == nil {
						continue
					}
					paragraphs = append(paragraphs, paragraph)
					if paragraph.ID == anchor.ID {
						anchorPos = len(paragraphs) - 1
					}
				}
				if anchorPos < 0 {
					continue
				}
				if anchorPos >= 0 && anchorPos < len(paragraphs) && !paragraphLooksLikeTableLabel(*paragraphs[anchorPos]) {
					return paragraphs[anchorPos]
				}
				if strings.EqualFold(action.Position, "before") {
					for index := anchorPos - 1; index >= 0; index-- {
						if !paragraphLooksLikeTableLabel(*paragraphs[index]) {
							return paragraphs[index]
						}
					}
				} else {
					for index := anchorPos + 1; index < len(paragraphs); index++ {
						if !paragraphLooksLikeTableLabel(*paragraphs[index]) {
							return paragraphs[index]
						}
					}
				}
				for index := range paragraphs {
					if index == anchorPos {
						continue
					}
					if !paragraphLooksLikeTableLabel(*paragraphs[index]) {
						return paragraphs[index]
					}
				}
				return paragraphs[anchorPos]
			}
		}
	}
	return nil
}

func documentParagraphs(document *goword.Document) []goword.Paragraph {
	if document == nil {
		return nil
	}
	return document.Paragraphs
}

func inferInsertTemplateRole(action *editorActionInput, anchor *goword.Paragraph) string {
	evidence := strings.Join([]string{action.FormatBasis, action.ReaderContextSummary, action.EditIntent}, " ")
	normalized := strings.ToLower(evidence)
	if strings.Contains(normalized, "body") || strings.Contains(normalized, "normal") || strings.Contains(evidence, "正文") || strings.Contains(evidence, "普通段落") {
		return "body"
	}
	if parsePreferredHeadingLevel(evidence) != nil || strings.Contains(evidence, "同级标题") || strings.Contains(evidence, "新增标题") || strings.Contains(evidence, "插入标题") || strings.Contains(evidence, "小标题") || strings.Contains(evidence, "章节标题") {
		return "heading"
	}
	if textLooksLikeHeading(action.NewText) {
		return "heading"
	}
	if anchor != nil && paragraphLooksLikeHeading(*anchor) && strings.EqualFold(action.Position, "after") {
		return "body"
	}
	return "body"
}

func hasExplicitBodyTemplateHint(action *editorActionInput) bool {
	if action == nil {
		return false
	}
	evidence := strings.ToLower(strings.Join([]string{action.FormatBasis, action.ReaderContextSummary, action.EditIntent}, " "))
	for _, keyword := range []string{"正文", "普通段落", "body", "normal", "说明正文", "说明段落", "tablebody", "单元格说明", "同一单元格正文"} {
		if strings.Contains(evidence, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func textLooksLikeNumberedListItem(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return regexp.MustCompile(`^(\d+[、.．)|）]|[一二三四五六七八九十]+、|\([一二三四五六七八九十]+\)|（[一二三四五六七八九十]+）)`).MatchString(trimmed)
}

func textLooksLikeBodyParagraph(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return strings.ContainsAny(trimmed, "，。；：,.!?！？") || len([]rune(trimmed)) > 12
}

func shouldPreferTableBodyTemplate(anchor *goword.Paragraph, action *editorActionInput) bool {
	if hasExplicitBodyTemplateHint(action) {
		return true
	}
	if anchor == nil {
		return false
	}
	if !paragraphLooksLikeTableLabel(*anchor) {
		return true
	}
	return textLooksLikeBodyParagraph(action.NewText)
}

func shouldPreferListBodyTemplate(action *editorActionInput) bool {
	if hasExplicitBodyTemplateHint(action) {
		return true
	}
	newText := strings.TrimSpace(action.NewText)
	if newText == "" {
		return false
	}
	return !textLooksLikeNumberedListItem(newText)
}

func shouldPreferAdjacentBodyTemplate(anchor *goword.Paragraph, action *editorActionInput) bool {
	if anchor == nil {
		return false
	}
	return hasExplicitBodyTemplateHint(action) && paragraphLooksLikeTableLabel(*anchor)
}

func resolveAdjacentBodyTemplateParagraph(paragraphs []goword.Paragraph, anchorIndex int, anchor *goword.Paragraph, action *editorActionInput) *goword.Paragraph {
	if anchor == nil || anchorIndex < 0 || !shouldPreferAdjacentBodyTemplate(anchor, action) {
		return nil
	}
	if strings.EqualFold(action.Position, "before") {
		for index := anchorIndex - 1; index >= 0; index-- {
			if paragraphMatchesBodyTemplate(paragraphs[index], action) {
				return &paragraphs[index]
			}
		}
		for index := anchorIndex + 1; index < len(paragraphs); index++ {
			if paragraphMatchesBodyTemplate(paragraphs[index], action) {
				return &paragraphs[index]
			}
		}
		return nil
	}
	for index := anchorIndex + 1; index < len(paragraphs); index++ {
		if paragraphMatchesBodyTemplate(paragraphs[index], action) {
			return &paragraphs[index]
		}
	}
	for index := anchorIndex - 1; index >= 0; index-- {
		if paragraphMatchesBodyTemplate(paragraphs[index], action) {
			return &paragraphs[index]
		}
	}
	return nil
}

func paragraphMatchesBodyTemplate(paragraph goword.Paragraph, action *editorActionInput) bool {
	if strings.TrimSpace(paragraph.PlainText()) == "" {
		return false
	}
	desiredStyleID := normalizeStyleID(action.ParagraphStylePatch.StyleID)
	styleID := normalizeStyleID(paragraph.Style.StyleID)
	if desiredStyleID != "" {
		return styleID == desiredStyleID && !paragraphLooksLikeTableLabel(paragraph)
	}
	if styleID == "tablebody" || styleID == "normal" || styleID == "bodytext" {
		return !paragraphLooksLikeTableLabel(paragraph)
	}
	return !paragraphLooksLikeTableLabel(paragraph)
}

func parsePreferredHeadingLevel(evidence string) *int {
	normalized := strings.ToLower(strings.TrimSpace(evidence))
	if normalized == "" {
		return nil
	}
	if strings.Contains(normalized, "style_id=title") || strings.Contains(normalized, " title") {
		level := 0
		return &level
	}
	for level := 1; level <= 9; level++ {
		needle := fmt.Sprintf("heading%d", level)
		if strings.Contains(normalized, needle) {
			value := level
			return &value
		}
	}
	for symbol, value := range map[string]int{"一级标题": 1, "二级标题": 2, "三级标题": 3, "四级标题": 4} {
		if strings.Contains(evidence, symbol) {
			level := value
			return &level
		}
	}
	return nil
}

func paragraphLooksLikeHeading(paragraph goword.Paragraph) bool {
	return extractHeadingLevel(&paragraph) != nil
}

func paragraphLooksLikeTableLabel(paragraph goword.Paragraph) bool {
	styleID := normalizeStyleID(paragraph.Style.StyleID)
	if strings.Contains(styleID, "tableheader") || strings.Contains(styleID, "tablelabel") {
		return true
	}
	text := strings.TrimSpace(paragraph.PlainText())
	runStyle := firstTextRunStyleValue(&paragraph)
	if runStyle.Bold && len([]rune(text)) <= 12 {
		return true
	}
	return false
}

func paragraphLooksLikeNumberedListItem(paragraph goword.Paragraph) bool {
	styleID := normalizeStyleID(paragraph.Style.StyleID)
	if strings.Contains(styleID, "list") {
		return true
	}
	return paragraph.Style.Numbering != nil && strings.TrimSpace(paragraph.Style.Numbering.NumID) != ""
}

func textLooksLikeHeading(text string) bool {
	return headingLevelFromText(text) != nil
}

func extractHeadingLevel(paragraph *goword.Paragraph) *int {
	if paragraph == nil {
		return nil
	}
	styleID := strings.ToLower(strings.TrimSpace(paragraph.Style.StyleID))
	if styleID == "title" {
		level := 0
		return &level
	}
	for level := 1; level <= 9; level++ {
		needle := fmt.Sprintf("heading%d", level)
		if styleID == needle {
			value := level
			return &value
		}
	}
	return headingLevelFromText(paragraph.PlainText())
}

func headingLevelFromText(text string) *int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len([]rune(trimmed)) > 40 {
		return nil
	}
	if strings.HasPrefix(trimmed, "第") && strings.Contains(trimmed, "章") {
		level := 1
		return &level
	}
	if strings.HasPrefix(trimmed, "第") && strings.Contains(trimmed, "节") {
		level := 2
		return &level
	}
	for _, prefix := range []string{"一、", "二、", "三、", "四、", "五、", "1.", "2.", "3."} {
		if strings.HasPrefix(trimmed, prefix) {
			level := 2
			return &level
		}
	}
	if strings.HasPrefix(trimmed, "(") || strings.HasPrefix(trimmed, "（") || strings.HasPrefix(trimmed, "1.1") {
		level := 3
		return &level
	}
	return nil
}

func findParagraphIndex(paragraphs []goword.Paragraph, paragraph *goword.Paragraph) int {
	if paragraph == nil {
		return -1
	}
	for index := range paragraphs {
		if paragraphs[index].ID == paragraph.ID {
			return index
		}
	}
	return -1
}

func findNearestBodyParagraph(paragraphs []goword.Paragraph, anchorIndex int) *goword.Paragraph {
	if len(paragraphs) == 0 {
		return nil
	}
	if anchorIndex < 0 {
		for index := len(paragraphs) - 1; index >= 0; index-- {
			if !paragraphLooksLikeHeading(paragraphs[index]) {
				return &paragraphs[index]
			}
		}
		return &paragraphs[len(paragraphs)-1]
	}
	for distance := 0; distance < len(paragraphs); distance++ {
		left := anchorIndex - distance
		if left >= 0 && !paragraphLooksLikeHeading(paragraphs[left]) {
			return &paragraphs[left]
		}
		right := anchorIndex + distance
		if right < len(paragraphs) && !paragraphLooksLikeHeading(paragraphs[right]) {
			return &paragraphs[right]
		}
	}
	return &paragraphs[anchorIndex]
}

func findNearestHeadingParagraph(paragraphs []goword.Paragraph, anchorIndex int, preferredLevel *int) *goword.Paragraph {
	if len(paragraphs) == 0 {
		return nil
	}
	effectiveIndex := anchorIndex
	if effectiveIndex < 0 {
		effectiveIndex = len(paragraphs) - 1
	}
	matchesLevel := func(paragraph goword.Paragraph) bool {
		level := extractHeadingLevel(&paragraph)
		if level == nil {
			return false
		}
		if preferredLevel == nil {
			return true
		}
		return *level == *preferredLevel
	}
	for distance := 0; distance < len(paragraphs); distance++ {
		left := effectiveIndex - distance
		if left >= 0 && matchesLevel(paragraphs[left]) {
			return &paragraphs[left]
		}
		right := effectiveIndex + distance
		if right < len(paragraphs) && matchesLevel(paragraphs[right]) {
			return &paragraphs[right]
		}
	}
	if preferredLevel != nil {
		return findNearestHeadingParagraph(paragraphs, anchorIndex, nil)
	}
	return nil
}

func findAdjacentBodyAfterHeading(paragraphs []goword.Paragraph, anchorIndex int) *goword.Paragraph {
	for index := anchorIndex + 1; index < len(paragraphs); index++ {
		if paragraphLooksLikeHeading(paragraphs[index]) {
			break
		}
		return &paragraphs[index]
	}
	return nil
}

func findAdjacentBodyBeforeHeading(paragraphs []goword.Paragraph, anchorIndex int) *goword.Paragraph {
	for index := anchorIndex - 1; index >= 0; index-- {
		if paragraphLooksLikeHeading(paragraphs[index]) {
			break
		}
		return &paragraphs[index]
	}
	return nil
}

func findParagraphByText(document *goword.Document, text string) *goword.Paragraph {
	if document == nil {
		return nil
	}
	for index := range document.Paragraphs {
		if strings.TrimSpace(document.Paragraphs[index].PlainText()) == strings.TrimSpace(text) {
			return &document.Paragraphs[index]
		}
	}
	for index := range document.Paragraphs {
		if strings.Contains(document.Paragraphs[index].PlainText(), strings.TrimSpace(text)) {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func findParagraphContainingAll(document *goword.Document, texts ...string) *goword.Paragraph {
	if document == nil || len(texts) == 0 {
		return nil
	}
	for index := range document.Paragraphs {
		content := document.Paragraphs[index].PlainText()
		matched := true
		for _, text := range texts {
			if !strings.Contains(content, strings.TrimSpace(text)) {
				matched = false
				break
			}
		}
		if matched {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func paragraphImmediatelyAfterText(document *goword.Document, text string) *goword.Paragraph {
	anchor := findParagraphByText(document, text)
	if anchor == nil || document == nil {
		return nil
	}
	for index := range document.Paragraphs {
		if document.Paragraphs[index].ID == anchor.ID {
			if index+1 < len(document.Paragraphs) {
				return &document.Paragraphs[index+1]
			}
			break
		}
	}
	return nil
}

func effectiveParagraphNumberingText(document *goword.Document, paragraph *goword.Paragraph) string {
	if paragraph == nil {
		return ""
	}
	if strings.TrimSpace(paragraph.NumberingText) != "" {
		return strings.TrimSpace(paragraph.NumberingText)
	}
	return strings.TrimSpace(buildEffectiveNumberingTextIndex(document)[paragraph.ID])
}

func buildEffectiveNumberingTextIndex(document *goword.Document) map[string]string {
	result := make(map[string]string)
	if document == nil {
		return result
	}
	definitions := make(map[string]goword.NumberingDefinition, len(document.Meta.Numbering))
	for _, definition := range document.Meta.Numbering {
		definitions[strings.TrimSpace(definition.NumID)] = definition
	}
	counters := map[string][]int{}
	for _, block := range document.Blocks {
		if block.Paragraph == nil || block.Paragraph.Style.Numbering == nil {
			continue
		}
		numbering := block.Paragraph.Style.Numbering
		numID := strings.TrimSpace(numbering.NumID)
		if numID == "" || numbering.Level < 0 {
			continue
		}
		counter := counters[numID]
		if len(counter) <= numbering.Level {
			extended := make([]int, numbering.Level+1)
			copy(extended, counter)
			counter = extended
		}
		counter[numbering.Level]++
		for index := numbering.Level + 1; index < len(counter); index++ {
			counter[index] = 0
		}
		counters[numID] = counter
		result[block.Paragraph.ID] = formatEffectiveNumberingText(definitions[numID], counter, numbering.Level)
	}
	return result
}

func formatEffectiveNumberingText(definition goword.NumberingDefinition, counter []int, level int) string {
	if level < 0 {
		return ""
	}
	if level < len(definition.Levels) {
		pattern := strings.TrimSpace(definition.Levels[level].Text)
		if pattern != "" {
			for index := 0; index <= level && index < len(counter); index++ {
				pattern = strings.ReplaceAll(pattern, fmt.Sprintf("%%%d", index+1), strconv.Itoa(counter[index]))
			}
			return strings.TrimSpace(pattern)
		}
	}
	parts := make([]string, 0, level+1)
	for index := 0; index <= level && index < len(counter); index++ {
		if counter[index] == 0 {
			continue
		}
		parts = append(parts, strconv.Itoa(counter[index]))
	}
	return strings.Join(parts, ".")
}

func findLayoutTableOnPage(layout *goword.DocumentLayout, pageIndex int, tableID string) *goword.TableLayout {
	if layout == nil || pageIndex < 0 || pageIndex >= len(layout.Pages) {
		return nil
	}
	for _, block := range layout.Pages[pageIndex].Blocks {
		if block.Table == nil {
			continue
		}
		if strings.TrimSpace(tableID) == "" || strings.TrimSpace(block.Table.TableID) == strings.TrimSpace(tableID) {
			return block.Table
		}
	}
	return nil
}

func layoutTableHasRowTexts(table *goword.TableLayout, texts ...string) bool {
	if table == nil || len(texts) == 0 {
		return false
	}
	for _, row := range table.Rows {
		matched := true
		for _, text := range texts {
			if !layoutRowContainsText(row, text) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func layoutRowContainsText(row goword.TableRowLayout, text string) bool {
	needle := strings.TrimSpace(text)
	if needle == "" {
		return false
	}
	for _, cell := range row.Cells {
		for _, paragraph := range cell.Paragraphs {
			for _, line := range paragraph.Lines {
				if strings.Contains(strings.TrimSpace(line.Text), needle) {
					return true
				}
			}
		}
	}
	return false
}

func resultHasReaderLayoutTrace(result *executionResult, focusPageIndex int) bool {
	if result == nil {
		return false
	}
	for _, trace := range result.ToolTrace {
		name := strings.ToLower(strings.TrimSpace(trace.Name))
		if !strings.Contains(name, "word-reader") {
			continue
		}
		if !traceInputBool(trace.Input, "include_layout") {
			continue
		}
		if traceInputInt(trace.Input, "focus_page_index") != focusPageIndex {
			continue
		}
		return true
	}
	return false
}

func traceInputBool(input map[string]any, key string) bool {
	if input == nil {
		return false
	}
	value, ok := input[key]
	if !ok {
		return false
	}
	boolValue, ok := value.(bool)
	return ok && boolValue
}

func traceInputInt(input map[string]any, key string) int {
	if input == nil {
		return -1
	}
	value, ok := input[key]
	if !ok {
		return -1
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return -1
}

func tableContainsText(document *goword.Document, text string) bool {
	if document == nil {
		return false
	}
	needle := strings.TrimSpace(text)
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				for _, inner := range cell.Blocks {
					if inner.Paragraph != nil && strings.Contains(inner.Paragraph.PlainText(), needle) {
						return true
					}
				}
			}
		}
	}
	return false
}

func tableContainsExactText(document *goword.Document, text string) bool {
	if document == nil {
		return false
	}
	needle := strings.TrimSpace(text)
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				for _, inner := range cell.Blocks {
					if inner.Paragraph != nil && strings.TrimSpace(inner.Paragraph.PlainText()) == needle {
						return true
					}
				}
			}
		}
	}
	return false
}

func tableRowContainsTexts(row goword.TableRow, texts ...string) bool {
	for _, text := range texts {
		needle := strings.TrimSpace(text)
		if needle == "" {
			continue
		}
		matched := false
		for _, cell := range row.Cells {
			for _, inner := range cell.Blocks {
				if inner.Paragraph != nil && strings.Contains(strings.TrimSpace(inner.Paragraph.PlainText()), needle) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func findTableRowContainingTexts(table *goword.Table, texts ...string) (*goword.TableRow, int) {
	if table == nil {
		return nil, -1
	}
	for index := range table.Rows {
		if tableRowContainsTexts(table.Rows[index], texts...) {
			return &table.Rows[index], index
		}
	}
	return nil, -1
}

func tableRowIndexByTarget(document *goword.Document, tableID, targetText string) (int, bool) {
	if document == nil {
		return 0, false
	}
	trimmedTableID := strings.TrimSpace(tableID)
	trimmedTarget := strings.TrimSpace(targetText)
	if trimmedTableID == "" || trimmedTarget == "" {
		return 0, false
	}
	normalizedTarget := normalizeMatchText(trimmedTarget)
	for _, block := range document.Blocks {
		if block.Table == nil || strings.TrimSpace(block.Table.ID) != trimmedTableID {
			continue
		}
		for rowIndex, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				for _, inner := range cell.Blocks {
					if inner.Paragraph == nil {
						continue
					}
					content := strings.TrimSpace(inner.Paragraph.PlainText())
					if content == trimmedTarget || strings.Contains(content, trimmedTarget) || strings.Contains(normalizeMatchText(content), normalizedTarget) {
						return rowIndex, true
					}
				}
			}
		}
	}
	return 0, false
}

func normalizeTableRowIndex(document *goword.Document, tableID string, rowIndex int, targetText string) int {
	if document == nil || rowIndex < 0 {
		return rowIndex
	}
	table := findDocumentTableByID(document, tableID)
	if table == nil {
		return rowIndex
	}
	if rowIndex >= len(table.Rows) && rowIndex-1 >= 0 && rowIndex-1 < len(table.Rows) {
		rowIndex--
	}
	if rowIndex > 0 && !tableRowContainsTarget(table, rowIndex, targetText) && tableRowContainsTarget(table, rowIndex-1, targetText) {
		return rowIndex - 1
	}
	return rowIndex
}

func findDocumentTableByID(document *goword.Document, tableID string) *goword.Table {
	if document == nil {
		return nil
	}
	trimmedTableID := strings.TrimSpace(tableID)
	if trimmedTableID == "" {
		return nil
	}
	for _, block := range document.Blocks {
		if block.Table != nil && strings.TrimSpace(block.Table.ID) == trimmedTableID {
			return block.Table
		}
	}
	return nil
}

func resolveDocumentTableID(document *goword.Document, tableID, targetText string) string {
	trimmedTableID := strings.TrimSpace(tableID)
	if trimmedTableID != "" && findDocumentTableByID(document, trimmedTableID) != nil {
		return trimmedTableID
	}
	if strings.TrimSpace(targetText) == "" {
		return trimmedTableID
	}
	table := findTableContainingTexts(document, targetText)
	if table == nil {
		fragments := splitCompoundTargetText(targetText)
		if len(fragments) > 1 {
			table = findTableContainingTexts(document, fragments...)
		}
	}
	if table == nil {
		return trimmedTableID
	}
	return strings.TrimSpace(table.ID)
}

func splitCompoundTargetText(targetText string) []string {
	parts := strings.Fields(strings.TrimSpace(targetText))
	fragments := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if len([]rune(trimmed)) < 2 {
			continue
		}
		fragments = append(fragments, trimmed)
	}
	return fragments
}

func tableRowContainsTarget(table *goword.Table, rowIndex int, targetText string) bool {
	if table == nil || rowIndex < 0 || rowIndex >= len(table.Rows) {
		return false
	}
	trimmedTarget := strings.TrimSpace(targetText)
	if trimmedTarget == "" {
		return false
	}
	normalizedTarget := normalizeMatchText(trimmedTarget)
	for _, cell := range table.Rows[rowIndex].Cells {
		for _, inner := range cell.Blocks {
			if inner.Paragraph == nil {
				continue
			}
			content := strings.TrimSpace(inner.Paragraph.PlainText())
			if content == trimmedTarget || strings.Contains(content, trimmedTarget) || strings.Contains(normalizeMatchText(content), normalizedTarget) {
				return true
			}
		}
	}
	return false
}

func findHeaderFooterPart(document *goword.Document, kind, variant string) *goword.HeaderFooterPart {
	if document == nil {
		return nil
	}
	normalizedKind := strings.TrimSpace(strings.ToLower(kind))
	normalizedVariant := strings.TrimSpace(strings.ToLower(variant))
	if normalizedVariant == "" {
		normalizedVariant = "default"
	}
	for index := range document.Meta.HeaderFooters {
		part := &document.Meta.HeaderFooters[index]
		partKind := strings.TrimSpace(strings.ToLower(part.Kind))
		partVariant := strings.TrimSpace(strings.ToLower(part.Variant))
		if partVariant == "" {
			partVariant = "default"
		}
		if partKind == normalizedKind && partVariant == normalizedVariant {
			return part
		}
	}
	return nil
}

func headerFooterPartContainsText(part *goword.HeaderFooterPart, text string) bool {
	if part == nil {
		return false
	}
	needle := strings.TrimSpace(text)
	if needle == "" {
		return false
	}
	return blocksContainText(part.Blocks, needle)
}

func blocksContainText(blocks []goword.Block, text string) bool {
	needle := strings.TrimSpace(text)
	if needle == "" {
		return false
	}
	for _, block := range blocks {
		if block.Paragraph != nil && strings.Contains(block.Paragraph.PlainText(), needle) {
			return true
		}
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				if blocksContainText(cell.Blocks, needle) {
					return true
				}
			}
		}
	}
	return false
}

func tableCellContainsTexts(document *goword.Document, texts ...string) bool {
	if document == nil {
		return false
	}
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				joined := make([]string, 0)
				for _, inner := range cell.Blocks {
					if inner.Paragraph != nil {
						joined = append(joined, inner.Paragraph.PlainText())
					}
				}
				cellText := strings.Join(joined, "\n")
				matched := true
				for _, text := range texts {
					if !strings.Contains(cellText, strings.TrimSpace(text)) {
						matched = false
						break
					}
				}
				if matched {
					return true
				}
			}
		}
	}
	return false
}

func tableCellParagraphCount(document *goword.Document, texts ...string) int {
	if document == nil {
		return 0
	}
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				joined := make([]string, 0)
				paragraphCount := 0
				for _, inner := range cell.Blocks {
					if inner.Paragraph != nil {
						joined = append(joined, inner.Paragraph.PlainText())
						paragraphCount++
					}
				}
				cellText := strings.Join(joined, "\n")
				matched := true
				for _, text := range texts {
					if !strings.Contains(cellText, strings.TrimSpace(text)) {
						matched = false
						break
					}
				}
				if matched {
					return paragraphCount
				}
			}
		}
	}
	return 0
}

func tableCellParagraphsContainingTexts(document *goword.Document, texts ...string) []*goword.Paragraph {
	cell := findTableCellContainingTexts(document, texts...)
	if cell == nil {
		return nil
	}
	paragraphs := make([]*goword.Paragraph, 0)
	for innerIndex := range cell.Blocks {
		if cell.Blocks[innerIndex].Paragraph == nil {
			continue
		}
		paragraphs = append(paragraphs, cell.Blocks[innerIndex].Paragraph)
	}
	return paragraphs
}

func findTableCellContainingTexts(document *goword.Document, texts ...string) *goword.TableCell {
	if document == nil {
		return nil
	}
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Rows {
			for cellIndex := range row.Cells {
				joined := make([]string, 0)
				for innerIndex := range row.Cells[cellIndex].Blocks {
					if row.Cells[cellIndex].Blocks[innerIndex].Paragraph == nil {
						continue
					}
					joined = append(joined, row.Cells[cellIndex].Blocks[innerIndex].Paragraph.PlainText())
				}
				cellText := strings.Join(joined, "\n")
				matched := true
				for _, text := range texts {
					if !strings.Contains(cellText, strings.TrimSpace(text)) {
						matched = false
						break
					}
				}
				if matched {
					return &row.Cells[cellIndex]
				}
			}
		}
	}
	return nil
}

func findTableContainingTexts(document *goword.Document, texts ...string) *goword.Table {
	if document == nil || len(texts) == 0 {
		return nil
	}
	for _, block := range document.Blocks {
		if block.Table == nil {
			continue
		}
		matched := true
		for _, text := range texts {
			if !tableStructContainsText(block.Table, text) {
				matched = false
				break
			}
		}
		if matched {
			return block.Table
		}
	}
	return nil
}

func tableStructContainsText(table *goword.Table, text string) bool {
	if table == nil {
		return false
	}
	needle := normalizeMatchText(text)
	for rowIndex := range table.Rows {
		for cellIndex := range table.Rows[rowIndex].Cells {
			for blockIndex := range table.Rows[rowIndex].Cells[cellIndex].Blocks {
				paragraph := table.Rows[rowIndex].Cells[cellIndex].Blocks[blockIndex].Paragraph
				if paragraph == nil {
					continue
				}
				if strings.Contains(normalizeMatchText(paragraph.PlainText()), needle) {
					return true
				}
			}
		}
	}
	return false
}

func firstParagraphFromTableCell(cell *goword.TableCell) *goword.Paragraph {
	if cell == nil {
		return nil
	}
	for blockIndex := range cell.Blocks {
		if cell.Blocks[blockIndex].Paragraph != nil {
			return cell.Blocks[blockIndex].Paragraph
		}
	}
	return nil
}

func expectedSingleRunParagraphSnapshot(text string, paragraphStyle goword.ParagraphStyle, textStyle goword.TextStyle) paragraphStructureSnapshot {
	return paragraphStructureSnapshot{
		Text:  text,
		Style: paragraphStyle,
		Runs: []paragraphRunSnapshot{{
			Text:  text,
			Style: textStyle,
		}},
	}
}

func expectedTableCellSnapshot(paragraphs ...paragraphStructureSnapshot) tableCellStructureSnapshot {
	return tableCellStructureSnapshot{Paragraphs: paragraphs}
}

func paragraphSnapshotWithText(base paragraphStructureSnapshot, text string) paragraphStructureSnapshot {
	cloned := base
	cloned.Text = text
	if len(cloned.Runs) == 0 {
		cloned.Runs = []paragraphRunSnapshot{{Text: text}}
		return cloned
	}
	cloned.Runs = append([]paragraphRunSnapshot(nil), cloned.Runs...)
	cloned.Runs[0].Text = text
	for index := 1; index < len(cloned.Runs); index++ {
		cloned.Runs[index].Text = ""
	}
	return cloned
}

func paragraphSnapshotWithStyledText(base paragraphStructureSnapshot, target string, patch goword.TextStyle) (paragraphStructureSnapshot, error) {
	byteIndex := strings.Index(base.Text, target)
	if byteIndex < 0 {
		return paragraphStructureSnapshot{}, fmt.Errorf("baseline snapshot 中未找到目标短语: %s", target)
	}
	startOffset := len([]rune(base.Text[:byteIndex]))
	endOffset := startOffset + len([]rune(target))
	cloned := base
	cloned.Runs = patchSnapshotRunsStyle(base.Runs, startOffset, endOffset, patch)
	return cloned, nil
}

func paragraphSnapshotWithInsertedLineBreak(base paragraphStructureSnapshot, offset int) paragraphStructureSnapshot {
	cloned := base
	textRunes := []rune(base.Text)
	if offset < 0 {
		offset = 0
	}
	if offset > len(textRunes) {
		offset = len(textRunes)
	}
	cloned.Text = string(textRunes[:offset]) + "\n" + string(textRunes[offset:])
	cloned.Runs = insertSnapshotRunBreak(base.Runs, offset, goword.RunBreakTypeLine)
	return cloned
}

func splitParagraphSnapshotAt(base paragraphStructureSnapshot, offset int) (paragraphStructureSnapshot, paragraphStructureSnapshot) {
	textRunes := []rune(base.Text)
	if offset < 0 {
		offset = 0
	}
	if offset > len(textRunes) {
		offset = len(textRunes)
	}
	leftRuns, rightRuns := splitSnapshotRunsAtVisibleOffset(base.Runs, offset)
	fallbackStyle := firstSnapshotRunStyle(base.Runs)
	left := base
	left.Text = string(textRunes[:offset])
	left.Runs = ensureSnapshotRuns(leftRuns, fallbackStyle)
	right := base
	right.Text = string(textRunes[offset:])
	right.Runs = ensureSnapshotRuns(rightRuns, fallbackStyle)
	return left, right
}

func insertSnapshotRunBreak(runs []paragraphRunSnapshot, offset int, breakType goword.RunBreakType) []paragraphRunSnapshot {
	leftRuns, rightRuns := splitSnapshotRunsAtVisibleOffset(runs, offset)
	breakRun := paragraphRunSnapshot{BreakType: breakType, Style: firstSnapshotRunStyle(runs)}
	result := append(leftRuns, breakRun)
	result = append(result, rightRuns...)
	return result
}

func patchSnapshotRunsStyle(runs []paragraphRunSnapshot, startOffset, endOffset int, patch goword.TextStyle) []paragraphRunSnapshot {
	if len(runs) == 0 {
		return runs
	}
	out := make([]paragraphRunSnapshot, 0, len(runs)+1)
	currentOffset := 0

	for _, run := range runs {
		visibleRunes := []rune(snapshotRunVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if run.BreakType != "" {
			out = append(out, cloneSnapshotRun(run))
			continue
		}

		runes := []rune(run.Text)
		switch {
		case endOffset <= runStart || startOffset >= runEnd:
			out = append(out, cloneSnapshotRun(run))
		default:
			if startOffset > runStart {
				leftRun := cloneSnapshotRun(run)
				leftRun.Text = string(runes[:startOffset-runStart])
				out = append(out, leftRun)
			}

			middleStart := 0
			if startOffset > runStart {
				middleStart = startOffset - runStart
			}
			middleEnd := len(runes)
			if endOffset < runEnd {
				middleEnd = endOffset - runStart
			}
			middleText := string(runes[middleStart:middleEnd])
			if middleText != "" {
				patchedRun := cloneSnapshotRun(run)
				patchedRun.Text = middleText
				mergeSnapshotTextStyle(&patchedRun.Style, patch)
				out = append(out, patchedRun)
			}

			if endOffset < runEnd {
				rightRun := cloneSnapshotRun(run)
				rightRun.Text = string(runes[middleEnd:])
				out = append(out, rightRun)
			}
		}
	}

	if len(out) == 0 {
		return runs
	}
	return out
}

func splitSnapshotRunsAtVisibleOffset(runs []paragraphRunSnapshot, offset int) ([]paragraphRunSnapshot, []paragraphRunSnapshot) {
	leftRuns := make([]paragraphRunSnapshot, 0, len(runs))
	rightRuns := make([]paragraphRunSnapshot, 0, len(runs))
	currentOffset := 0
	splitDone := false

	for _, run := range runs {
		visibleRunes := []rune(snapshotRunVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if splitDone {
			rightRuns = append(rightRuns, cloneSnapshotRun(run))
			continue
		}

		if offset <= runStart {
			rightRuns = append(rightRuns, cloneSnapshotRun(run))
			splitDone = true
			continue
		}

		if offset >= runEnd {
			leftRuns = append(leftRuns, cloneSnapshotRun(run))
			continue
		}

		leftText := string(visibleRunes[:offset-runStart])
		rightText := string(visibleRunes[offset-runStart:])
		leftRun := cloneSnapshotRun(run)
		leftRun.Text = leftText
		leftRuns = append(leftRuns, leftRun)
		rightRun := cloneSnapshotRun(run)
		rightRun.Text = rightText
		rightRuns = append(rightRuns, rightRun)
		splitDone = true
	}

	return leftRuns, rightRuns
}

func ensureSnapshotRuns(runs []paragraphRunSnapshot, fallbackStyle goword.TextStyle) []paragraphRunSnapshot {
	if len(runs) > 0 {
		return runs
	}
	return []paragraphRunSnapshot{{Style: fallbackStyle}}
}

func firstSnapshotRunStyle(runs []paragraphRunSnapshot) goword.TextStyle {
	for _, run := range runs {
		if run.BreakType != "" {
			continue
		}
		return run.Style
	}
	if len(runs) > 0 {
		return runs[0].Style
	}
	return goword.TextStyle{}
}

func mergeSnapshotTextStyle(base *goword.TextStyle, patch goword.TextStyle) {
	if base == nil {
		return
	}
	if patch.StyleID != "" {
		base.StyleID = patch.StyleID
	}
	if patch.FontFamily != "" {
		base.FontFamily = patch.FontFamily
		base.ThemeFontASCII = ""
		base.ThemeFontEastAsia = ""
	}
	if patch.ThemeFontASCII != "" {
		base.ThemeFontASCII = patch.ThemeFontASCII
	}
	if patch.ThemeFontEastAsia != "" {
		base.ThemeFontEastAsia = patch.ThemeFontEastAsia
	}
	if patch.FontSizeHP > 0 {
		base.FontSizeHP = patch.FontSizeHP
	}
	if patch.BoldSet {
		base.Bold = patch.Bold
		base.BoldSet = true
	}
	if patch.ItalicSet {
		base.Italic = patch.Italic
		base.ItalicSet = true
	}
	if patch.UnderlineSet {
		base.Underline = patch.Underline
		base.UnderlineSet = true
	}
	if patch.Color != "" {
		base.Color = patch.Color
		base.ThemeColor = ""
	}
	if patch.ThemeColor != "" {
		base.ThemeColor = patch.ThemeColor
	}
}

func snapshotRunVisibleText(run paragraphRunSnapshot) string {
	if run.BreakType == goword.RunBreakTypeLine {
		return "\n"
	}
	return run.Text
}

func cloneSnapshotRun(run paragraphRunSnapshot) paragraphRunSnapshot {
	return paragraphRunSnapshot{
		Text:      run.Text,
		BreakType: run.BreakType,
		Style:     run.Style,
	}
}

func assertParagraphSnapshot(paragraph *goword.Paragraph, expected paragraphStructureSnapshot, label string) error {
	if paragraph == nil {
		return fmt.Errorf("%s 缺失", label)
	}
	actual := buildParagraphStructureSnapshot(paragraph)
	if snapshotHash(actual) != snapshotHash(expected) {
		return fmt.Errorf("%s 结构快照异常: actual=%s expected=%s", label, snapshotJSON(actual), snapshotJSON(expected))
	}
	return nil
}

func assertTableCellSnapshot(cell *goword.TableCell, expected tableCellStructureSnapshot, label string) error {
	if cell == nil {
		return fmt.Errorf("%s 缺失", label)
	}
	actual := buildTableCellStructureSnapshot(cell)
	if snapshotHash(actual) != snapshotHash(expected) {
		return fmt.Errorf("%s 结构快照异常: actual=%s expected=%s", label, snapshotJSON(actual), snapshotJSON(expected))
	}
	return nil
}

func assertUntouchedParagraphHash(before, after *goword.Document, label, text string) error {
	beforeParagraph := findParagraphByText(before, text)
	afterParagraph := findParagraphByText(after, text)
	if beforeParagraph == nil || afterParagraph == nil {
		return fmt.Errorf("%s 缺少 before/after 段落用于结构比对", label)
	}
	beforeHash := snapshotHash(buildParagraphStructureSnapshot(beforeParagraph))
	afterHash := snapshotHash(buildParagraphStructureSnapshot(afterParagraph))
	if beforeHash != afterHash {
		return fmt.Errorf("%s 结构快照漂移: before=%s after=%s", label, beforeHash, afterHash)
	}
	return nil
}

func assertUntouchedCellHash(before, after *goword.Document, label string, texts ...string) error {
	beforeCell := findTableCellContainingTexts(before, texts...)
	afterCell := findTableCellContainingTexts(after, texts...)
	if beforeCell == nil || afterCell == nil {
		return fmt.Errorf("%s 缺少 before/after 单元格用于结构比对", label)
	}
	beforeHash := snapshotHash(buildTableCellStructureSnapshot(beforeCell))
	afterHash := snapshotHash(buildTableCellStructureSnapshot(afterCell))
	if beforeHash != afterHash {
		return fmt.Errorf("%s 结构快照漂移: before=%s after=%s", label, beforeHash, afterHash)
	}
	return nil
}

func buildParagraphStructureSnapshot(paragraph *goword.Paragraph) paragraphStructureSnapshot {
	if paragraph == nil {
		return paragraphStructureSnapshot{}
	}
	runs := make([]paragraphRunSnapshot, 0, len(paragraph.Runs))
	for _, run := range paragraph.Runs {
		runs = append(runs, paragraphRunSnapshot{Text: run.Text, BreakType: run.BreakType, Style: run.Style})
	}
	return paragraphStructureSnapshot{
		Text:          paragraph.PlainText(),
		Style:         paragraph.Style,
		NumberingText: paragraph.NumberingText,
		Runs:          runs,
	}
}

func buildTableCellStructureSnapshot(cell *goword.TableCell) tableCellStructureSnapshot {
	if cell == nil {
		return tableCellStructureSnapshot{}
	}
	paragraphs := make([]paragraphStructureSnapshot, 0)
	for _, block := range cell.Blocks {
		if block.Paragraph == nil {
			continue
		}
		paragraphs = append(paragraphs, buildParagraphStructureSnapshot(block.Paragraph))
	}
	return tableCellStructureSnapshot{Paragraphs: paragraphs}
}

func snapshotHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("marshal_error:%v", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum)
}

func snapshotJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("marshal_error:%v", err)
	}
	return string(encoded)
}

func countResultActionType(result *executionResult, actionType string) int {
	if result == nil {
		return 0
	}
	count := 0
	trimmedType := strings.TrimSpace(strings.ToLower(actionType))
	for _, action := range result.AppliedActions {
		if action == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(action.Action)) == trimmedType {
			count++
		}
	}
	return count
}

func assertEnterpriseDocumentIntegrity(document *goword.Document) error {
	if document == nil {
		return fmt.Errorf("document is nil")
	}
	title := findParagraphByText(document, "Auramate 智能编辑综合验证报告")
	if title == nil || !strings.EqualFold(normalizeStyleID(title.Style.StyleID), "title") {
		return fmt.Errorf("标题样式漂移")
	}
	listItem := findParagraphByText(document, "完成样式评审")
	if listItem == nil || listItem.Style.Numbering == nil || strings.TrimSpace(listItem.Style.Numbering.NumID) != "9" {
		return fmt.Errorf("编号列表样式漂移")
	}
	subListItem := findParagraphByText(document, "核对表格样式")
	if subListItem == nil || subListItem.Style.Numbering == nil || strings.TrimSpace(subListItem.Style.Numbering.NumID) != "9" || subListItem.Style.Numbering.Level != 1 {
		return fmt.Errorf("多级编号列表样式漂移")
	}
	tableHeaderLeft := findParagraphByText(document, "指标")
	tableHeaderRight := findParagraphByText(document, "当前值")
	if tableHeaderLeft == nil || tableHeaderRight == nil {
		return fmt.Errorf("表格表头结构异常")
	}
	if normalizeStyleID(tableHeaderLeft.Style.StyleID) != "tableheader" || normalizeStyleID(tableHeaderRight.Style.StyleID) != "tableheader" {
		return fmt.Errorf("表格表头结构异常")
	}
	releaseTable := findTableContainingTexts(document, "发布批次", "灰度批次", "正式批次")
	if releaseTable == nil || len(releaseTable.Rows) == 0 || !releaseTable.Rows[0].RepeatHeaderSet || !releaseTable.Rows[0].RepeatHeader {
		return fmt.Errorf("重复表头表格结构漂移")
	}
	return nil
}

func firstTextRunStyleValue(paragraph *goword.Paragraph) goword.TextStyle {
	if paragraph == nil {
		return goword.TextStyle{}
	}
	for _, run := range paragraph.Runs {
		if strings.TrimSpace(run.Text) != "" {
			return run.Style
		}
	}
	if len(paragraph.Runs) > 0 {
		return paragraph.Runs[0].Style
	}
	return goword.TextStyle{}
}

func findTextRunContaining(paragraph *goword.Paragraph, text string) *goword.Run {
	if paragraph == nil {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	for index := range paragraph.Runs {
		if strings.Contains(paragraph.Runs[index].Text, trimmed) {
			return &paragraph.Runs[index]
		}
	}
	for index := range paragraph.Runs {
		if strings.Contains(normalizeMatchText(paragraph.Runs[index].Text), normalizeMatchText(trimmed)) {
			return &paragraph.Runs[index]
		}
	}
	return nil
}

func textStyleSatisfies(actual, expected goword.TextStyle) bool {
	if strings.TrimSpace(expected.FontFamily) != "" && actual.FontFamily != expected.FontFamily {
		return false
	}
	if expected.FontSizeHP != 0 && actual.FontSizeHP != expected.FontSizeHP {
		return false
	}
	if expected.BoldSet && actual.Bold != expected.Bold {
		return false
	}
	if expected.ItalicSet && actual.Italic != expected.Italic {
		return false
	}
	if expected.UnderlineSet && actual.Underline != expected.Underline {
		return false
	}
	if strings.TrimSpace(expected.Color) != "" && actual.Color != expected.Color {
		return false
	}
	return true
}

func paragraphStyleID(paragraph *goword.Paragraph) string {
	if paragraph == nil {
		return ""
	}
	return paragraph.Style.StyleID
}

func normalizeMatchText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\n", ""), " ", ""))
}

func normalizeStyleID(styleID string) string {
	return strings.ToLower(strings.TrimSpace(styleID))
}

func paragraphID(paragraph *goword.Paragraph) string {
	if paragraph == nil {
		return ""
	}
	return paragraph.ID
}

func matchedHitParagraphID(hit *goword.SearchHit) string {
	if hit == nil {
		return ""
	}
	return hit.ParagraphID
}

func hitParagraphID(hit *readerQueryHit) string {
	if hit == nil {
		return ""
	}
	return hit.ParagraphID
}

func previewText(value string) string {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) <= 24 {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:24]) + "..."
}

func passScenario(name string, started time.Time, message string, details map[string]any, artifactPath string) scenarioResult {
	return scenarioResult{
		Name:           name,
		Passed:         true,
		DurationMillis: time.Since(started).Milliseconds(),
		Message:        message,
		ArtifactPath:   artifactPath,
		Details:        details,
	}
}

func failScenario(name string, started time.Time, err error, suggestions []string, details map[string]any) scenarioResult {
	message := "场景失败"
	if err != nil {
		message = err.Error()
	}
	return scenarioResult{
		Name:           name,
		Passed:         false,
		DurationMillis: time.Since(started).Milliseconds(),
		Message:        message,
		Error:          errString(err),
		Suggestions:    suggestions,
		Details:        details,
	}
}

func replaceSuggestions() []string {
	return []string{
		"若正文替换失败，应优先检查 reader -> search -> replace_text 的命中链，确认 paragraph_id 和字符区间来自同一次命中。",
		"若替换成功但保存后丢失，应优先检查 SaveDOCX 重写与 reopen 校验链路。",
	}
}

func resolvePythonPath(cfg config) (string, error) {
	if cfg.PythonPath != "" {
		return cfg.PythonPath, nil
	}
	candidates := []string{
		filepath.Join(cfg.RepoRoot, ".venv", "Scripts", "python.exe"),
		filepath.Join(cfg.RepoRoot, ".venv", "bin", "python"),
		filepath.Join(filepath.Dir(cfg.RepoRoot), ".venv", "Scripts", "python.exe"),
		filepath.Join(filepath.Dir(cfg.RepoRoot), ".venv", "bin", "python"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 Python 解释器，请通过 --python 指定路径")
}

func findRepoRoot(start string) (string, error) {
	current := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("未找到 go.mod，无法定位仓库根目录")
		}
		current = parent
	}
}

func defaultString(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func urlEscape(value string) string {
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "?", "%3F", "&", "%26", "=", "%3D")
	return replacer.Replace(value)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
