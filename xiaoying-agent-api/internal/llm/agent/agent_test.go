package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/skills"
)

type stubAgentTool struct {
	info tools.ToolInfo
}

type stubContextProvider struct {
	model models.Model
}

type stubVisualAnalyzer struct{}

func (stubVisualAnalyzer) Analyze(context.Context, string, message.BinaryContent) (string, error) {
	return "ok", nil
}

func TestShouldTriggerSilentTurnRecoveryTreatsReasoningOnlyAsSilent(t *testing.T) {
	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			name: "reasoning only is not user-facing final answer",
			msg: message.Message{Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "I checked the result."},
			}},
			want: true,
		},
		{
			name: "text answer does not need recovery",
			msg: message.Message{Parts: []message.ContentPart{
				message.TextContent{Text: "已完成。"},
			}},
			want: false,
		},
		{
			name: "tool call means the turn is still executing",
			msg: message.Message{Parts: []message.ContentPart{
				message.ToolCall{Name: "read", Input: `{"file_path":"a.go"}`},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTriggerSilentTurnRecovery(tt.msg); got != tt.want {
				t.Fatalf("shouldTriggerSilentTurnRecovery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func intPtr(value int) *int {
	return &value
}

type capturingSummaryProvider struct {
	model     models.Model
	content   string
	mu        sync.Mutex
	messages  [][]message.Message
	releaseCh <-chan struct{}
	startedCh chan<- struct{}
}

func (p stubContextProvider) SendMessages(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*provider.ProviderResponse, error) {
	return nil, nil
}

func (p stubContextProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan provider.ProviderEvent {
	ch := make(chan provider.ProviderEvent)
	close(ch)
	return ch
}

func (p stubContextProvider) Model() models.Model {
	return p.model
}

func (p stubContextProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p stubContextProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func (p *capturingSummaryProvider) SendMessages(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*provider.ProviderResponse, error) {
	p.mu.Lock()
	copyMessages := append([]message.Message(nil), messages...)
	p.messages = append(p.messages, copyMessages)
	p.mu.Unlock()
	if p.startedCh != nil {
		select {
		case p.startedCh <- struct{}{}:
		default:
		}
	}
	if p.releaseCh != nil {
		select {
		case <-p.releaseCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &provider.ProviderResponse{Content: p.content, FinishReason: message.FinishReasonEndTurn}, nil
}

func (p *capturingSummaryProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan provider.ProviderEvent {
	ch := make(chan provider.ProviderEvent)
	close(ch)
	return ch
}

func (p *capturingSummaryProvider) Model() models.Model {
	if p.model.ID == "" {
		return models.Model{ID: models.ModelID("summary-test-model"), Provider: models.ProviderMock}
	}
	return p.model
}

func (p *capturingSummaryProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *capturingSummaryProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func (p *capturingSummaryProvider) lastRequest() []message.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.messages) == 0 {
		return nil
	}
	return append([]message.Message(nil), p.messages[len(p.messages)-1]...)
}

func TestAnalyzeActionStatusIgnoresQuestionOnlyDiagnosticPrompt(t *testing.T) {
	a := &agent{}
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "为什么运行时总会调用工具？是不是提示词的问题？"}}},
	}
	userAskedAction, botClaimedAction, recentlyActed := a.analyzeActionStatus(history, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "这是提示词和运行时策略共同导致的。"}}})
	if userAskedAction {
		t.Fatalf("expected question-only diagnostic prompt not to be treated as explicit action request")
	}
	if botClaimedAction {
		t.Fatalf("expected explanatory response not to claim action")
	}
	if recentlyActed {
		t.Fatalf("expected no recent tool activity")
	}
}

func TestBuildSilentTurnRecoveryExhaustedMessageIsFinished(t *testing.T) {
	msg := buildSilentTurnRecoveryExhaustedMessage()
	if !msg.IsFinished() {
		t.Fatalf("expected silent turn recovery exhausted message to be finished")
	}
	if msg.FinishPart() == nil || msg.FinishPart().Reason != message.FinishReasonEndTurn {
		t.Fatalf("expected silent turn recovery exhausted message to carry end_turn finish part")
	}
}

func TestAnalyzeActionStatusTreatsExplicitSideEffectQuestionAsAction(t *testing.T) {
	a := &agent{}
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "能不能帮我打开计算器？"}}},
	}
	userAskedAction, _, _ := a.analyzeActionStatus(history, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "我来帮你处理。"}}})
	if !userAskedAction {
		t.Fatalf("expected explicit side-effect request to be treated as action")
	}
}

func TestAnalyzeActionStatusTreatsFramedTechnicalMutationAsAction(t *testing.T) {
	a := &agent{}
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "请修复登录 bug。"}}},
	}
	userAskedAction, _, _ := a.analyzeActionStatus(history, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "我先检查根因。"}}})
	if !userAskedAction {
		t.Fatalf("expected framed technical mutation request to be treated as action")
	}
}

func TestBuildToolTurnFingerprintNormalizesJSONButKeepsDistinctTargets(t *testing.T) {
	first := buildToolTurnFingerprint([]message.ToolCall{{
		Name:  "read",
		Input: `{"limit":50,"file_path":"backend/main.go","offset":1}`,
	}})
	second := buildToolTurnFingerprint([]message.ToolCall{{
		Name:  "read",
		Input: `{"file_path":"backend/main.go","offset":1,"limit":50}`,
	}})
	third := buildToolTurnFingerprint([]message.ToolCall{{
		Name:  "read",
		Input: `{"file_path":"backend/main.go","offset":51,"limit":50}`,
	}})

	if first.key == "" {
		t.Fatalf("expected non-empty fingerprint for read tool")
	}
	if first.key != second.key {
		t.Fatalf("expected same semantic read call to normalize to same fingerprint, got %q vs %q", first.key, second.key)
	}
	if first.key == third.key {
		t.Fatalf("expected different read ranges to produce different fingerprints")
	}
}

func TestGuidanceStringsUseRegisteredToolNames(t *testing.T) {
	monitor := missingRealActionToolGuidance(nil)
	if !strings.Contains(monitor, tools.WriteToolName) || !strings.Contains(monitor, tools.RunCommandToolName) || !strings.Contains(monitor, tools.AppLauncherToolName) {
		t.Fatalf("expected monitor guidance to reference registered tool names, got %q", monitor)
	}
	if strings.Contains(monitor, "create-docx") || strings.Contains(monitor, "browser-use") || strings.Contains(monitor, "open-folder") {
		t.Fatalf("expected monitor guidance to avoid stale tool names, got %q", monitor)
	}

	plan := planExecutionGuidance(nil)
	if !strings.Contains(plan, tools.WriteToolName) || !strings.Contains(plan, tools.PatchToolName) || !strings.Contains(plan, tools.RunCommandToolName) {
		t.Fatalf("expected plan guidance to reference registered tool names, got %q", plan)
	}
	if strings.Contains(plan, "write_file") {
		t.Fatalf("expected plan guidance to avoid stale write_file alias, got %q", plan)
	}
}

func TestMissingRealActionToolGuidanceRespectsSessionAllowlist(t *testing.T) {
	allowlist := map[string]struct{}{
		tools.ManageToolsToolName: {},
		tools.FetchToolName:       {},
		tools.WordCopilotToolName: {},
	}
	text := missingRealActionToolGuidance(allowlist)
	if strings.Contains(text, tools.WriteToolName) || strings.Contains(text, tools.RunCommandToolName) || strings.Contains(text, tools.AppLauncherToolName) {
		t.Fatalf("expected restricted guidance not to mention blocked tools, got %q", text)
	}
	if !strings.Contains(text, tools.WordCopilotToolName) {
		t.Fatalf("expected restricted guidance to mention an allowed action-capable tool, got %q", text)
	}
	if !strings.Contains(text, "allowed in this session") {
		t.Fatalf("expected restricted guidance to mention session allowlist, got %q", text)
	}
}

func TestPlanExecutionGuidanceRespectsSessionAllowlist(t *testing.T) {
	allowlist := map[string]struct{}{
		tools.ReadToolName:        {},
		tools.FetchToolName:       {},
		tools.WordReaderToolName:  {},
		tools.WordCopilotToolName: {},
	}
	text := planExecutionGuidance(allowlist)
	if strings.Contains(text, tools.WriteToolName) || strings.Contains(text, tools.RunCommandToolName) {
		t.Fatalf("expected restricted plan guidance not to mention blocked tools, got %q", text)
	}
	if !strings.Contains(text, tools.WordCopilotToolName) && !strings.Contains(text, tools.ReadToolName) && !strings.Contains(text, tools.FetchToolName) {
		t.Fatalf("expected restricted plan guidance to mention allowed tools, got %q", text)
	}
	if !strings.Contains(text, "allowed in this session") {
		t.Fatalf("expected restricted plan guidance to mention session allowlist, got %q", text)
	}
}

func TestShouldTreatFingerprintSequenceAsSoftLoopForLowQualityRepeats(t *testing.T) {
	fingerprints := []toolTurnFingerprint{
		{names: tools.RunCommandToolName, key: "run=a"},
		{names: tools.RunCommandToolName, key: "run=b"},
		{names: tools.RunCommandToolName, key: "run=c"},
		{names: tools.RunCommandToolName, key: "run=d"},
	}
	if !shouldTreatFingerprintSequenceAsSoftLoop(fingerprints, nil, map[string]int{"generic": 2}) {
		t.Fatalf("expected low-quality repeated tool-name sequence to trigger soft loop detection")
	}
}

func TestShouldTreatFingerprintSequenceAsSoftLoopSkipsReadOnlySequencesWithoutEmptyResults(t *testing.T) {
	fingerprints := []toolTurnFingerprint{
		{names: tools.ReadToolName, key: "read=a"},
		{names: tools.ReadToolName, key: "read=b"},
		{names: tools.ReadToolName, key: "read=c"},
		{names: tools.ReadToolName, key: "read=d"},
	}
	if shouldTreatFingerprintSequenceAsSoftLoop(fingerprints, nil, map[string]int{"generic": 2}) {
		t.Fatalf("expected read-only sequences without empty results to remain handled by dedicated read-loop guards")
	}
}

func TestShouldTreatFingerprintSequenceAsSoftLoopForRepeatedEmptyReadOnlySearches(t *testing.T) {
	fingerprints := []toolTurnFingerprint{
		{names: tools.GrepToolName, key: "grep=a"},
		{names: tools.GrepToolName, key: "grep=b"},
		{names: tools.GrepToolName, key: "grep=c"},
		{names: tools.GrepToolName, key: "grep=d"},
	}
	if !shouldTreatFingerprintSequenceAsSoftLoop(fingerprints, []string{tools.GrepToolName}, nil) {
		t.Fatalf("expected repeated empty read-only searches to trigger soft loop detection")
	}
}

func TestToolCallsContainManagePlanProgress(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []message.ToolCall
		want      bool
	}{
		{
			name:      "update in progress",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"update","id":"abc12345","status":"in_progress"}`}},
			want:      true,
		},
		{
			name:      "update completed",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"update","id":"abc12345","status":"completed"}`}},
			want:      true,
		},
		{
			name:      "complete",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"complete","id":"abc12345"}`}},
			want:      true,
		},
		{
			name:      "archive",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"archive"}`}},
			want:      true,
		},
		{
			name:      "list is not progress",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"list"}`}},
			want:      false,
		},
		{
			name:      "add is not progress",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"add","item":"one"}`}},
			want:      false,
		},
		{
			name:      "update pending is not progress",
			toolCalls: []message.ToolCall{{Name: tools.ManagePlanToolName, Input: `{"action":"update","id":"abc12345","status":"pending"}`}},
			want:      false,
		},
		{
			name:      "other tools are ignored",
			toolCalls: []message.ToolCall{{Name: tools.ReadToolName, Input: `{"file_path":"backend/main.go"}`}},
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolCallsContainManagePlanProgress(tc.toolCalls); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestToolCallsAreAnalysisOnly(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []message.ToolCall
		want      bool
	}{
		{
			name: "multiple read-only calls",
			toolCalls: []message.ToolCall{
				{Name: tools.ReadToolName, Input: `{"file_path":"backend/main.go"}`},
				{Name: tools.GrepToolName, Input: `{"pattern":"agent"}`},
			},
			want: true,
		},
		{
			name:      "read-only shell command",
			toolCalls: []message.ToolCall{{Name: tools.RunCommandToolName, Input: `{"command":"Get-Content backend/main.go"}`}},
			want:      true,
		},
		{
			name: "mixed plan and read batch",
			toolCalls: []message.ToolCall{
				{Name: tools.ManagePlanToolName, Input: `{"action":"update","id":"abc12345","status":"in_progress"}`},
				{Name: tools.ReadToolName, Input: `{"file_path":"backend/main.go"}`},
			},
			want: false,
		},
		{
			name:      "mutating command",
			toolCalls: []message.ToolCall{{Name: tools.RunCommandToolName, Input: `{"command":"go test ./..."}`}},
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolCallsAreAnalysisOnly(tc.toolCalls); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestShouldInjectActivePlanProgressCheckpoint(t *testing.T) {
	if shouldInjectActivePlanProgressCheckpoint(false, maxAnalysisStepsBeforePlanProgressCheckpoint, 0) {
		t.Fatalf("expected no checkpoint without an active plan")
	}
	if shouldInjectActivePlanProgressCheckpoint(true, maxAnalysisStepsBeforePlanProgressCheckpoint-1, 0) {
		t.Fatalf("expected no checkpoint before the threshold")
	}
	if shouldInjectActivePlanProgressCheckpoint(true, maxAnalysisStepsBeforePlanProgressCheckpoint, maxActivePlanProgressCheckpoints) {
		t.Fatalf("expected no checkpoint after the cap is reached")
	}
	if !shouldInjectActivePlanProgressCheckpoint(true, maxAnalysisStepsBeforePlanProgressCheckpoint, maxActivePlanProgressCheckpoints-1) {
		t.Fatalf("expected checkpoint when active-plan analysis stalls")
	}
}

func TestPlanSnapshotNeedsArchive(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     bool
	}{
		{
			name:     "all completed items need archive",
			metadata: `{"items":[{"status":"completed"},{"status":"completed"}]}`,
			want:     true,
		},
		{
			name:     "active item does not need archive",
			metadata: `{"items":[{"status":"completed"},{"status":"in_progress"}]}`,
			want:     false,
		},
		{
			name:     "empty archived plan does not need archive",
			metadata: `{"items":[]}`,
			want:     false,
		},
		{
			name:     "invalid metadata does not need archive",
			metadata: `not-json`,
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := planSnapshotNeedsArchive(tc.metadata); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestPlanSnapshotHasVisibleItems(t *testing.T) {
	if !planSnapshotHasVisibleItems(`{"items":[{"status":"pending"}]}`) {
		t.Fatalf("expected visible plan items")
	}
	if planSnapshotHasVisibleItems(`{"items":[]}`) {
		t.Fatalf("expected empty plan to have no visible items")
	}
	if planSnapshotHasVisibleItems(`not-json`) {
		t.Fatalf("expected invalid metadata to have no visible items")
	}
}

func TestShouldInjectActivePlanCloseoutCheckpoint(t *testing.T) {
	if shouldInjectActivePlanCloseoutCheckpoint(false, false) {
		t.Fatalf("expected no checkpoint when archive is not needed")
	}
	if shouldInjectActivePlanCloseoutCheckpoint(true, true) {
		t.Fatalf("expected no second closeout checkpoint")
	}
	if !shouldInjectActivePlanCloseoutCheckpoint(true, false) {
		t.Fatalf("expected closeout checkpoint when archive is pending")
	}
}

func TestShouldInjectActivePlanReconciliationCheckpoint(t *testing.T) {
	if shouldInjectActivePlanReconciliationCheckpoint(false, false) {
		t.Fatalf("expected no reconciliation checkpoint without visible items")
	}
	if shouldInjectActivePlanReconciliationCheckpoint(true, true) {
		t.Fatalf("expected no second reconciliation checkpoint")
	}
	if !shouldInjectActivePlanReconciliationCheckpoint(true, false) {
		t.Fatalf("expected reconciliation checkpoint when active items are visible")
	}
}

func TestShouldCountCorrectionVerificationTurnRequiresSuccessfulNonMutatingTools(t *testing.T) {
	state := &sessionTaskState{CorrectionMode: true}
	successResults := &message.Message{Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "call-1", Name: "run_command", Content: "ok"},
	}}

	if !shouldCountCorrectionVerificationTurn(state, successResults) {
		t.Fatalf("expected successful non-mutating correction verification to count")
	}
	if shouldCountCorrectionVerificationTurn(state, &message.Message{Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "call-1", Name: "edit", Content: "ok"},
	}}) {
		t.Fatalf("expected mutating tools not to count as correction verification")
	}
	if shouldCountCorrectionVerificationTurn(state, &message.Message{Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "call-1", Name: "run_command", Content: "failed", IsError: true},
	}}) {
		t.Fatalf("expected failed verification not to count")
	}
}

func TestUpdateCorrectionVerificationStateResetsAfterMutation(t *testing.T) {
	state := &sessionTaskState{CorrectionMode: true, CorrectionVerificationTurns: 2}
	updateCorrectionVerificationState(state, &message.Message{Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "call-1", Name: "edit", Content: "ok"},
	}})
	if state.CorrectionVerificationTurns != 0 {
		t.Fatalf("expected mutating turn to reset verification count, got %d", state.CorrectionVerificationTurns)
	}
}

func TestBuildCorrectionVerificationHandoffMessageIsFinished(t *testing.T) {
	msg := buildCorrectionVerificationHandoffMessage()
	if !msg.IsFinished() {
		t.Fatalf("expected correction verification handoff to be finished")
	}
	if msg.FinishPart() == nil || msg.FinishPart().Reason != message.FinishReasonEndTurn {
		t.Fatalf("expected correction verification handoff to end the turn")
	}
}

func TestCorrectionVerificationHandoffTextIsRecognized(t *testing.T) {
	msg := buildCorrectionVerificationHandoffMessage()
	if !isCorrectionVerificationHandoffText(msg.Content().Text) {
		t.Fatalf("expected correction verification handoff text to be recognized")
	}
}

func TestUpdateTaskStateFromAssistantMessageKeepsCorrectionHandoffWaiting(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	sessionID := "session-correction-handoff"
	initialState := &sessionTaskState{CorrectionMode: true, Status: "active"}
	payload, err := json.Marshal(initialState)
	if err != nil {
		t.Fatalf("marshal initial state: %v", err)
	}
	if _, err := agentUnderTest.q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{
		Key:       taskStateKey(sessionID),
		Value:     sql.NullString{String: string(payload), Valid: true},
		UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("persist initial state: %v", err)
	}

	if err := agentUnderTest.updateTaskStateFromAssistantMessage(context.Background(), sessionID, buildCorrectionVerificationHandoffMessage()); err != nil {
		t.Fatalf("update task state: %v", err)
	}

	got, err := agentUnderTest.loadTaskState(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if got == nil {
		t.Fatalf("expected task state")
	}
	if got.Status != "waiting_user" {
		t.Fatalf("expected waiting_user status for correction handoff, got %q", got.Status)
	}
}

func (p *capturingSummaryProvider) requestsSnapshot() [][]message.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([][]message.Message, 0, len(p.messages))
	for _, request := range p.messages {
		result = append(result, append([]message.Message(nil), request...))
	}
	return result
}

func (p *capturingSummaryProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

type orderedMessageService struct {
	*pubsub.Broker[message.Message]
	mu       sync.Mutex
	messages []message.Message
	byID     map[string]message.Message
	nextID   int
}

type scopedSearchMessageService struct {
	*pubsub.Broker[message.Message]
	mu                 sync.Mutex
	sessionResults     map[string][]message.Message
	sessionWindows     map[string][]message.Message
	globalResults      []message.Message
	sessionSearches    []string
	windowSearches     []string
	globalSearchCalls  int
	sessionSearchDelay time.Duration
}

type failingWindowMessageService struct {
	*orderedMessageService
	listWindowErr   error
	listCalls       int
	listWindowCalls int
}

func newOrderedMessageService(messages ...message.Message) *orderedMessageService {
	svc := &orderedMessageService{
		Broker:   pubsub.NewBroker[message.Message](),
		byID:     make(map[string]message.Message, len(messages)),
		messages: make([]message.Message, 0, len(messages)),
		nextID:   len(messages),
	}
	for _, msg := range messages {
		svc.messages = append(svc.messages, msg)
		svc.byID[msg.ID] = msg
	}
	return svc
}

func newScopedSearchMessageService() *scopedSearchMessageService {
	return &scopedSearchMessageService{
		Broker:         pubsub.NewBroker[message.Message](),
		sessionResults: make(map[string][]message.Message),
		sessionWindows: make(map[string][]message.Message),
	}
}

func newFailingWindowMessageService(listWindowErr error, messages ...message.Message) *failingWindowMessageService {
	return &failingWindowMessageService{
		orderedMessageService: newOrderedMessageService(messages...),
		listWindowErr:         listWindowErr,
	}
}

func (s *scopedSearchMessageService) Create(_ context.Context, _ string, _ message.CreateMessageParams) (message.Message, error) {
	return message.Message{}, fmt.Errorf("not implemented")
}

func (s *scopedSearchMessageService) Update(_ context.Context, _ message.Message) error {
	return fmt.Errorf("not implemented")
}

func (s *scopedSearchMessageService) Get(_ context.Context, _ string) (message.Message, error) {
	return message.Message{}, fmt.Errorf("not implemented")
}

func (s *scopedSearchMessageService) List(_ context.Context, _ string) ([]message.Message, error) {
	return nil, nil
}

func (s *scopedSearchMessageService) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func (s *scopedSearchMessageService) DeleteSessionMessages(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func (s *scopedSearchMessageService) Search(_ context.Context, _ string, _ int) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalSearchCalls++
	return append([]message.Message(nil), s.globalResults...), nil
}

func (s *scopedSearchMessageService) SearchInSession(_ context.Context, sessionID string, _ string, _ int) ([]message.Message, error) {
	s.mu.Lock()
	s.sessionSearches = append(s.sessionSearches, sessionID)
	delay := s.sessionSearchDelay
	results := append([]message.Message(nil), s.sessionResults[sessionID]...)
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return results, nil
}

func (s *scopedSearchMessageService) ListWindow(_ context.Context, sessionID string, _ string, _ int) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowSearches = append(s.windowSearches, sessionID)
	return append([]message.Message(nil), s.sessionWindows[sessionID]...), nil
}

func (s *orderedMessageService) Create(_ context.Context, sessionID string, params message.CreateMessageParams) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	msg := message.Message{
		ID:        fmt.Sprintf("ordered-msg-%d", s.nextID),
		SessionID: sessionID,
		Role:      params.Role,
		Parts:     append([]message.ContentPart(nil), params.Parts...),
		Model:     params.Model,
	}
	s.messages = append(s.messages, msg)
	s.byID[msg.ID] = msg
	return msg, nil
}

func (s *orderedMessageService) Update(_ context.Context, msg message.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if s.messages[i].ID == msg.ID {
			s.messages[i] = msg
			s.byID[msg.ID] = msg
			return nil
		}
	}
	return fmt.Errorf("message %s not found", msg.ID)
}

func (s *orderedMessageService) Get(_ context.Context, id string) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.byID[id]
	if !ok {
		return message.Message{}, fmt.Errorf("message %s not found", id)
	}
	return msg, nil
}

func (s *orderedMessageService) List(_ context.Context, sessionID string) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]message.Message, 0, len(s.messages))
	for _, msg := range s.messages {
		if msg.SessionID == sessionID {
			result = append(result, msg)
		}
	}
	return result, nil
}

func (s *orderedMessageService) ListWindow(ctx context.Context, sessionID string, _ string, _ int) ([]message.Message, error) {
	return s.List(ctx, sessionID)
}

func (s *orderedMessageService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
	filtered := s.messages[:0]
	for _, msg := range s.messages {
		if msg.ID != id {
			filtered = append(filtered, msg)
		}
	}
	s.messages = filtered
	return nil
}

func (s *orderedMessageService) DeleteSessionMessages(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.messages[:0]
	for _, msg := range s.messages {
		if msg.SessionID == sessionID {
			delete(s.byID, msg.ID)
			continue
		}
		filtered = append(filtered, msg)
	}
	s.messages = filtered
	return nil
}

func (s *orderedMessageService) Search(_ context.Context, _ string, _ int) ([]message.Message, error) {
	return nil, nil
}

func (s *orderedMessageService) SearchInSession(_ context.Context, _ string, _ string, _ int) ([]message.Message, error) {
	return nil, nil
}

func (s *failingWindowMessageService) List(_ context.Context, sessionID string) ([]message.Message, error) {
	s.listCalls++
	return s.orderedMessageService.List(context.Background(), sessionID)
}

func (s *failingWindowMessageService) ListWindow(_ context.Context, _ string, _ string, _ int) ([]message.Message, error) {
	s.listWindowCalls++
	return nil, s.listWindowErr
}

type summarizeTestSessionService struct {
	*pubsub.Broker[session.Session]
	mu      sync.Mutex
	session session.Session
}

func newSummarizeTestSessionService(sess session.Session) *summarizeTestSessionService {
	return &summarizeTestSessionService{Broker: pubsub.NewBroker[session.Session](), session: sess}
}

func (s *summarizeTestSessionService) Create(ctx context.Context, title string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("not implemented")
}

func (s *summarizeTestSessionService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("not implemented")
}

func (s *summarizeTestSessionService) Get(ctx context.Context, id string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session.ID != id {
		return session.Session{}, fmt.Errorf("session %s not found", id)
	}
	return s.session, nil
}

func (s *summarizeTestSessionService) List(ctx context.Context) ([]session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []session.Session{s.session}, nil
}

func (s *summarizeTestSessionService) Save(ctx context.Context, sess session.Session) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = sess
	return sess, nil
}

func (s *summarizeTestSessionService) Delete(ctx context.Context, id string) error {
	return nil
}

type multiSessionTestService struct {
	*pubsub.Broker[session.Session]
	mu       sync.Mutex
	sessions map[string]session.Session
}

func newMultiSessionTestService(sessions ...session.Session) *multiSessionTestService {
	items := make(map[string]session.Session, len(sessions))
	for _, sess := range sessions {
		items[sess.ID] = sess
	}
	return &multiSessionTestService{Broker: pubsub.NewBroker[session.Session](), sessions: items}
}

func (s *multiSessionTestService) Create(ctx context.Context, title string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("not implemented")
}

func (s *multiSessionTestService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("not implemented")
}

func (s *multiSessionTestService) Get(ctx context.Context, id string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return session.Session{}, fmt.Errorf("session %s not found", id)
	}
	return sess, nil
}

func (s *multiSessionTestService) List(ctx context.Context) ([]session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]session.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		result = append(result, sess)
	}
	return result, nil
}

func (s *multiSessionTestService) Save(ctx context.Context, sess session.Session) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = sess
	return sess, nil
}

func (s *multiSessionTestService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

func loadEmptyAgentTestConfig(t *testing.T) {
	t.Helper()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, ".auramate.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	if _, err := config.Load(configDir, false); err != nil {
		t.Fatalf("load test config: %v", err)
	}
}

func (t stubAgentTool) Info() tools.ToolInfo {
	return t.info
}

func (t stubAgentTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	return tools.NewTextResponse("ok"), nil
}

func TestPrepareConversationContextDoesNotReinsertFirstUserMessage(t *testing.T) {
	agentInstance := &agent{
		provider: stubContextProvider{model: models.Model{ContextWindow: 128000}},
	}

	makeLongText := func(prefix string, repeat int) string {
		return prefix + strings.Repeat("x", repeat)
	}

	history := []message.Message{message.NewTextMessage(message.User, makeLongText("first-user:", 4000))}
	for index := 0; index < 140; index++ {
		history = append(history, message.NewTextMessage(message.Assistant, makeLongText(fmt.Sprintf("assistant-%02d:", index), 20000)))
	}
	history = append(history, message.NewTextMessage(message.User, makeLongText("latest-user:", 12000)))

	prepared, err := agentInstance.prepareConversationContext(context.Background(), "session-test", history)
	if err != nil {
		t.Fatalf("prepareConversationContext returned error: %v", err)
	}

	if len(prepared) == 0 {
		t.Fatalf("expected prepared context to keep recent messages")
	}

	for _, msg := range prepared {
		if msg.Role != message.User {
			continue
		}
		if strings.Contains(msg.Content().String(), "first-user:") {
			t.Fatalf("expected oldest user message to be truncated from prepared context, but it was reinserted at the front")
		}
	}

	lastMsg := prepared[len(prepared)-1]
	if lastMsg.Role != message.User || !strings.Contains(lastMsg.Content().String(), "latest-user:") {
		t.Fatalf("expected latest user message to remain at the tail of prepared context")
	}

	if prepared[0].Role == message.User && strings.Contains(prepared[0].Content().String(), "first-user:") {
		t.Fatalf("expected prepared context head to stop forcing the oldest user message")
	}
}

func TestPrepareConversationContextPreservesStructuredToolResultWhenColdFolded(t *testing.T) {
	agentInstance := &agent{
		provider: stubContextProvider{model: models.Model{ContextWindow: 128000}},
	}

	toolPayload := map[string]any{
		"document":          map[string]any{"name": "reader.docx"},
		"full_text_excerpt": strings.Repeat("证据内容", 8000),
		"exploration": map[string]any{
			"paragraph": map[string]any{
				"paragraph_id": "p-1",
				"text":         strings.Repeat("命中上下文", 800),
			},
		},
		"query": map[string]any{
			"hits": []any{map[string]any{"preview": "总计", "paragraph_id": "p-1"}},
		},
		"session": map[string]any{"id": "session-reader"},
	}
	contentBytes, err := json.Marshal(toolPayload)
	if err != nil {
		t.Fatalf("marshal tool payload: %v", err)
	}

	history := []message.Message{
		message.NewTextMessage(message.System, "system prompt"),
		{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				Name:    "auramate-word-reader",
				Content: string(contentBytes),
			}},
		},
		message.NewTextMessage(message.Assistant, strings.Repeat("assistant-hot-context-1", 2000)),
		message.NewTextMessage(message.Assistant, strings.Repeat("assistant-hot-context-2", 2000)),
		message.NewTextMessage(message.Assistant, strings.Repeat("assistant-hot-context-3", 2000)),
		message.NewTextMessage(message.Assistant, strings.Repeat("assistant-hot-context-4", 2000)),
		message.NewTextMessage(message.User, "latest-user"),
	}

	prepared, err := agentInstance.prepareConversationContext(context.Background(), "session-json-fold", history)
	if err != nil {
		t.Fatalf("prepareConversationContext returned error: %v", err)
	}

	var foldedContent string
	for _, msg := range prepared {
		if msg.Role != message.Tool {
			continue
		}
		for _, part := range msg.Parts {
			if result, ok := part.(message.ToolResult); ok && result.Name == "auramate-word-reader" {
				foldedContent = result.Content
			}
		}
	}
	if foldedContent == "" {
		t.Fatalf("expected folded tool result to remain in prepared history")
	}

	var folded map[string]any
	if err := json.Unmarshal([]byte(foldedContent), &folded); err != nil {
		t.Fatalf("expected folded tool result to remain valid JSON, got %v with content %q", err, foldedContent)
	}
	exploration, ok := folded["exploration"].(map[string]any)
	if !ok {
		t.Fatalf("expected exploration field to survive structured folding, got %#v", folded["exploration"])
	}
	paragraph, ok := exploration["paragraph"].(map[string]any)
	if !ok || paragraph["paragraph_id"] != "p-1" {
		t.Fatalf("expected paragraph evidence to survive structured folding, got %#v", exploration["paragraph"])
	}
	sessionPayload, ok := folded["session"].(map[string]any)
	if !ok || sessionPayload["id"] != "session-reader" {
		t.Fatalf("expected session info to survive structured folding, got %#v", folded["session"])
	}
}

func TestPrepareConversationContextKeepsLargeReadToolResultWhenHot(t *testing.T) {
	agentInstance := &agent{
		provider: stubContextProvider{model: models.Model{ContextWindow: 256000}},
	}

	largeRead := strings.Repeat("line-content-", 9000)
	originalContent := "<file_content>\n" + largeRead + "\n</file_content>"
	history := []message.Message{
		message.NewTextMessage(message.System, "system prompt"),
		message.NewTextMessage(message.User, "请读取并继续处理"),
		{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				Name:    "read",
				Content: originalContent,
			}},
		},
		message.NewTextMessage(message.Assistant, "我已读取，准备继续"),
		message.NewTextMessage(message.User, "继续"),
	}

	prepared, err := agentInstance.prepareConversationContext(context.Background(), "session-hot-read-fold", history)
	if err != nil {
		t.Fatalf("prepareConversationContext returned error: %v", err)
	}

	var foldedContent string
	for _, msg := range prepared {
		if msg.Role != message.Tool {
			continue
		}
		for _, part := range msg.Parts {
			if result, ok := part.(message.ToolResult); ok && result.Name == "read" {
				foldedContent = result.Content
			}
		}
	}

	if foldedContent == "" {
		t.Fatalf("expected read tool result to remain in prepared history")
	}

	if foldedContent != originalContent {
		t.Fatalf("expected hot read output to stay intact for current-turn reasoning")
	}

	if strings.Contains(foldedContent, "[System: Content Folded]") {
		t.Fatalf("expected no folded marker for hot read output")
	}
}

func TestPrepareConversationContextDroppedThinkingDoesNotConsumeHotProtection(t *testing.T) {
	agentInstance := &agent{
		provider: stubContextProvider{model: models.Model{ContextWindow: 256000}},
	}

	largeRead := strings.Repeat("line-content-", 9000)
	originalContent := "<file_content>\n" + largeRead + "\n</file_content>"
	history := []message.Message{
		message.NewTextMessage(message.System, "system prompt"),
		message.NewTextMessage(message.User, "read and continue"),
		{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				Name:    "read",
				Content: originalContent,
			}},
		},
	}
	for i := 0; i < MinHotMessages+5; i++ {
		history = append(history, message.NewTextMessage(message.Assistant, "<thinking>private scratchpad</thinking>"))
	}
	history = append(history, message.NewTextMessage(message.User, "continue"))

	prepared, err := agentInstance.prepareConversationContext(context.Background(), "session-hot-read-thinking", history)
	if err != nil {
		t.Fatalf("prepareConversationContext returned error: %v", err)
	}

	var foldedContent string
	for _, msg := range prepared {
		if msg.Role != message.Tool {
			continue
		}
		for _, part := range msg.Parts {
			if result, ok := part.(message.ToolResult); ok && result.Name == "read" {
				foldedContent = result.Content
			}
		}
	}

	if foldedContent != originalContent {
		t.Fatalf("expected dropped thinking turns not to consume hot protection")
	}
}

func TestGetProviderForRequestRejectsExplicitModelFallback(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	agentInstance := &agent{
		provider: stubContextProvider{model: models.Model{ID: models.ModelID("custom-main-model")}},
	}

	ctx := context.WithValue(context.Background(), tools.ModelOverrideCandidatesContextKey, []string{"kimi-k2.5"})
	ctx = context.WithValue(ctx, tools.ModelOverrideContextKey, "kimi-k2.5")

	resolved, err := agentInstance.getProviderForRequest(ctx, nil)
	if err == nil {
		t.Fatalf("expected explicit model fallback to fail")
	}
	if resolved != nil {
		t.Fatalf("expected no provider on explicit failure, got %#v", resolved)
	}
	if !strings.Contains(err.Error(), "已阻止回退到默认模型") {
		t.Fatalf("expected strict fallback error, got %v", err)
	}
}

func TestGetProviderForRequestKeepsAutoFallback(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	defaultProvider := stubContextProvider{model: models.Model{ID: models.ModelID("custom-main-model")}}
	agentInstance := &agent{provider: defaultProvider}

	ctx := context.WithValue(context.Background(), tools.ModelOverrideCandidatesContextKey, []string{"kimi-k2.5"})

	resolved, err := agentInstance.getProviderForRequest(ctx, nil)
	if err != nil {
		t.Fatalf("expected auto fallback to keep default provider, got %v", err)
	}
	if resolved == nil {
		t.Fatalf("expected resolved provider")
	}
	if resolved.Model().ID != defaultProvider.Model().ID {
		t.Fatalf("expected default provider to remain active, got %s", resolved.Model().ID)
	}
}

func TestGetProviderForRequestPrefersVisualProviderForAttachments(t *testing.T) {
	agentInstance := &agent{
		provider:       stubContextProvider{model: models.Model{ID: models.ModelID("qwen3.5-plus"), SupportsAttachments: true}},
		visualProvider: stubContextProvider{model: models.Model{ID: models.ModelID("ark-code-latest"), SupportsAttachments: true}},
	}

	resolved, err := agentInstance.getProviderForRequest(context.Background(), []message.Attachment{{FileName: "image.png"}})
	if err != nil {
		t.Fatalf("expected attachment request to resolve visual provider, got %v", err)
	}
	if resolved == nil {
		t.Fatalf("expected resolved provider")
	}
	if resolved.Model().ID != models.ModelID("ark-code-latest") {
		t.Fatalf("expected visual provider for attachments, got %s", resolved.Model().ID)
	}
}

func TestGetProviderForRequestPrefersVisualProviderForImageAttachmentsWithModelOverride(t *testing.T) {
	agentInstance := &agent{
		provider:       stubContextProvider{model: models.Model{ID: models.ModelID("qwen3.5-plus"), SupportsAttachments: true}},
		visualProvider: stubContextProvider{model: models.Model{ID: models.DoubaoSeed20Pro, SupportsAttachments: true}},
	}
	ctx := context.WithValue(context.Background(), tools.ModelOverrideCandidatesContextKey, []string{"kimi-k2.5"})
	ctx = context.WithValue(ctx, tools.ModelOverrideContextKey, "kimi-k2.5")

	resolved, err := agentInstance.getProviderForRequest(ctx, []message.Attachment{{FileName: "image.png"}})
	if err != nil {
		t.Fatalf("expected image attachment to bypass text model override, got %v", err)
	}
	if resolved == nil || resolved.Model().ID != models.DoubaoSeed20Pro {
		t.Fatalf("expected doubao visual provider for image attachment, got %#v", resolved)
	}
}

func TestGetProviderForRequestKeepsDefaultProviderForNonImageAttachments(t *testing.T) {
	defaultProvider := stubContextProvider{model: models.Model{ID: models.ModelID("qwen3.5-plus"), SupportsAttachments: true}}
	agentInstance := &agent{
		provider:       defaultProvider,
		visualProvider: stubContextProvider{model: models.Model{ID: models.DoubaoSeed20Pro, SupportsAttachments: true}},
	}

	resolved, err := agentInstance.getProviderForRequest(context.Background(), []message.Attachment{{FileName: "notes.txt"}})
	if err != nil {
		t.Fatalf("expected non-image attachment to use default provider, got %v", err)
	}
	if resolved == nil || resolved.Model().ID != defaultProvider.Model().ID {
		t.Fatalf("expected default provider for non-image attachment, got %#v", resolved)
	}
}

func TestBuildSummarySystemPrompt(t *testing.T) {
	t.Run("normal session merges previous summary", func(t *testing.T) {
		prompt := buildSummarySystemPrompt("session-1", "prior summary")
		if !strings.Contains(prompt, "PREVIOUS SUMMARY") {
			t.Fatalf("expected previous summary marker in prompt")
		}
		if !strings.Contains(prompt, "prior summary") {
			t.Fatalf("expected previous summary content to be preserved")
		}
	})

	t.Run("proactive session uses stream prompt only", func(t *testing.T) {
		prompt := buildSummarySystemPrompt("aura-proactive-internal", "prior summary")
		if prompt != StreamSummarizePrompt {
			t.Fatalf("expected proactive prompt to use stream summarize prompt")
		}
		if strings.Contains(prompt, "PREVIOUS SUMMARY") {
			t.Fatalf("did not expect previous summary marker in proactive prompt")
		}
	})
}

func TestDetachedSummaryContextPreservesRequestModelOverride(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, tools.ModelOverrideCandidatesContextKey, []string{"custom-main-model"})
	ctx = context.WithValue(ctx, tools.ModelOverrideContextKey, "custom-main-model")
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, "source-session")
	cancel()

	summaryCtx := detachedSummaryContext(ctx, "target-session")

	if err := summaryCtx.Err(); err != nil {
		t.Fatalf("expected detached summary context to ignore parent cancellation, got %v", err)
	}
	if got := tools.GetRequestKind(summaryCtx); got != "summarization" {
		t.Fatalf("expected summarization request kind, got %q", got)
	}
	if got, _ := summaryCtx.Value(tools.ModelOverrideContextKey).(string); got != "custom-main-model" {
		t.Fatalf("expected explicit model override to survive, got %q", got)
	}
	if got, _ := summaryCtx.Value(tools.ModelOverrideCandidatesContextKey).([]string); strings.Join(got, ",") != "custom-main-model" {
		t.Fatalf("expected model candidate override to survive, got %v", got)
	}
	if got, _ := summaryCtx.Value(tools.SessionIDContextKey).(string); got != "target-session" {
		t.Fatalf("expected summary session id override, got %q", got)
	}
}

func TestSummarizeRejectsConcurrentRun(t *testing.T) {
	sessionID := "session-concurrent-summary"
	memories, _ := newAgentMemoryTestService(t)
	startCh := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	provider := &capturingSummaryProvider{
		content:   "summary",
		startedCh: startCh,
		releaseCh: releaseCh,
	}
	msgService := newOrderedMessageService()
	for i := 0; i < 12; i++ {
		role := message.Assistant
		text := fmt.Sprintf("assistant-%02d", i)
		if i == 0 {
			role = message.User
			text = "first user requirement"
		}
		msgService.messages = append(msgService.messages, message.Message{
			ID:        fmt.Sprintf("msg-%02d", i),
			SessionID: sessionID,
			Role:      role,
			Parts:     []message.ContentPart{message.TextContent{Text: text}},
		})
		msgService.byID[fmt.Sprintf("msg-%02d", i)] = msgService.messages[len(msgService.messages)-1]
	}
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		memories:          memories,
		messages:          msgService,
		sessions:          newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "test"}),
		summarizeProvider: provider,
		provider:          provider,
	}

	firstErrCh := make(chan error, 1)
	go func() {
		firstErrCh <- a.Summarize(context.Background(), sessionID)
	}()

	select {
	case <-startCh:
	case <-time.After(2 * time.Second):
		close(releaseCh)
		t.Fatalf("timed out waiting for first summarization to start")
	}

	if err := a.Summarize(context.Background(), sessionID); !errors.Is(err, ErrSummarizationInProgress) {
		close(releaseCh)
		t.Fatalf("expected ErrSummarizationInProgress, got %v", err)
	}

	close(releaseCh)
	if err := <-firstErrCh; err != nil {
		t.Fatalf("expected first summarization to complete, got %v", err)
	}
}

func TestSummarizePreservesFirstUserMessageWhenCutoffExcludesIt(t *testing.T) {
	sessionID := "session-preserve-first-user"
	memories, _ := newAgentMemoryTestService(t)
	provider := &capturingSummaryProvider{content: "condensed state"}
	updatedAt := time.Now().UnixMilli()
	messages := make([]message.Message, 0, 45)
	for i := 0; i < 45; i++ {
		role := message.Assistant
		text := fmt.Sprintf("assistant-step-%02d", i)
		if i == 0 {
			role = message.User
			text = "FIRST USER INSTRUCTION: only update summary logic"
		} else if i == 44 {
			role = message.User
			text = "latest user follow-up"
		}
		messages = append(messages, message.Message{
			ID:        fmt.Sprintf("msg-%02d", i),
			SessionID: sessionID,
			Role:      role,
			UpdatedAt: updatedAt + int64(i),
			Parts:     []message.ContentPart{message.TextContent{Text: text}},
		})
	}
	msgService := newOrderedMessageService(messages...)
	sessionSvc := newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "summary test"})
	_, conn := newAgentMemoryTestService(t)
	q := db.New(conn)
	state := &sessionTaskState{
		PrimaryGoal:       "让摘要主链产出结构化 checkpoint",
		CurrentFocus:      "摘要 authority checkpoint",
		Status:            "active",
		RecentProgress:    []string{"已完成现状定位", "确定从 sessionTaskState 派生 checkpoint"},
		OutstandingIssues: []string{"worker checkpoint 仍待落地"},
		NextStep:          "把 checkpoint 注入 summary message",
		UpdatedAt:         updatedAt,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if _, err := q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{Key: taskStateKey(sessionID), Value: sql.NullString{String: string(payload), Valid: true}, UpdatedAt: updatedAt}); err != nil {
		t.Fatalf("persist task state: %v", err)
	}
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		memories:          memories,
		messages:          msgService,
		sessions:          sessionSvc,
		summarizeProvider: provider,
		provider:          provider,
		q:                 q,
	}

	if err := a.Summarize(context.Background(), sessionID); err != nil {
		t.Fatalf("summarize returned error: %v", err)
	}

	requests := provider.requestsSnapshot()
	foundPrompt := false
	for _, request := range requests {
		if len(request) != 2 {
			continue
		}
		userPrompt := request[1].Content().String()
		if strings.Contains(userPrompt, "FIRST USER INSTRUCTION: only update summary logic") && strings.Contains(userPrompt, "Authoritative Task Checkpoint:") && strings.Contains(userPrompt, "- Goal: 让摘要主链产出结构化 checkpoint") {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		t.Fatalf("expected one summarize prompt to preserve first user message, got %#v", requests)
	}
	if sessionSvc.session.SummaryMessageID == "" {
		t.Fatalf("expected summary message id to be saved back to session")
	}
	summaryMsg, err := msgService.Get(context.Background(), sessionSvc.session.SummaryMessageID)
	if err != nil {
		t.Fatalf("get summary message: %v", err)
	}
	if got := normalizeSessionSummaryText(summaryMsg.Content().String()); got != "condensed state" {
		t.Fatalf("expected normalized summary text, got %q", got)
	}
	taskCheckpoint, ok := parseSummaryTaskCheckpoint(summaryMsg.Content().String())
	if !ok || taskCheckpoint == nil || taskCheckpoint.State == nil {
		t.Fatalf("expected task checkpoint in summary message, got %q", summaryMsg.Content().String())
	}
	if taskCheckpoint.State.PrimaryGoal != state.PrimaryGoal {
		t.Fatalf("expected primary goal in task checkpoint, got %#v", taskCheckpoint.State)
	}
	sessionCheckpoint, ok := parseSummarySessionCheckpoint(summaryMsg.Content().String())
	if !ok || sessionCheckpoint == nil {
		t.Fatalf("expected session checkpoint in summary message, got %q", summaryMsg.Content().String())
	}
	if sessionCheckpoint.SessionID != sessionID || sessionCheckpoint.Summary != "condensed state" {
		t.Fatalf("unexpected session checkpoint: %#v", sessionCheckpoint)
	}
	pseudo, ok := buildSessionSummaryPseudoMemory(sessionSvc.session, summaryMsg)
	if !ok {
		t.Fatalf("expected pseudo memory to be built from checkpoint summary")
	}
	if strings.Contains(pseudo.Content, "Primary goal: "+state.PrimaryGoal) {
		t.Fatalf("expected continuity pseudo memory to exclude current task checkpoint state, got %q", pseudo.Content)
	}
}

func TestCanAutoSummarizeFallsBackToMainProvider(t *testing.T) {
	a := &agent{provider: &capturingSummaryProvider{content: "summary"}}
	if !a.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected auto summarize to be available when main provider exists")
	}

	a.activeRequests.Store("session-auto-summary-summarize", context.CancelFunc(func() {}))
	defer a.activeRequests.Delete("session-auto-summary-summarize")
	if a.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected auto summarize to be blocked while summarization is already running")
	}

	b := &agent{}
	if b.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected auto summarize to be unavailable without any provider")
	}
}

func TestCanAutoSummarizeRespectsFailureCooldown(t *testing.T) {
	a := &agent{provider: &capturingSummaryProvider{content: "summary"}}
	state := &SummarizationState{
		failureCount:    2,
		lastAttemptTime: time.Now(),
		backoffDuration: 4 * time.Second,
	}
	a.sessionSummarizeState.Store("session-auto-summary", state)

	if a.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected cooldown to suppress auto summarize while retry window is active")
	}

	state.lastAttemptTime = time.Now().Add(-5 * time.Second)
	if !a.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected auto summarize to resume after cooldown expires")
	}
}

func TestRecordSummarizationFallbackKeepsCooldownActive(t *testing.T) {
	a := &agent{provider: &capturingSummaryProvider{content: "summary"}}
	state := a.getOrCreateSummarizationState("session-auto-summary")
	recordSummarizationFallback(state, time.Now())

	if a.canAutoSummarize("session-auto-summary") {
		t.Fatalf("expected fallback cooldown to suppress immediate auto summarize retry")
	}

	snapshot := snapshotSummarizationState(state)
	if snapshot.failureCount != 1 {
		t.Fatalf("expected fallback cooldown to reset failure count to 1, got %d", snapshot.failureCount)
	}
	if snapshot.backoffDuration != summarizationFallbackCooldown {
		t.Fatalf("expected fallback cooldown %v, got %v", summarizationFallbackCooldown, snapshot.backoffDuration)
	}
}

func TestBuildHardLimitFallbackHistoryPreservesPromptAndTruncates(t *testing.T) {
	sessionID := "session-hard-limit-fallback"
	msgService := newOrderedMessageService(
		message.Message{ID: "msg-1", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: strings.Repeat("a", 20)}}},
		message.Message{ID: "msg-2", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: strings.Repeat("b", 20)}}},
		message.Message{ID: "msg-3", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: strings.Repeat("c", 20)}}},
	)
	a := &agent{
		messages: msgService,
		sessions: newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "test"}),
	}

	fallback := a.buildHardLimitFallbackHistory(context.Background(), sessionID, "STATIC SYSTEM PROMPT", 12)
	if len(fallback) == 0 {
		t.Fatalf("expected fallback history to keep at least one message")
	}
	if fallback[0].Role != message.System || !strings.Contains(fallback[0].Content().String(), "STATIC SYSTEM PROMPT") {
		t.Fatalf("expected static system prompt to be rebuilt, got %#v", fallback[0])
	}
	if len(fallback) >= 4 {
		t.Fatalf("expected fallback history to truncate old messages, got %d entries", len(fallback))
	}
}

func TestSyncSummaryRecoveryTimeoutForModel(t *testing.T) {
	tests := []struct {
		name  string
		model models.Model
		want  time.Duration
	}{
		{
			name:  "regular summary model uses short timeout",
			model: models.Model{ID: models.ModelID("summary-fast"), CanReason: false},
			want:  syncSummaryRecoveryTimeout,
		},
		{
			name:  "reasoning summary model uses extended timeout",
			model: models.Model{ID: models.ModelID("summary-reasoner"), CanReason: true},
			want:  reasoningSyncSummaryRecoveryTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncSummaryRecoveryTimeoutForModel(tt.model); got != tt.want {
				t.Fatalf("syncSummaryRecoveryTimeoutForModel(%+v) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestSummarizeWithTimeoutCancelsSlowSummary(t *testing.T) {
	sessionID := "session-summary-timeout"
	releaseCh := make(chan struct{})
	messages := []message.Message{
		{ID: "msg-1", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "用户消息 1"}}},
		{ID: "msg-2", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "助手消息 2"}}},
		{ID: "msg-3", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "用户消息 3"}}},
		{ID: "msg-4", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "助手消息 4"}}},
		{ID: "msg-5", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "用户消息 5"}}},
		{ID: "msg-6", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "助手消息 6"}}},
		{ID: "msg-7", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "用户消息 7"}}},
		{ID: "msg-8", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "助手消息 8"}}},
		{ID: "msg-9", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "用户消息 9"}}},
		{ID: "msg-10", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "助手消息 10"}}},
	}
	a := &agent{
		summarizeProvider: &capturingSummaryProvider{releaseCh: releaseCh},
		messages:          newOrderedMessageService(messages...),
		sessions:          newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "timeout"}),
	}

	start := time.Now()
	err := a.summarizeWithTimeout(context.Background(), sessionID, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected timeout-bounded summary to return quickly, got %v", elapsed)
	}
	if _, busy := a.activeRequests.Load(sessionID + "-summarize"); busy {
		t.Fatalf("expected summarize busy flag to be cleared after timeout")
	}
}

func TestTriggerTreeSummarizationHonorsRunningAndCooldown(t *testing.T) {
	msgs := make([]message.Message, 0, 11)
	sessions := make([]session.Session, 0, 11)
	for i := 0; i < 11; i++ {
		sessionID := fmt.Sprintf("session-%02d", i)
		summaryID := fmt.Sprintf("summary-%02d", i)
		msgs = append(msgs, message.Message{
			ID:        summaryID,
			SessionID: sessionID,
			Role:      message.User,
			Parts:     []message.ContentPart{message.TextContent{Text: fmt.Sprintf("[System Summary]: summary-%02d", i)}},
			CreatedAt: int64(i + 1),
			UpdatedAt: int64(i + 1),
		})
		sessions = append(sessions, session.Session{
			ID:               sessionID,
			Title:            fmt.Sprintf("Session %02d", i),
			SummaryMessageID: summaryID,
			UpdatedAt:        int64(i + 1),
		})
	}

	startedCh := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	provider := &capturingSummaryProvider{
		content:   "fused milestone summary",
		startedCh: startedCh,
		releaseCh: releaseCh,
	}
	a := &agent{
		messages:          newOrderedMessageService(msgs...),
		sessions:          newMultiSessionTestService(sessions...),
		provider:          provider,
		summarizeProvider: provider,
	}

	a.triggerTreeSummarization(context.Background())
	select {
	case <-startedCh:
	case <-time.After(2 * time.Second):
		close(releaseCh)
		t.Fatalf("timed out waiting for tree summarization to start")
	}

	a.triggerTreeSummarization(context.Background())
	close(releaseCh)

	deadline := time.After(2 * time.Second)
	for provider.requestCount() != 1 {
		select {
		case <-deadline:
			t.Fatalf("expected exactly one tree summary request while running, got %d", provider.requestCount())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	a.triggerTreeSummarization(context.Background())
	select {
	case <-startedCh:
		t.Fatalf("expected cooldown to suppress a second tree summary run")
	case <-time.After(150 * time.Millisecond):
	}

	if provider.requestCount() != 1 {
		t.Fatalf("expected cooldown to keep tree summary request count at 1, got %d", provider.requestCount())
	}
}

func TestListRecentSessionSummaryMemoriesUsesSummaryMessages(t *testing.T) {
	checkpointContent := buildStructuredSummaryMessageContent(
		"第二条摘要",
		buildSummarySessionCheckpoint("session-2", "会话二", "第二条摘要", 20),
		buildSummaryTaskCheckpoint("session-2", &sessionTaskState{PrimaryGoal: "继续推进摘要 checkpoint", NextStep: "补 continuity memory", UpdatedAt: 20}),
	)
	a := &agent{
		messages: newOrderedMessageService(
			message.Message{
				ID:        "summary-1",
				SessionID: "session-1",
				Role:      message.User,
				Parts:     []message.ContentPart{message.TextContent{Text: "[System Summary]: 第一条摘要"}},
				UpdatedAt: 10,
			},
			message.Message{
				ID:        "summary-2",
				SessionID: "session-2",
				Role:      message.User,
				Parts:     []message.ContentPart{message.TextContent{Text: checkpointContent}},
				UpdatedAt: 20,
			},
			message.Message{
				ID:        "summary-worker",
				SessionID: "worker-1",
				Role:      message.User,
				Parts:     []message.ContentPart{message.TextContent{Text: "[System Summary]: 不应出现"}},
				UpdatedAt: 30,
			},
		),
		sessions: newMultiSessionTestService(
			session.Session{ID: "session-1", Title: "会话一", SummaryMessageID: "summary-1", UpdatedAt: 10},
			session.Session{ID: "session-2", Title: "会话二", SummaryMessageID: "summary-2", UpdatedAt: 20},
			session.Session{ID: "worker-1", ParentSessionID: "session-2", Title: "Worker: hidden", SummaryMessageID: "summary-worker", UpdatedAt: 30},
		),
	}

	mems, err := a.listRecentSessionSummaryMemories(context.Background(), "", 5)
	if err != nil {
		t.Fatalf("listRecentSessionSummaryMemories returned error: %v", err)
	}
	if len(mems) != 2 {
		t.Fatalf("expected 2 visible session summaries, got %d", len(mems))
	}
	if !strings.Contains(mems[0].Content, "[Session 会话二]: 第二条摘要") {
		t.Fatalf("expected latest summary first, got %q", mems[0].Content)
	}
	if strings.Contains(mems[0].Content, "Primary goal: 继续推进摘要 checkpoint") || strings.Contains(mems[0].Content, "Next step: 补 continuity memory") {
		t.Fatalf("expected continuity memory to exclude current task state, got %q", mems[0].Content)
	}
	if strings.Contains(mems[0].Content, "[System Summary]:") || strings.Contains(mems[1].Content, "[System Summary]:") {
		t.Fatalf("expected session summary prefix to be normalized, got %#v", mems)
	}
}

func TestListContinuityMemoriesScopesToCurrentSessionChain(t *testing.T) {
	a := &agent{
		messages: newOrderedMessageService(
			message.Message{ID: "summary-current", SessionID: "current", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "[System Summary]: 当前会话摘要"}}, UpdatedAt: 30},
			message.Message{ID: "summary-parent", SessionID: "parent", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "[System Summary]: 父会话摘要"}}, UpdatedAt: 20},
			message.Message{ID: "summary-other", SessionID: "other", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "[System Summary]: 无关会话摘要"}}, UpdatedAt: 40},
		),
		sessions: newMultiSessionTestService(
			session.Session{ID: "current", ParentSessionID: "parent", Title: "Current", SummaryMessageID: "summary-current"},
			session.Session{ID: "parent", Title: "Parent", SummaryMessageID: "summary-parent"},
			session.Session{ID: "other", Title: "Other", SummaryMessageID: "summary-other"},
		),
	}

	mems, err := a.listContinuityMemories(context.Background(), "current", 5)
	if err != nil {
		t.Fatalf("list continuity memories: %v", err)
	}
	if len(mems) != 2 {
		t.Fatalf("expected current session chain only, got %#v", mems)
	}
	joined := mems[0].Content + "\n" + mems[1].Content
	if !strings.Contains(joined, "当前会话摘要") || !strings.Contains(joined, "父会话摘要") || strings.Contains(joined, "无关会话摘要") {
		t.Fatalf("unexpected scoped continuity content: %q", joined)
	}
}

func TestShouldReplacePrimaryGoal(t *testing.T) {
	tests := []struct {
		name         string
		existingGoal string
		userInput    string
		want         bool
	}{
		{
			name:         "empty goal should be set",
			existingGoal: "",
			userInput:    "修复构建失败问题",
			want:         true,
		},
		{
			name:         "supplemental clarification should not replace goal",
			existingGoal: "修复构建失败问题",
			userInput:    "另外补充一下，把错误日志也整理出来",
			want:         false,
		},
		{
			name:         "explicit new task should replace goal",
			existingGoal: "修复构建失败问题",
			userInput:    "新任务：改成优化摘要逻辑",
			want:         true,
		},
		{
			name:         "generic repeat should not replace goal",
			existingGoal: "修复构建失败问题",
			userInput:    "修复构建失败问题",
			want:         false,
		},
		{
			name:         "direct topic shift without explicit marker should replace goal",
			existingGoal: "修复聊天界面布局",
			userInput:    "解释 Go 的 interface 和 type assertion",
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReplacePrimaryGoal(tt.existingGoal, tt.userInput)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestLooksLikeContinuationFollowUp(t *testing.T) {
	tests := []struct {
		name         string
		existingGoal string
		userInput    string
		want         bool
	}{
		{name: "generic continue", existingGoal: "修复构建失败", userInput: "继续", want: true},
		{name: "follow up supplement", existingGoal: "线上事故排查", userInput: "继续，把修复顺序和回归验证清单补全", want: true},
		{name: "new task is not continuation", existingGoal: "线上事故排查", userInput: "新任务：优化摘要逻辑", want: false},
		{name: "no existing goal is not follow up", existingGoal: "", userInput: "继续推进", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeContinuationFollowUp(tt.existingGoal, tt.userInput); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestDeriveTaskFocusFromUserInputAbstractsRawFeedback(t *testing.T) {
	state := &sessionTaskState{
		PrimaryGoal: "修复聊天界面布局",
		Status:      "active",
	}

	got := deriveTaskFocusFromUserInput(state, "还是没看到模型菜单，再加一个入口")
	if got != "优先核对被用户否认或纠正的结果" {
		t.Fatalf("expected abstract focus label, got %q", got)
	}

	for _, fragment := range []string{"没看到", "加一个", "模型菜单"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("expected focus to avoid raw user phrasing, got %q", got)
		}
	}
}

func TestDecayTaskFocusFallsBackToGoalDirectedFocus(t *testing.T) {
	state := &sessionTaskState{
		PrimaryGoal:       "修复聊天界面布局",
		CurrentFocus:      "根据用户最新反馈核对现有结果并修正缺口",
		CurrentFocusDecay: 1,
		Status:            "active",
	}

	decayTaskFocus(state)
	if state.CurrentFocus != "围绕当前主目标继续推进并验证结果" {
		t.Fatalf("expected focus to decay back to goal-directed default, got %q", state.CurrentFocus)
	}
	if state.CurrentFocusDecay != 0 {
		t.Fatalf("expected focus decay to reach zero, got %d", state.CurrentFocusDecay)
	}
}

func TestApplyCorrectionModeFromUserInput(t *testing.T) {
	state := &sessionTaskState{
		PrimaryGoal: "修复聊天界面布局",
		Status:      "active",
	}

	if !applyCorrectionModeFromUserInput(state, "不是这个问题，我说的是模型菜单还是看不到") {
		t.Fatalf("expected correction mode to activate for denial/correction input")
	}
	if !state.CorrectionMode {
		t.Fatalf("expected correction mode flag to be true")
	}
	if state.CorrectionDecay != maxCorrectionDecay {
		t.Fatalf("expected correction decay to initialize to %d, got %d", maxCorrectionDecay, state.CorrectionDecay)
	}
	if state.CorrectionHint == "" {
		t.Fatalf("expected correction hint to be recorded")
	}
	if !strings.Contains(state.CorrectionHint, "纠正") && !strings.Contains(state.CorrectionHint, "否认") {
		t.Fatalf("expected correction hint to describe correction intent, got %q", state.CorrectionHint)
	}
}

func TestApplyCorrectionModeFromUserInputPreservesExistingCorrectionOnNormalFollowUp(t *testing.T) {
	state := &sessionTaskState{
		PrimaryGoal:     "修复聊天界面布局",
		Status:          "active",
		CorrectionMode:  true,
		CorrectionHint:  "用户否认界面结果已经生效；先核对真实显示状态，再修正缺口，不要重复追加同类改动。",
		CorrectionDecay: 2,
	}

	if applyCorrectionModeFromUserInput(state, "继续把剩余样式收尾") {
		t.Fatalf("did not expect normal follow-up to keep correction mode active")
	}
	if !state.CorrectionMode || state.CorrectionHint == "" || state.CorrectionDecay != 2 {
		t.Fatalf("expected correction mode state to remain active for clarifying follow-up, got %+v", state)
	}
}

func TestUpdateTaskStateFromUserInputKeepsCorrectionPriorityDuringDiagnosticFollowUp(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	state := &sessionTaskState{
		PrimaryGoal:     "排查模型菜单显示问题",
		Status:          "active",
		DiagnosticMode:  true,
		CorrectionMode:  true,
		CorrectionHint:  "用户否认界面结果已经生效；先核对真实显示状态，再修正缺口，不要重复追加同类改动。",
		CorrectionDecay: 2,
	}
	if err := agentUnderTest.saveTaskState(context.Background(), "session-correction-diagnostic", state); err != nil {
		t.Fatalf("save task state: %v", err)
	}

	if err := agentUnderTest.updateTaskStateFromUserInput(context.Background(), "session-correction-diagnostic", "继续检查右上角模型按钮的显示状态"); err != nil {
		t.Fatalf("update task state from user input: %v", err)
	}

	updated, err := agentUnderTest.loadTaskState(context.Background(), "session-correction-diagnostic")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if updated == nil || !updated.CorrectionMode {
		t.Fatalf("expected correction mode to remain active, got %+v", updated)
	}
	if updated.CorrectionDecay != 1 {
		t.Fatalf("expected correction decay to decrement once per user turn, got %d", updated.CorrectionDecay)
	}
	if updated.CurrentFocus != "优先核对被用户否认或纠正的结果" {
		t.Fatalf("expected correction-focused current focus, got %q", updated.CurrentFocus)
	}
	if !strings.Contains(updated.NextStep, "先核对被用户否认的结果") || !strings.Contains(updated.NextStep, "诊断相位") {
		t.Fatalf("expected correction-aware diagnostic next step, got %q", updated.NextStep)
	}
	if updated.ContinuationMode != true {
		t.Fatalf("expected continuation mode to remain enabled for follow-up instruction")
	}
	if updated.ContinuationCount != 1 {
		t.Fatalf("expected continuation count to increment once, got %d", updated.ContinuationCount)
	}
	if len(updated.RecentProgress) == 0 || updated.RecentProgress[len(updated.RecentProgress)-1] != "收到继续/补全指令" {
		t.Fatalf("expected normal follow-up progress entry, got %+v", updated.RecentProgress)
	}
	if err := agentUnderTest.updateTaskStateFromToolResults(context.Background(), "session-correction-diagnostic", &message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read", Metadata: "client/src/components/chat/ChatInput.tsx", Content: "ok", IsError: false}}}); err != nil {
		t.Fatalf("update task state from tool results: %v", err)
	}

	updated, err = agentUnderTest.loadTaskState(context.Background(), "session-correction-diagnostic")
	if err != nil {
		t.Fatalf("reload task state: %v", err)
	}
	if !updated.CorrectionMode || updated.CorrectionDecay != 1 {
		t.Fatalf("expected internal tool events to stop consuming correction lifetime, got %+v", updated)
	}
	if updated.CurrentFocus != "优先核对被用户否认或纠正的结果" {
		t.Fatalf("expected tool progress to preserve correction-focused current focus, got %q", updated.CurrentFocus)
	}
	if !strings.Contains(updated.NextStep, "先核对被用户否认的结果") {
		t.Fatalf("expected tool progress to preserve correction-aware next step, got %q", updated.NextStep)
	}
}

func TestUpdateTaskStateFromUserInputReplacesGoalAndClearsContinuityState(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	state := &sessionTaskState{
		PrimaryGoal:       "修复聊天界面布局",
		CurrentFocus:      "根据用户最新反馈核对现有结果并修正缺口",
		CurrentFocusDecay: 2,
		ContinuationMode:  true,
		ContinuationCount: 3,
		ReadOnlyMode:      true,
		DiagnosticMode:    true,
		DiagnosticPhase:   "service-analysis",
		DiagnosticRoot:    "/tmp/project",
		DiagnosticTargets: []string{"/tmp/project/logs/order_sync.log"},
		StructuredFacts: []taskStructuredFact{{
			Marker:       "cache",
			CurrentValue: "fallback",
		}},
		FindingsLedger: []taskFindingLedgerEntry{{
			Source:  "assistant",
			Summary: "旧主题结论",
		}},
		ExecutionJournal: []taskExecutionJournalEntry{{
			Tool:    "read",
			Status:  "success",
			Summary: "旧主题读取",
		}},
		OutstandingIssues: []string{"read | old/file.go | 旧问题"},
		NextStep:          "优先基于已有连续性状态继续，不要重复规划或重复浏览",
		LastTool:          "read",
		LastToolStatus:    "success",
		LastToolSummary:   "旧主题读取",
	}
	if err := agentUnderTest.saveTaskState(context.Background(), "session-new-goal", state); err != nil {
		t.Fatalf("save task state: %v", err)
	}

	if err := agentUnderTest.updateTaskStateFromUserInput(context.Background(), "session-new-goal", "解释 Go 的 interface 和 type assertion"); err != nil {
		t.Fatalf("update task state from user input: %v", err)
	}

	updated, err := agentUnderTest.loadTaskState(context.Background(), "session-new-goal")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if updated.PrimaryGoal != "解释 Go 的 interface 和 type assertion" {
		t.Fatalf("expected new primary goal, got %q", updated.PrimaryGoal)
	}
	if updated.ContinuationMode || updated.ContinuationCount != 0 {
		t.Fatalf("expected continuation state to reset, got %+v", updated)
	}
	if updated.DiagnosticMode || updated.DiagnosticPhase != "" || updated.DiagnosticRoot != "" || len(updated.DiagnosticTargets) != 0 {
		t.Fatalf("expected diagnostic continuity to reset, got %+v", updated)
	}
	if len(updated.StructuredFacts) != 0 || len(updated.FindingsLedger) != 0 || len(updated.ExecutionJournal) != 0 || len(updated.OutstandingIssues) != 0 {
		t.Fatalf("expected continuity artifacts to clear, got %+v", updated)
	}
	if updated.LastTool != "" || updated.LastToolStatus != "" || updated.LastToolSummary != "" {
		t.Fatalf("expected last tool state to clear, got %+v", updated)
	}
	if updated.CurrentFocus != "根据用户最新反馈调整当前实现并验证结果" {
		t.Fatalf("expected focus to realign to new goal, got %q", updated.CurrentFocus)
	}
	if updated.CurrentFocusDecay != maxCurrentFocusDecay {
		t.Fatalf("expected current focus decay to reset to %d, got %d", maxCurrentFocusDecay, updated.CurrentFocusDecay)
	}
	if updated.NextStep != "继续拆解并执行当前目标" {
		t.Fatalf("expected next step to reset for new goal, got %q", updated.NextStep)
	}
	if updated.ReadOnlyMode {
		t.Fatalf("expected inferred read-only state to clear across unrelated new goal")
	}
	if len(updated.RecentProgress) != 1 || updated.RecentProgress[0] != "收到新的用户目标" {
		t.Fatalf("expected recent progress to restart from new goal marker, got %+v", updated.RecentProgress)
	}
}

func TestUpdateTaskStateFromUserInputPreservesManualReadOnlyAcrossNewGoal(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	state := &sessionTaskState{
		PrimaryGoal:        "审查代码，不要修改",
		ReadOnlyMode:       true,
		ReadOnlyModeManual: true,
	}
	if err := agentUnderTest.saveTaskState(context.Background(), "session-manual-read-only-new-goal", state); err != nil {
		t.Fatalf("save task state: %v", err)
	}

	if err := agentUnderTest.updateTaskStateFromUserInput(context.Background(), "session-manual-read-only-new-goal", "今天天气怎么样"); err != nil {
		t.Fatalf("update task state from user input: %v", err)
	}

	updated, err := agentUnderTest.loadTaskState(context.Background(), "session-manual-read-only-new-goal")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if !updated.ReadOnlyMode || !updated.ReadOnlyModeManual {
		t.Fatalf("expected manual read-only mode to persist across new goal, got %+v", updated)
	}
}

func TestUpdateTaskStateFromUserInputClearsIntermediateContinuityAcrossMultipleTopicSwitches(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	state := &sessionTaskState{
		PrimaryGoal:       "修复聊天界面布局",
		ContinuationMode:  true,
		ContinuationCount: 2,
		ReadOnlyMode:      true,
		DiagnosticMode:    true,
		DiagnosticPhase:   "root-cause",
		DiagnosticRoot:    "/tmp/project",
		DiagnosticTargets: []string{
			"/tmp/project/logs/order_sync.log",
		},
		StructuredFacts: []taskStructuredFact{{
			Marker:       "cache",
			CurrentValue: "fallback",
		}},
		FindingsLedger: []taskFindingLedgerEntry{{
			Source:  "assistant",
			Summary: "旧主题布局问题结论",
		}},
		ExecutionJournal: []taskExecutionJournalEntry{{
			Tool:    "read",
			Status:  "success",
			Summary: "旧主题读取",
		}},
		NextStep: "优先基于已有连续性状态继续，不要重复规划或重复浏览",
	}
	if err := agentUnderTest.saveTaskState(context.Background(), "session-topic-switch-chain", state); err != nil {
		t.Fatalf("save task state: %v", err)
	}

	if err := agentUnderTest.updateTaskStateFromUserInput(context.Background(), "session-topic-switch-chain", "解释 Go 的 interface 和 type assertion"); err != nil {
		t.Fatalf("switch to intermediate topic: %v", err)
	}
	if err := agentUnderTest.updateTaskStateFromToolResults(context.Background(), "session-topic-switch-chain", &message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read", Metadata: "docs/go/interface.md", Content: "interface 示例", IsError: false}}}); err != nil {
		t.Fatalf("record intermediate tool result: %v", err)
	}
	if err := agentUnderTest.updateTaskStateFromAssistantMessage(context.Background(), "session-topic-switch-chain", message.NewTextMessage(message.Assistant, "已解释 interface 和 type assertion 的差异，并补充了断言示例。")); err != nil {
		t.Fatalf("record intermediate assistant message: %v", err)
	}

	intermediate, err := agentUnderTest.loadTaskState(context.Background(), "session-topic-switch-chain")
	if err != nil {
		t.Fatalf("load intermediate task state: %v", err)
	}
	if len(intermediate.ExecutionJournal) == 0 || len(intermediate.FindingsLedger) == 0 {
		t.Fatalf("expected intermediate topic to accumulate continuity state, got %+v", intermediate)
	}

	if err := agentUnderTest.updateTaskStateFromUserInput(context.Background(), "session-topic-switch-chain", "帮我写一个咖啡店开业活动文案"); err != nil {
		t.Fatalf("switch to final topic: %v", err)
	}

	updated, err := agentUnderTest.loadTaskState(context.Background(), "session-topic-switch-chain")
	if err != nil {
		t.Fatalf("load task state after second switch: %v", err)
	}
	if updated.PrimaryGoal != "帮我写一个咖啡店开业活动文案" {
		t.Fatalf("expected final goal to be latest topic, got %q", updated.PrimaryGoal)
	}
	if updated.ContinuationMode || updated.ContinuationCount != 0 {
		t.Fatalf("expected continuation state to reset after second topic switch, got %+v", updated)
	}
	if updated.DiagnosticMode || updated.DiagnosticPhase != "" || updated.DiagnosticRoot != "" || len(updated.DiagnosticTargets) != 0 {
		t.Fatalf("expected diagnostic state to clear after second topic switch, got %+v", updated)
	}
	if len(updated.StructuredFacts) != 0 || len(updated.FindingsLedger) != 0 || len(updated.ExecutionJournal) != 0 || len(updated.OutstandingIssues) != 0 {
		t.Fatalf("expected intermediate continuity artifacts to clear after second topic switch, got %+v", updated)
	}
	if len(updated.RecentProgress) != 1 || updated.RecentProgress[0] != "收到新的用户目标" {
		t.Fatalf("expected recent progress to restart for latest topic, got %+v", updated.RecentProgress)
	}
	workingStateMsg := buildWorkingStateSystemMessage(updated)
	if workingStateMsg == nil {
		t.Fatalf("expected working state prompt for latest topic")
	}
	workingStateText := workingStateMsg.Content().String()
	for _, fragment := range []string{"interface", "type assertion", "聊天界面布局", "旧主题布局问题结论"} {
		if strings.Contains(workingStateText, fragment) {
			t.Fatalf("expected working state prompt to drop prior topics, got %q", workingStateText)
		}
	}
	if buildExecutionJournalSystemMessage(updated) != nil {
		t.Fatalf("expected execution journal prompt to be cleared after latest topic switch")
	}
	if buildFindingsLedgerSystemMessage(updated) != nil {
		t.Fatalf("expected findings ledger prompt to be cleared after latest topic switch")
	}
	if buildStructuredFactsSystemMessage(updated) != nil {
		t.Fatalf("expected structured facts prompt to be cleared after latest topic switch")
	}
}

func TestTruncatedToolCallError(t *testing.T) {
	input := `{"_truncated_error":"Tool call arguments were truncated at 900 chars due to output token limit."}`
	messageText, ok := truncatedToolCallError(input)
	if !ok {
		t.Fatalf("expected truncated tool call marker to be detected")
	}
	if !strings.Contains(messageText, "output token limit") {
		t.Fatalf("expected original truncation reason to be preserved, got %q", messageText)
	}
}

func TestIsLikelyOutputLimitError(t *testing.T) {
	err := fmt.Errorf("provider error: exceeded the maximum number of output tokens for this response")
	if !isLikelyOutputLimitError(err) {
		t.Fatalf("expected output limit error to be detected")
	}

	contextErr := fmt.Errorf("context length exceeded")
	if isLikelyOutputLimitError(contextErr) {
		t.Fatalf("did not expect context limit error to be treated as output limit")
	}
}

func TestDeriveWordSessionToolAllowlist(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n请修改当前文档"
	allowlist := deriveWordSessionToolAllowlist(input)
	if len(allowlist) == 0 {
		t.Fatalf("expected word session allowlist to be enabled")
	}
	if !isToolAllowedByAllowlist(allowlist, tools.ManageToolsToolName) {
		t.Fatalf("expected manage_tools to stay available")
	}
	if !isToolAllowedByAllowlist(allowlist, "auramate-word-reader") {
		t.Fatalf("expected auramate-word-reader to be allowed")
	}
	if !isToolAllowedByAllowlist(allowlist, "read") {
		t.Fatalf("expected generic read tool to be allowed in word context")
	}
	if !isToolAllowedByAllowlist(allowlist, "fetch") {
		t.Fatalf("expected fetch to be allowed in word context")
	}
	if isToolAllowedByAllowlist(allowlist, "run_command") {
		t.Fatalf("did not expect run_command to be allowed in word context")
	}
	if isToolAllowedByAllowlist(allowlist, "check_command_status") {
		t.Fatalf("did not expect check_command_status to be allowed in word context")
	}
	if !isToolAllowedByAllowlist(allowlist, "skill-generator") {
		t.Fatalf("expected skill-generator to be allowed in word context")
	}

	if deriveWordSessionToolAllowlist("普通编码请求") != nil {
		t.Fatalf("did not expect allowlist for normal requests")
	}

	withoutOpenPanelNote := "[WORD_CONTEXT]\nediting_policy: interface_only\n[/WORD_CONTEXT]\n请修改当前文档"
	if deriveWordSessionToolAllowlist(withoutOpenPanelNote) != nil {
		t.Fatalf("did not expect allowlist without explicit open panel note")
	}
}

func TestDeriveSessionToolAllowlistBlocksWordToolsOutsideDocumentMode(t *testing.T) {
	agentInstance := &agent{
		allTools: map[string]tools.BaseTool{
			tools.ManageToolsToolName: stubAgentTool{info: tools.ToolInfo{Name: tools.ManageToolsToolName}},
			"auramate-word-reader":    stubAgentTool{info: tools.ToolInfo{Name: "auramate-word-reader"}},
			"auramate-word-copilot":   stubAgentTool{info: tools.ToolInfo{Name: "auramate-word-copilot"}},
			"skill-generator":         stubAgentTool{info: tools.ToolInfo{Name: "skill-generator"}},
			"read":                    stubAgentTool{info: tools.ToolInfo{Name: "read"}},
			"fetch":                   stubAgentTool{info: tools.ToolInfo{Name: "fetch"}},
			"run_command":             stubAgentTool{info: tools.ToolInfo{Name: "run_command"}},
		},
		activeTools: map[string]bool{
			tools.ManageToolsToolName: true,
			"auramate-word-reader":    true,
			"auramate-word-copilot":   true,
			"skill-generator":         true,
			"read":                    true,
			"fetch":                   true,
			"run_command":             true,
		},
	}

	allowlist := agentInstance.deriveSessionToolAllowlist("普通编码请求")
	if !isToolAllowedByAllowlist(allowlist, tools.ManageToolsToolName) {
		t.Fatalf("expected manage_tools to remain available outside document mode")
	}
	if !isToolAllowedByAllowlist(allowlist, "read") {
		t.Fatalf("expected read to remain available outside document mode")
	}
	if !isToolAllowedByAllowlist(allowlist, "fetch") {
		t.Fatalf("expected fetch to remain available outside document mode")
	}
	if !isToolAllowedByAllowlist(allowlist, "run_command") {
		t.Fatalf("expected run_command to remain available outside document mode")
	}
	if !isToolAllowedByAllowlist(allowlist, "skill-generator") {
		t.Fatalf("expected skill-generator to remain available outside document mode")
	}
	if isToolAllowedByAllowlist(allowlist, "auramate-word-reader") {
		t.Fatalf("did not expect auramate-word-reader outside document mode")
	}
	if isToolAllowedByAllowlist(allowlist, "auramate-word-copilot") {
		t.Fatalf("did not expect auramate-word-copilot outside document mode")
	}
}

func TestDeriveSessionToolAllowlistForModeKeepsToolsAvailableOutsideWordContext(t *testing.T) {
	agentInstance := &agent{
		allTools: map[string]tools.BaseTool{
			tools.ManageToolsToolName: stubAgentTool{info: tools.ToolInfo{Name: tools.ManageToolsToolName}},
			"read":                    stubAgentTool{info: tools.ToolInfo{Name: "read"}},
			"run_command":             stubAgentTool{info: tools.ToolInfo{Name: "run_command"}},
			"write":                   stubAgentTool{info: tools.ToolInfo{Name: "write"}},
		},
		activeTools: map[string]bool{
			tools.ManageToolsToolName: true,
			"read":                    true,
			"run_command":             true,
			"write":                   true,
		},
	}

	allowlist := agentInstance.deriveSessionToolAllowlistForMode("解释 Go 的接口和类型断言，并给一个例子", "normal")
	for _, name := range []string{tools.ManageToolsToolName, "read", "run_command", "write"} {
		if !isToolAllowedByAllowlist(allowlist, name) {
			t.Fatalf("expected %s to remain available outside word context", name)
		}
	}
}

func TestAppendWordCopilotRecoveryHintContinuesMultiEditWordTask(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把页眉里的“Auramate 内部资料”改成“Auramate 项目周报”，把“验证说明：当前版本仍需人工复核。”改成“审阅说明：当前版本仍需人工复核。”，并把表格里的85%改成92%。"
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"85%\",\"table_id\":\"tbl_1\",\"row_index\":1,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"}]}}"
	got := appendWordCopilotRecoveryHint(input, "auramate-word-copilot", content, false)
	if !strings.Contains(got, "任务尚未完成") {
		t.Fatalf("expected continuation hint for multi-edit word task, got %q", got)
	}
	if !strings.Contains(got, "下一步只处理未完成的第 1 项") {
		t.Fatalf("expected next-step subtask hint, got %q", got)
	}
}

func TestAppendWordCopilotRecoveryHintSkipsSingleEditSuccess(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把表格里的85%改成92%。"
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"85%\",\"table_id\":\"tbl_1\",\"row_index\":1,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"}]}}"
	got := appendWordCopilotRecoveryHint(input, "auramate-word-copilot", content, false)
	if got != content {
		t.Fatalf("expected single edit success to stay unchanged, got %q", got)
	}
}

func TestAppendWordCopilotRecoveryHintKeepsErrorPath(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，并把风险提示右侧结果改成无需人工复核。"
	got := appendWordCopilotRecoveryHint(input, "auramate-word-copilot", "Error: missing evidence", true)
	if !strings.Contains(got, "调用失败后") {
		t.Fatalf("expected original error recovery hint, got %q", got)
	}
}

func TestAppendWordCopilotRecoveryHintAddsRelativeAnchorGuidance(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}"
	got := appendWordCopilotRecoveryHint(input, "auramate-word-copilot", content, false)
	if !strings.Contains(got, "preferred_paragraph_id") {
		t.Fatalf("expected relative-anchor guidance, got %q", got)
	}
	if !strings.Contains(got, "reader_session_id") {
		t.Fatalf("expected reader_session_id guidance, got %q", got)
	}
	if !strings.Contains(got, "第 1 项") {
		t.Fatalf("expected remaining-step numbering, got %q", got)
	}
	if !strings.Contains(got, "不要继续只处理表格单元格") {
		t.Fatalf("expected anti-table-loop guidance, got %q", got)
	}
}

func TestBuildWordMultiEditControlMessage(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	got := buildWordMultiEditControlMessage(input)
	if !strings.Contains(got, "必须硬拆成 3 个顺序子任务") {
		t.Fatalf("expected explicit hard-split count, got %q", got)
	}
	if !strings.Contains(got, "1. 把最后那条备注改成归档备注") || !strings.Contains(got, "2. 把第三方费用那条后面补一句需要提前走采购审批") || !strings.Contains(got, "3. 把风险提示右侧结果改成无需人工复核") {
		t.Fatalf("expected ordered subtask list, got %q", got)
	}
	if !strings.Contains(got, "preferred_paragraph_id") {
		t.Fatalf("expected relative-anchor prerequisite guidance, got %q", got)
	}
	if !strings.Contains(got, "reader_session_id") {
		t.Fatalf("expected session alignment guidance, got %q", got)
	}
}

func TestBuildWordMultiEditControlMessageAddsPageAnchorGuidance(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把第3页那段说明改成更正式的表述，并把表格里的通过率改成92%。"
	got := buildWordMultiEditControlMessage(input)
	if !strings.Contains(got, "focus_page_index") {
		t.Fatalf("expected page anchor guidance, got %q", got)
	}
	if !strings.Contains(got, "include_layout=true") {
		t.Fatalf("expected layout guidance, got %q", got)
	}
}

func TestPendingWordEditSubtasksTracksRemainingSteps(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	content := strings.Join([]string{
		"__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"附录说明：本报告用于自动化回归。\",\"new_text\":\"归档备注：本报告用于自动化回归。\",\"preferred_paragraph_id\":\"para_1\"}",
		"__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}",
	}, "\n")
	pending := pendingWordEditSubtasks(input, content)
	if len(pending) != 1 {
		t.Fatalf("expected one pending subtask, got %d", len(pending))
	}
	if pending[0].Kind != "relative_insert" {
		t.Fatalf("expected remaining subtask to be relative insert, got %#v", pending[0])
	}
	if !strings.Contains(pending[0].Clause, "第三方费用") {
		t.Fatalf("expected remaining clause to mention third-party fee, got %#v", pending[0])
	}
}

func TestBuildWordPendingSubtaskControlMessage(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	msgHistory := []message.Message{
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{Name: "auramate-word-copilot", Content: strings.Join([]string{
					"__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"附录说明：本报告用于自动化回归。\",\"new_text\":\"归档备注：本报告用于自动化回归。\",\"preferred_paragraph_id\":\"para_1\"}",
					"__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}",
				}, "\n")},
			},
		},
	}
	got := buildWordPendingSubtaskControlMessage(input, msgHistory)
	if !strings.Contains(got, "当前已完成 2/3 项") {
		t.Fatalf("expected progress summary, got %q", got)
	}
	if !strings.Contains(got, "第三方费用那条后面补一句需要提前走采购审批") {
		t.Fatalf("expected remaining step clause, got %q", got)
	}
}

func TestValidateWordCopilotSubtaskResultRejectsWrongRemainingStep(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	history := []message.Message{
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{Name: "auramate-word-copilot", Content: strings.Join([]string{
					"__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"附录说明：本报告用于自动化回归。\",\"new_text\":\"归档备注：本报告用于自动化回归。\",\"preferred_paragraph_id\":\"para_1\"}",
					"__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}",
				}, "\n")},
			},
		},
	}
	wrongContent := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}"
	got, blocked := validateWordCopilotSubtaskResult(input, history, nil, 0, wrongContent)
	if !blocked {
		t.Fatalf("expected wrong remaining step to be blocked")
	}
	if !strings.Contains(got, "只执行未完成的第 2 项") {
		t.Fatalf("expected explicit pending-step rejection, got %q", got)
	}
}

func TestValidateWordCopilotSubtaskResultAcceptsMatchingRemainingStep(t *testing.T) {
	input := "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把最后那条备注改成归档备注，把第三方费用那条后面补一句需要提前走采购审批，并把风险提示右侧结果改成无需人工复核。"
	history := []message.Message{
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{Name: "auramate-word-copilot", Content: strings.Join([]string{
					"__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"附录说明：本报告用于自动化回归。\",\"new_text\":\"归档备注：本报告用于自动化回归。\",\"preferred_paragraph_id\":\"para_1\"}",
					"__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_1\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}",
				}, "\n")},
			},
		},
	}
	rightContent := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"第三方费用由甲方另行承担，并在采购前单独确认。\",\"new_text\":\"需要提前走采购审批流程。\",\"preferred_paragraph_id\":\"para_2\",\"position\":\"after\"}"
	got, blocked := validateWordCopilotSubtaskResult(input, history, nil, 0, rightContent)
	if blocked {
		t.Fatalf("expected matching remaining step to pass, got %q", got)
	}
	if got != rightContent {
		t.Fatalf("expected matching content to stay unchanged, got %q", got)
	}
}

func TestSessionScopedToolFiltering(t *testing.T) {
	agentInstance := &agent{
		allTools: map[string]tools.BaseTool{
			tools.ManageToolsToolName: stubAgentTool{info: tools.ToolInfo{Name: tools.ManageToolsToolName}},
			"auramate-word-reader":    stubAgentTool{info: tools.ToolInfo{Name: "auramate-word-reader"}},
			"auramate-word-copilot":   stubAgentTool{info: tools.ToolInfo{Name: "auramate-word-copilot"}},
			"read":                    stubAgentTool{info: tools.ToolInfo{Name: "read"}},
		},
		activeTools: map[string]bool{
			tools.ManageToolsToolName: true,
			"auramate-word-reader":    true,
			"auramate-word-copilot":   true,
			"read":                    true,
		},
	}

	agentInstance.updateSessionToolAllowlist("session-word", "[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]", "coding")
	active := agentInstance.getActiveToolsForSession("session-word", "")
	if len(active) != 4 {
		t.Fatalf("expected 4 active tools after filtering, got %d", len(active))
	}
	readSeen := false
	wordCopilotSeen := false
	for _, tool := range active {
		if tool.Info().Name == "read" {
			readSeen = true
		}
		if tool.Info().Name == "auramate-word-copilot" {
			wordCopilotSeen = true
		}
	}
	if !readSeen {
		t.Fatalf("expected generic read tool to remain active in word context")
	}
	if !wordCopilotSeen {
		t.Fatalf("expected auramate-word-copilot to remain active in document mode")
	}

	infos := agentInstance.ListAvailableToolsForSession("session-word")
	readListed := false
	for _, info := range infos {
		if info.Name == "read" {
			readListed = true
		}
	}
	if !readListed {
		t.Fatalf("expected generic read tool to be listed in session-scoped tools")
	}

	if err := agentInstance.LoadToolForSession("session-word", "auramate-word-copilot"); err != nil {
		t.Fatalf("expected auramate-word-copilot load to succeed in document mode, got %v", err)
	}

	if err := agentInstance.LoadToolForSession("session-word", "read"); err != nil {
		t.Fatalf("expected read tool load to succeed, got %v", err)
	}
	if !agentInstance.IsLoadedForSession("session-word", tools.ManageToolsToolName) {
		t.Fatalf("expected manage_tools to remain loaded")
	}

	agentInstance.updateSessionToolAllowlist("session-normal", "普通编码请求", "coding")
	normalInfos := agentInstance.ListAvailableToolsForSession("session-normal")
	for _, info := range normalInfos {
		if info.Name == "auramate-word-reader" || info.Name == "auramate-word-copilot" {
			t.Fatalf("did not expect %s to be listed outside document mode", info.Name)
		}
	}
	if err := agentInstance.LoadToolForSession("session-normal", "auramate-word-copilot"); err == nil {
		t.Fatalf("expected auramate-word-copilot load to fail outside document mode")
	}

	agentInstance.allTools["run_command"] = stubAgentTool{info: tools.ToolInfo{Name: "run_command"}}
	agentInstance.allTools["write"] = stubAgentTool{info: tools.ToolInfo{Name: "write"}}
	agentInstance.activeTools["run_command"] = true
	agentInstance.activeTools["write"] = true
	agentInstance.updateSessionToolAllowlist("session-chat", "解释 Go 的接口和类型断言，并给一个例子", "normal")
	chatActive := agentInstance.getActiveToolsForSession("session-chat", "")
	runCommandSeen := false
	writeSeen := false
	for _, tool := range chatActive {
		if tool.Info().Name == "run_command" {
			runCommandSeen = true
		}
		if tool.Info().Name == "write" {
			writeSeen = true
		}
	}
	if !runCommandSeen {
		t.Fatalf("expected run_command to remain active outside word context")
	}
	if !writeSeen {
		t.Fatalf("expected write to remain active outside word context")
	}
}

func TestEstimateHistoryTokensMatchesRuneBasedHelper(t *testing.T) {
	history := []message.Message{
		message.NewTextMessage(message.User, "中文上下文"),
		message.NewTextMessage(message.Assistant, "mixed-English-123"),
	}

	want := estimateContextTokensFromText("中文上下文") + estimateContextTokensFromText("mixed-English-123")
	got := estimateHistoryTokens(history)
	if got != want {
		t.Fatalf("estimateHistoryTokens = %d, want %d", got, want)
	}
}

func TestTruncateHistoryFallbackUsesTokenBudget(t *testing.T) {
	sessionID := "session-rune-fallback"
	msgService := newOrderedMessageService(
		message.Message{ID: "msg-1", SessionID: sessionID, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "中文中文中文中文"}}},
		message.Message{ID: "msg-2", SessionID: sessionID, Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "abcdefghi"}}},
	)
	a := &agent{
		messages: msgService,
		sessions: newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "fallback test"}),
	}

	truncated := a.truncateHistoryFallback(context.Background(), sessionID, estimateContextTokensFromText("abcdefghi"))
	if len(truncated) != 1 {
		t.Fatalf("expected fallback truncation to keep only the newest message, got %d", len(truncated))
	}
	if truncated[0].ID != "msg-2" {
		t.Fatalf("expected fallback truncation to keep newest message, got %s", truncated[0].ID)
	}
}

func TestLoadToolKeepsNativeWordToolWhenSkillHasSameName(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "auramate-word-reader")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "dummy.py"), []byte("print('noop')\n"), 0644); err != nil {
		t.Fatalf("write dummy script: %v", err)
	}
	skillMd := `---
name: auramate-word-reader
description: skill fallback description should not override native
language: python
script_path: dummy.py
parameters:
  sample:
    type: string
    description: sample
    required: false
---

# fallback
should never replace native description
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	skillsManager := skills.NewManager(tmpDir)
	skillsManager.Scan()
	if !skillsManager.IsLoaded("auramate-word-reader") {
		t.Fatalf("expected fallback skill to be discoverable for test setup")
	}

	nativeTool := tools.NewWordReaderTool()
	nativeDescription := nativeTool.Info().Description
	agentInstance := &agent{
		allTools: map[string]tools.BaseTool{
			nativeTool.Info().Name: nativeTool,
		},
		activeTools:   map[string]bool{},
		skillsManager: skillsManager,
	}

	if err := agentInstance.LoadTool("auramate-word-reader"); err != nil {
		t.Fatalf("expected native tool load to succeed, got %v", err)
	}
	gotDescription := agentInstance.allTools["auramate-word-reader"].Info().Description
	if gotDescription != nativeDescription {
		t.Fatalf("expected native tool description to remain authoritative, got %q", gotDescription)
	}
	if strings.Contains(gotDescription, "skill fallback description should not override native") {
		t.Fatalf("expected same-name skill fallback to be ignored when native tool exists")
	}
}

func TestContainsReadOnlyCue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "explicit chinese read only", input: "这是只读评估，不要修改任何文件", want: true},
		{name: "english read only", input: "read-only analysis only, do not modify files", want: true},
		{name: "normal fix request", input: "请直接修改并修复这个问题", want: false},
		{name: "weather question is not read only", input: "今天天气怎么样", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsReadOnlyCue(tt.input); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestContainsDiagnosticCue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "chinese diagnostic triage", input: "这是只读评估，请排查根因、级联影响、风险隔离和回归验证", want: true},
		{name: "english incident triage", input: "read-only incident triage with root cause and cascade impact analysis", want: true},
		{name: "generic fix request", input: "直接修改代码并提交修复", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsDiagnosticCue(tt.input); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestExtractDiagnosticRootAndTargets(t *testing.T) {
	root := extractDiagnosticRoot("根目录是 workspace/mixed_release_triage。请只读排查根因和级联影响")
	if root != "workspace/mixed_release_triage" {
		t.Fatalf("unexpected diagnostic root: %q", root)
	}

	targets := buildDiagnosticTargets(root)
	if len(targets) != 8 {
		t.Fatalf("expected 8 diagnostic targets, got %d", len(targets))
	}
	if targets[0] != "workspace/mixed_release_triage/logs/order_sync.log" {
		t.Fatalf("unexpected first diagnostic target: %q", targets[0])
	}
	if targets[2] != "workspace/mixed_release_triage/service/order_sync.py" {
		t.Fatalf("unexpected service diagnostic target: %q", targets[2])
	}
}

func TestDeriveTaskStateHintsFromInput(t *testing.T) {
	derived := deriveTaskStateHintsFromInput(nil, "我在工作区里放了一个线上事故排查包，根目录是 workspace/mixed_release_triage。这是只读评估，禁止修改任何文件，请排查根因、级联影响和回归验证。")
	if derived == nil {
		t.Fatalf("expected derived state")
	}
	if !derived.ReadOnlyMode {
		t.Fatalf("expected read-only mode to be inferred")
	}
	if !derived.DiagnosticMode {
		t.Fatalf("expected diagnostic mode to be inferred")
	}
	if derived.DiagnosticRoot != "workspace/mixed_release_triage" {
		t.Fatalf("unexpected diagnostic root: %q", derived.DiagnosticRoot)
	}
	if len(derived.DiagnosticTargets) != 8 {
		t.Fatalf("expected diagnostic targets to be populated, got %d", len(derived.DiagnosticTargets))
	}

	upgraded := deriveTaskStateHintsFromInput(&sessionTaskState{}, "这是只读评估，根目录是 workspace/mixed_release_triage，请排查根因和风险隔离")
	blocked, reason := shouldBlockContinuationToolCall(upgraded, message.ToolCall{Name: "ls", Input: `{"path":"workspace/mixed_release_triage","recursive":true}`})
	if !blocked {
		t.Fatalf("expected recursive root ls to be blocked after runtime hint derivation")
	}
	if !strings.Contains(reason, "Diagnostic mode guard") {
		t.Fatalf("expected diagnostic mode guard reason, got %q", reason)
	}

	longPrefix := strings.Repeat("路径说明", 40) + " 这是只读评估，禁止修改任何文件。根目录是 workspace/mixed_release_triage，请排查根因和级联影响。"
	truncatedSafe := deriveTaskStateHintsFromInput(nil, longPrefix)
	if truncatedSafe == nil || !truncatedSafe.ReadOnlyMode || !truncatedSafe.DiagnosticMode {
		t.Fatalf("expected full input cues to survive long prefix truncation")
	}

	unlocked := deriveTaskStateHintsFromInput(&sessionTaskState{
		ReadOnlyMode:      true,
		DiagnosticMode:    true,
		DiagnosticPhase:   "root-cause",
		DiagnosticRoot:    "workspace/mixed_release_triage",
		DiagnosticTargets: []string{"workspace/mixed_release_triage/logs/order_sync.log"},
	}, "取消只读，放行工具，现在可以改代码")
	if unlocked == nil || unlocked.ReadOnlyMode {
		t.Fatalf("expected explicit read-only override to unlock writes, got %#v", unlocked)
	}
	if unlocked.DiagnosticMode || unlocked.DiagnosticPhase != "" || unlocked.DiagnosticRoot != "" || len(unlocked.DiagnosticTargets) != 0 {
		t.Fatalf("expected read-only diagnostic state to clear after write unlock, got %#v", unlocked)
	}

	stillReadOnly := deriveTaskStateHintsFromInput(&sessionTaskState{}, "审查一下，不要修改代码")
	if stillReadOnly == nil || !stillReadOnly.ReadOnlyMode {
		t.Fatalf("expected negated write cue to remain read-only, got %#v", stillReadOnly)
	}
}

func TestShouldBlockContinuationToolCall(t *testing.T) {
	oldLoader := loadActivePlanBaseNameForSession
	loadActivePlanBaseNameForSession = func(sessionID string) string {
		if sessionID == "session-with-plan" {
			return "release-plan"
		}
		return ""
	}
	t.Cleanup(func() {
		loadActivePlanBaseNameForSession = oldLoader
	})

	state := &sessionTaskState{
		ContinuationMode:  true,
		ContinuationCount: 1,
		StructuredFacts: []taskStructuredFact{
			{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync"},
			{Artifact: "service/order_sync.py", Marker: "REQUEST_TIMEOUT_SECONDS", CurrentValue: "30"},
			{Artifact: "logs/order_sync.log", Marker: "timed out after", CurrentValue: "30s"},
			{Artifact: "tests/failing_checks.txt", Marker: "timeout_sla_seconds", CurrentValue: "30"},
		},
	}

	blocked, _ := shouldBlockContinuationToolCall(state, message.ToolCall{Name: "manage_plan", Input: ""})
	if !blocked {
		t.Fatalf("expected empty manage_plan to be blocked in continuation mode")
	}

	complexState := &sessionTaskState{
		ContinuationMode:  true,
		ContinuationCount: 1,
		PrimaryGoal:       "请排查登录超时并全面修复和验证发布流程",
		NextStep:          "先梳理实现链路和验证面",
	}
	blocked, _ = shouldBlockContinuationToolCallForSession("session-without-plan", complexState, message.ToolCall{Name: "manage_plan", Input: `{"action":"add","items":["排查登录超时","修复实现","补充验证"]}`})
	if blocked {
		t.Fatalf("did not expect complex continuation plan bootstrap to be blocked when no active plan exists")
	}

	blocked, _ = shouldBlockContinuationToolCallForSession("session-with-plan", complexState, message.ToolCall{Name: "manage_plan", Input: `{"action":"add","items":["排查登录超时"]}`})
	if !blocked {
		t.Fatalf("expected manage_plan add to stay blocked when an active plan already exists")
	}

	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "manage_plan", Input: `{"action":"update","id":"abc12345","status":"in_progress"}`})
	if blocked {
		t.Fatalf("did not expect id-based manage_plan update to be blocked in continuation mode")
	}

	blocked, reason := shouldBlockContinuationToolCall(state, message.ToolCall{Name: "manage_plan", Input: `{"action":"update","index":2,"status":"in_progress"}`})
	if !blocked {
		t.Fatalf("expected index-based manage_plan update to be blocked in continuation mode")
	}
	if !strings.Contains(reason, "must target an existing item by id") {
		t.Fatalf("expected id-first continuation reason, got %q", reason)
	}

	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "ls", Input: `{"path":"workspace/foo","recursive":true}`})
	if !blocked {
		t.Fatalf("expected recursive ls to be blocked in continuation mode with reusable state")
	}

	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"file_path":"config/prod.yaml"}`})
	if blocked {
		t.Fatalf("did not expect targeted read to be blocked")
	}

	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"session_id":"fbba6c8c-7c9e-42a0-ad9d-2b3b6c47cba6","mode":"full"}`})
	if blocked {
		t.Fatalf("did not expect session snapshot read to be blocked")
	}

	blocked, reason = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: ""})
	if !blocked {
		t.Fatalf("expected empty read to be blocked in continuation mode")
	}
	if !strings.Contains(reason, "read requires a concrete file target or session_id") {
		t.Fatalf("expected empty read reason, got %q", reason)
	}

	readOnlyState := &sessionTaskState{ReadOnlyMode: true}
	blocked, reason = shouldBlockContinuationToolCall(readOnlyState, message.ToolCall{Name: "write", Input: `{"file_path":"docs/report.md","content":"x"}`})
	if !blocked {
		t.Fatalf("expected write to be blocked in read-only mode")
	}
	if !strings.Contains(reason, "Read-only guard") {
		t.Fatalf("expected read-only reason, got %q", reason)
	}

	blocked, _ = shouldBlockContinuationToolCall(readOnlyState, message.ToolCall{Name: "read", Input: `{"file_path":"config/prod.yaml"}`})
	if blocked {
		t.Fatalf("did not expect read to be blocked in read-only mode")
	}

	blocked, reason = shouldBlockContinuationToolCall(readOnlyState, message.ToolCall{Name: "ls", Input: ""})
	if !blocked {
		t.Fatalf("expected empty ls to be blocked in read-only mode")
	}
	if !strings.Contains(reason, "avoid empty ls") {
		t.Fatalf("expected empty ls reason, got %q", reason)
	}

	blocked, reason = shouldBlockContinuationToolCall(readOnlyState, message.ToolCall{Name: "ls", Input: `{"path":"workspace/foo","recursive":true}`})
	if !blocked {
		t.Fatalf("expected recursive ls to be blocked in read-only mode")
	}
	if !strings.Contains(reason, "avoid recursive ls") {
		t.Fatalf("expected recursive ls reason, got %q", reason)
	}

	blocked, _ = shouldBlockContinuationToolCall(readOnlyState, message.ToolCall{Name: "ls", Input: `{"path":"workspace/foo"}`})
	if blocked {
		t.Fatalf("did not expect shallow explicit ls to be blocked in read-only mode")
	}

	diagnosticState := &sessionTaskState{
		ReadOnlyMode: true,
		PrimaryGoal:  "这是只读评估，请排查根因、级联影响和回归验证，根目录是 workspace/mixed_release_triage。",
	}

	blocked, reason = shouldBlockContinuationToolCall(diagnosticState, message.ToolCall{Name: "manage_plan", Input: ""})
	if !blocked {
		t.Fatalf("expected manage_plan to be blocked in diagnostic mode")
	}
	if !strings.Contains(reason, "Diagnostic mode guard") {
		t.Fatalf("expected diagnostic guard reason, got %q", reason)
	}

	blocked, reason = shouldBlockContinuationToolCall(diagnosticState, message.ToolCall{Name: "ls", Input: `{"path":"workspace/mixed_release_triage"}`})
	if !blocked {
		t.Fatalf("expected root ls to be blocked in diagnostic mode")
	}
	if !strings.Contains(reason, "root directory") {
		t.Fatalf("expected root ls reason, got %q", reason)
	}

	blocked, _ = shouldBlockContinuationToolCall(diagnosticState, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/logs/order_sync.log"}`})
	if blocked {
		t.Fatalf("did not expect targeted diagnostic read to be blocked")
	}
}

func TestShouldBlockDiagnosticDelegationToolCall(t *testing.T) {
	state := &sessionTaskState{
		ReadOnlyMode: true,
		PrimaryGoal:  "这是只读评估，请排查根因、级联影响和回归验证，根目录是 workspace/mixed_release_triage。",
	}

	for _, toolName := range []string{"agent", "delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"} {
		blocked, reason := shouldBlockContinuationToolCall(state, message.ToolCall{Name: toolName, Input: `{}`})
		if !blocked {
			t.Fatalf("expected %s to be blocked in diagnostic mode", toolName)
		}
		if !strings.Contains(reason, "first-hand evidence") {
			t.Fatalf("expected direct-evidence diagnostic guard reason for %s, got %q", toolName, reason)
		}
	}
}

func TestClassifyExecutionJournalType(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{tool: "read", want: "read"},
		{tool: "grep", want: "inspect"},
		{tool: "patch", want: "patch"},
		{tool: "write", want: "write"},
		{tool: "run_command", want: "command"},
		{tool: "run_tests", want: "test"},
		{tool: "fetch_webpage", want: "external"},
		{tool: "unknown_tool", want: "tool"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := classifyExecutionJournalType(tt.tool); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestExtractTaskStateFileTargets(t *testing.T) {
	text := "请检查 config/prod.yaml、logs/order_sync.log 和 docs/release_runbook.md 中的配置漂移"
	targets := extractTaskStateFileTargets(text, 2)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0] != "config/prod.yaml" || targets[1] != "logs/order_sync.log" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestNormalizeGenerationInput(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		hasHistory     bool
		hasAttachments bool
		want           string
	}{
		{name: "non empty content preserved", content: "请修复这个问题", hasHistory: true, want: "请修复这个问题"},
		{name: "empty follow up becomes continue", content: "   ", hasHistory: true, want: "请继续完成任务。"},
		{name: "empty first turn becomes greeting", content: "", hasHistory: false, want: "你好。"},
		{name: "empty with attachments remains empty", content: "", hasHistory: true, hasAttachments: true, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGenerationInput(tt.content, tt.hasHistory, tt.hasAttachments); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestImageAttachmentMimeType(t *testing.T) {
	tests := []struct {
		name       string
		attachment message.Attachment
		want       string
	}{
		{
			name: "keep explicit image mime",
			attachment: message.Attachment{
				FileName: "capture.bin",
				MimeType: "image/png",
			},
			want: "image/png",
		},
		{
			name: "infer from file path",
			attachment: message.Attachment{
				FilePath: "C:/tmp/capture.webp",
				MimeType: "application/octet-stream",
			},
			want: "image/webp",
		},
		{
			name: "infer from file name",
			attachment: message.Attachment{
				FileName: "capture.jpeg",
			},
			want: "image/jpeg",
		},
		{
			name: "non image stays empty",
			attachment: message.Attachment{
				FileName: "report.pdf",
				MimeType: "application/pdf",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageAttachmentMimeType(tt.attachment); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestBuildAttachmentPromptInstructions(t *testing.T) {
	attachments := []message.Attachment{
		{
			FileName: "capture.png",
			FilePath: "C:/tmp/capture.png",
			MimeType: "image/png",
		},
		{
			FileName: "spec.pdf",
			FilePath: "C:/tmp/spec.pdf",
			MimeType: "application/pdf",
		},
		{
			FileName: "workspace",
			FilePath: "C:/tmp/workspace",
			MimeType: message.DirectoryAttachmentMimeType,
		},
	}

	instructions := buildAttachmentPromptInstructions(attachments)
	if !strings.Contains(instructions, "视觉分析") {
		t.Fatalf("expected image instructions to mention 视觉分析, got %q", instructions)
	}
	if !strings.Contains(instructions, "image_path") {
		t.Fatalf("expected image instructions to mention image_path, got %q", instructions)
	}
	if strings.Contains(instructions, "对图片附件，可使用 read") {
		t.Fatalf("expected image instructions to avoid read guidance, got %q", instructions)
	}
	if !strings.Contains(instructions, "对非图片附件，可使用 read 等工具") {
		t.Fatalf("expected non-image instructions to mention read, got %q", instructions)
	}
	if !strings.Contains(instructions, "User has attached the following directories") {
		t.Fatalf("expected directory section, got %q", instructions)
	}
	if !strings.Contains(instructions, "沿目录路径浏览结构并定位目标文件") {
		t.Fatalf("expected directory guidance, got %q", instructions)
	}
}

func TestShouldAllowExecutionNudges(t *testing.T) {
	if shouldAllowExecutionNudges(nil, "这是只读评估，禁止修改任何文件，请排查根因和级联影响") {
		t.Fatalf("expected read-only diagnostic input to disable execution nudges")
	}
	if !shouldAllowExecutionNudges(nil, "请直接修改并修复 backend 的构建错误") {
		t.Fatalf("expected normal fix request to allow execution nudges")
	}
	if shouldAllowExecutionNudges(&sessionTaskState{ReadOnlyMode: true, DiagnosticMode: true}, "继续补全修复顺序和回归验证") {
		t.Fatalf("expected persisted read-only diagnostic state to keep execution nudges disabled on follow-up input")
	}
}

func TestShouldEmitStreamingToolUse(t *testing.T) {
	if shouldEmitStreamingToolUse(nil) {
		t.Fatalf("nil tool call should not emit")
	}
	if !shouldEmitStreamingToolUse(&message.ToolCall{Name: "read", Input: ""}) {
		t.Fatalf("named tool call should emit even when input is still empty")
	}
	if !shouldEmitStreamingToolUse(&message.ToolCall{ID: "call-1", Name: "read", Input: ""}) {
		t.Fatalf("tool call with stable id should emit during streaming even before input is populated")
	}
	if !shouldEmitStreamingToolUse(&message.ToolCall{Name: "read", Input: `{"file_path":"config/prod.yaml"}`}) {
		t.Fatalf("concrete tool call should emit")
	}
}

func TestFilterToolsByNameRemovesVisualAnalysis(t *testing.T) {
	visualTool, err := tools.NewVisualAnalysisTool(stubVisualAnalyzer{})
	if err != nil {
		t.Fatalf("NewVisualAnalysisTool: %v", err)
	}
	active := []tools.BaseTool{tools.NewReadTool(), visualTool}

	filtered := filterToolsByName(active, tools.VisualAnalysisToolName)
	for _, tool := range filtered {
		if tool.Info().Name == tools.VisualAnalysisToolName {
			t.Fatalf("expected visual-analysis to be filtered from image attachment prompt tools")
		}
	}
	if len(filtered) != 1 || filtered[0].Info().Name != tools.ReadToolName {
		t.Fatalf("expected read tool to remain, got %#v", filtered)
	}
}

func TestBuildWritePolicySystemMessageReadOnly(t *testing.T) {
	msg := buildWritePolicySystemMessage(&sessionTaskState{ReadOnlyMode: true})
	if msg == nil {
		t.Fatalf("expected read-only write policy message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[WRITE POLICY]") {
		t.Fatalf("expected write policy marker, got %q", text)
	}
	if !strings.Contains(text, "Current session write policy: read-only") {
		t.Fatalf("expected read-only policy, got %q", text)
	}
	if !strings.Contains(text, tools.WriteToolName) || !strings.Contains(text, tools.EditToolName) || !strings.Contains(text, tools.WordCopilotToolName) {
		t.Fatalf("expected mutating tool names in policy, got %q", text)
	}
	if buildWritePolicySystemMessage(&sessionTaskState{}) != nil {
		t.Fatalf("did not expect write policy message when read-only mode is inactive")
	}
}

func TestFilterReadOnlyPromptToolsRemovesMutatingTools(t *testing.T) {
	active := []tools.BaseTool{
		tools.NewReadTool(),
		tools.NewWriteTool(nil, nil, nil),
		tools.NewPatchTool(nil, nil, nil),
		tools.NewManagePlanTool(nil),
	}

	filtered := filterReadOnlyPromptTools(active, &sessionTaskState{ReadOnlyMode: true})
	names := collectToolNames(filtered)
	if !names[tools.ReadToolName] {
		t.Fatalf("expected read tool to remain, got %#v", names)
	}
	if names[tools.WriteToolName] || names[tools.PatchToolName] || names[tools.ManagePlanToolName] {
		t.Fatalf("expected file-mutating tools to be removed from read-only prompt tools, got %#v", names)
	}
}

func TestContentPartsContainBinaryImage(t *testing.T) {
	if !contentPartsContainBinaryImage([]message.ContentPart{message.BinaryContent{MIMEType: "image/png"}}) {
		t.Fatalf("expected image binary content to be detected")
	}
	if contentPartsContainBinaryImage([]message.ContentPart{message.AttachmentMetaContent{Name: "image.png", Type: "image/png"}}) {
		t.Fatalf("metadata-only image attachment should not count as model-visible image input")
	}
}

func TestEstimateHistoryTokensUsesBoundedImageCost(t *testing.T) {
	history := []message.Message{{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "这是什么？"},
			message.BinaryContent{MIMEType: "image/png", Data: []byte(strings.Repeat("x", 2_000_000))},
		},
	}}

	got := estimateHistoryTokens(history)
	if got > 10_000 {
		t.Fatalf("expected image token estimate to be bounded, got %d", got)
	}
}

func TestPrepareConversationContextStripsHistoricalBinaryImages(t *testing.T) {
	agentInstance := &agent{}
	history := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "这是什么？"},
				message.BinaryContent{MIMEType: "image/png", Data: []byte("image-bytes")},
			},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "这是一个瀑布。"},
			},
		},
	}

	prepared, err := agentInstance.prepareConversationContextForModel(context.Background(), "session-image-history", history, models.Model{ContextWindow: 256000})
	if err != nil {
		t.Fatalf("prepareConversationContextForModel returned error: %v", err)
	}
	for _, msg := range prepared {
		for _, part := range msg.Parts {
			if _, ok := part.(message.BinaryContent); ok {
				t.Fatalf("expected historical image binary to be stripped, got %#v", prepared)
			}
		}
	}
	if !strings.Contains(prepared[0].Content().String(), "Historical image attachment omitted") {
		t.Fatalf("expected historical image placeholder, got %q", prepared[0].Content().String())
	}
}

func TestResolveContextConfigForReservedTokensShrinksHistoryBudget(t *testing.T) {
	base := GetContextConfig(256000)
	reserved := resolveContextConfigForReservedTokens(256000, 90000)

	if reserved.MaxHistoryChars >= base.MaxHistoryChars {
		t.Fatalf("expected reserved-token config to shrink history chars, got %d vs base %d", reserved.MaxHistoryChars, base.MaxHistoryChars)
	}
	if reserved.ToolOutputMaxChars >= base.ToolOutputMaxChars {
		t.Fatalf("expected reserved-token config to shrink tool output budget, got %d vs base %d", reserved.ToolOutputMaxChars, base.ToolOutputMaxChars)
	}
}

func TestPrepareConversationContextForModelWithReservedTokensFoldsOversizedHotToolResult(t *testing.T) {
	agentInstance := &agent{}
	hugeOutput := strings.Repeat("tool-output-line\n", 5000)
	history := []message.Message{
		{
			Role: message.System,
			Parts: []message.ContentPart{
				message.TextContent{Text: "system prompt"},
			},
		},
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "请继续分析最近的工具输出"},
			},
		},
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "tool-1", Name: "run_command", Content: hugeOutput},
			},
		},
	}

	prepared, err := agentInstance.prepareConversationContextForModelWithReservedTokens(context.Background(), "session-1", history, models.Model{ContextWindow: 256000}, 180000)
	if err != nil {
		t.Fatalf("prepareConversationContextForModelWithReservedTokens returned error: %v", err)
	}

	var folded message.ToolResult
	found := false
	for _, msg := range prepared {
		for _, part := range msg.Parts {
			toolResult, ok := part.(message.ToolResult)
			if !ok {
				continue
			}
			folded = toolResult
			found = true
		}
	}
	if !found {
		t.Fatalf("expected prepared history to retain a tool result")
	}
	if len(folded.Content) >= len(hugeOutput) {
		t.Fatalf("expected oversized hot tool result to be folded, got len=%d original=%d", len(folded.Content), len(hugeOutput))
	}
}

func TestBuildToolExecutionIDStableAcrossMessageSplit(t *testing.T) {
	withID := &message.ToolCall{ID: "call-1", Index: 0, Name: "read", Input: `{"file_path":"README.md"}`}
	if left, right := buildToolExecutionID("assistant-thinking", withID), buildToolExecutionID("assistant-tool", withID); left != right {
		t.Fatalf("expected tool execution id to stay stable across message split, got %q vs %q", left, right)
	}

	withoutID := &message.ToolCall{Index: 2, Name: "grep", Input: `{"pattern":"tool_call"}`}
	if left, right := buildToolExecutionID("assistant-thinking", withoutID), buildToolExecutionID("assistant-tool", withoutID); left != right {
		t.Fatalf("expected fallback execution id to stay stable across message split, got %q vs %q", left, right)
	}
}

func TestExecutionJournalTarget(t *testing.T) {
	metadata := `{"file_path":"backend/internal/llm/agent/agent.go","command":"go test ./..."}`
	if got := executionJournalTarget("read", metadata, ""); got != "backend/internal/llm/agent/agent.go" {
		t.Fatalf("expected file_path target, got %q", got)
	}

	if got := executionJournalTarget("run_command", `{"command":"go test ./..."}`, ""); got != "go test ./..." {
		t.Fatalf("expected command target fallback, got %q", got)
	}

	if got := executionJournalTarget("read", "not-json", "已读取 config/prod.yaml 并确认仍指向旧接口"); got != "config/prod.yaml" {
		t.Fatalf("expected summary file target fallback, got %q", got)
	}

	if got := executionJournalTarget("read", "backend/config.yaml", ""); got != "backend/config.yaml" {
		t.Fatalf("expected direct path metadata fallback, got %q", got)
	}
}

func TestRemoveResolvedTaskStateIssues(t *testing.T) {
	issues := []string{
		"read | logs/order_sync.log | timed out after 30s",
		"read | config/prod.yaml | sync endpoint still points to /v1/orders/sync",
		"run_command | go test ./... | tests failed",
	}

	remaining := removeResolvedTaskStateIssues(issues, "read", "logs/order_sync.log")
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining issues, got %d", len(remaining))
	}
	if remaining[0] != "read | config/prod.yaml | sync endpoint still points to /v1/orders/sync" {
		t.Fatalf("expected unrelated read issue to remain, got %#v", remaining)
	}

	unchanged := removeResolvedTaskStateIssues(issues, "read", "")
	if len(unchanged) != len(issues) {
		t.Fatalf("expected issue list to remain unchanged when target is empty")
	}
}

func TestAppendExecutionJournalEntry(t *testing.T) {
	entries := []taskExecutionJournalEntry{{Tool: "read", Type: "read", Status: "success", Target: "a.go", Summary: "read a.go", Timestamp: 1}}
	duplicate := taskExecutionJournalEntry{Tool: "read", Type: "read", Status: "success", Target: "a.go", Summary: "read a.go", Timestamp: 2}
	entries = appendExecutionJournalEntry(entries, duplicate, 8)
	if len(entries) != 1 {
		t.Fatalf("expected duplicate journal entry to be merged, got %d entries", len(entries))
	}
	if entries[0].Timestamp != 2 {
		t.Fatalf("expected merged entry timestamp to refresh")
	}

	newEntry := taskExecutionJournalEntry{Tool: "run_command", Type: "command", Status: "error", Target: "go test ./...", Summary: "build failed", Timestamp: 3}
	entries = appendExecutionJournalEntry(entries, newEntry, 8)
	if len(entries) != 2 {
		t.Fatalf("expected new execution journal entry to append, got %d entries", len(entries))
	}
	if entries[1].Tool != "run_command" {
		t.Fatalf("expected appended command entry, got %q", entries[1].Tool)
	}
}

func TestAppendAssistantFindings(t *testing.T) {
	entries := appendAssistantFindings(nil, "已确认 config/prod.yaml 仍指向 /v1/orders/sync，且 logs/order_sync.log 显示 timed out after 30s")
	if len(entries) != 1 {
		t.Fatalf("expected one finding entry, got %d", len(entries))
	}
	if entries[0].Target != "config/prod.yaml, logs/order_sync.log" {
		t.Fatalf("unexpected finding target: %q", entries[0].Target)
	}
	if entries[0].Source != "assistant" {
		t.Fatalf("expected assistant source, got %q", entries[0].Source)
	}
}

func TestExtractStructuredFactsFromReadResult(t *testing.T) {
	t.Run("service python assignments are extracted", func(t *testing.T) {
		content := `<file_content>
     1|REQUEST_TIMEOUT_SECONDS = 30
     2|MAX_RETRIES = 1
     3|            "synced_orders": 12480,
     4|            "api_version": "v1",
</file_content>`
		facts := extractStructuredFactsFromReadResult("service/order_sync.py", content)
		if len(facts) < 4 {
			t.Fatalf("expected at least 4 structured facts, got %d", len(facts))
		}
		if facts[0].Artifact != "service/order_sync.py" {
			t.Fatalf("unexpected artifact: %q", facts[0].Artifact)
		}
	})

	t.Run("log and docs lines are extracted", func(t *testing.T) {
		logContent := `<file_content>
     1|2026-04-12T09:14:02Z ERROR batch=A17 POST /v1/orders/sync timed out after 30s
     2|2026-04-12T09:14:03Z WARN cache fallback reused snapshot synced_orders=12480 api_version=v1
</file_content>`
		logFacts := extractStructuredFactsFromReadResult("logs/order_sync.log", logContent)
		if len(logFacts) < 4 {
			t.Fatalf("expected multiple log facts, got %d", len(logFacts))
		}

		docContent := "1. 使用 curl 校验接口：curl https://prod.example.com/v1/orders/sync"
		docFacts := extractStructuredFactsFromReadResult("docs/release_runbook.md", docContent)
		if len(docFacts) == 0 {
			t.Fatalf("expected curl fact from docs")
		}
		if docFacts[0].Marker != "curl" {
			t.Fatalf("expected curl marker, got %q", docFacts[0].Marker)
		}
	})
}

func TestBuildStructuredFactsSystemMessage(t *testing.T) {
	msg := buildStructuredFactsSystemMessage(&sessionTaskState{
		StructuredFacts: []taskStructuredFact{
			{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync", ExpectedValue: "/v2/orders/sync", Evidence: "sync_endpoint: /v1/orders/sync"},
			{Artifact: "logs/order_sync.log", Marker: "timed out after", CurrentValue: "30s", Evidence: "POST /v1/orders/sync timed out after 30s"},
		},
	})
	if msg == nil {
		t.Fatalf("expected structured facts message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[STRUCTURED FACTS]") {
		t.Fatalf("expected structured facts marker in message")
	}
	if !strings.Contains(text, "config/prod.yaml") || !strings.Contains(text, "current=/v1/orders/sync") || !strings.Contains(text, "expected=/v2/orders/sync") {
		t.Fatalf("expected structured fact contents, got %q", text)
	}
	if !strings.Contains(text, "copy that exact corrected value verbatim") {
		t.Fatalf("expected exact corrected value guidance, got %q", text)
	}
}

func TestBuildWorkingStateSystemMessage(t *testing.T) {
	msg := buildWorkingStateSystemMessage(&sessionTaskState{
		PrimaryGoal:       "修复上下文装配链路",
		Status:            "active",
		CurrentFocus:      "合并多个工作态 prompt",
		CorrectionMode:    true,
		CorrectionHint:    "用户纠正了任务方向；先按纠正后的目标重新对齐执行路径，停止沿旧理解继续扩展。",
		CorrectionDecay:   2,
		ContinuationMode:  true,
		ContinuationCount: 2,
		DiagnosticMode:    true,
		DiagnosticPhase:   "service-analysis",
		DiagnosticRoot:    "workspace/mixed_release_triage",
		DiagnosticTargets: []string{
			"workspace/mixed_release_triage/logs/order_sync.log",
			"workspace/mixed_release_triage/service/order_sync.py",
		},
		RecentProgress:    []string{"已拆分静态前缀与动态尾部", "已接入统一 Working State"},
		StructuredFacts:   []taskStructuredFact{{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync", ExpectedValue: "/v2/orders/sync"}},
		FindingsLedger:    []taskFindingLedgerEntry{{Target: "config/prod.yaml", Summary: "确认仍指向旧接口"}},
		ExecutionJournal:  []taskExecutionJournalEntry{{Tool: "read", Status: "success", Target: "config/prod.yaml", Summary: "已读取关键配置"}},
		OutstandingIssues: []string{"tool_result 仍然过长"},
		LastTool:          "read",
		LastToolStatus:    "success",
		LastToolSummary:   "已读取 config/prod.yaml",
		CachedWordTableLocator: &sessionWordTableLocator{
			ReaderSessionID: "reader-session-1",
			TableID:         "tbl-risk",
			RowIndex:        intPtr(2),
			CellIndex:       intPtr(1),
			Query:           "通过率",
			Source:          "exploration",
			EvidenceSummary: "最近一次 auramate-word-reader 已锁定目标单元格；query=通过率",
		},
		NextStep: "继续压缩动态尾部",
	})
	if msg == nil {
		t.Fatalf("expected working state message")
	}
	text := msg.Content().Text
	for _, expected := range []string{"[WORKING STATE]", "Goal: 修复上下文装配链路", "Correction mode: active (2)", "Correction target: 用户纠正了任务方向；先按纠正后的目标重新对齐执行路径，停止沿旧理解继续扩展。", "Diagnostic mode: active (phase=service-analysis)", "Confirmed facts:", "Recent execution journal:", "Word table locator cache:", "update_table_cell with new_text shorthand", "Next step: 继续压缩动态尾部"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in working state message, got %q", expected, text)
		}
	}
}

func TestBuildWorkingStateSystemMessageCompressesMutationHeavyState(t *testing.T) {
	msg := buildWorkingStateSystemMessage(&sessionTaskState{
		PrimaryGoal:    "收敛 mutation-heavy prompt",
		Status:         "active",
		RecentProgress: []string{"edit: 已修改 backend/internal/alpha.go", "write: 已写入 backend/internal/beta.go", "run_tests: agent 包测试通过"},
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "edit", Type: "patch", Status: "success", Target: "backend/internal/alpha.go", Summary: "updated alpha"},
			{Tool: "search_replace", Type: "patch", Status: "success", Target: "backend/internal/beta.go", Summary: "updated beta"},
			{Tool: "write", Type: "write", Status: "success", Target: "backend/internal/gamma.go", Summary: "updated gamma"},
			{Tool: "run_tests", Type: "test", Status: "success", Target: "go test ./internal/llm/agent", Summary: "agent 包测试通过"},
		},
		OutstandingIssues: []string{formatTaskStateIssue("edit", "backend/internal/alpha.go", "仍需验证是否有回归")},
		LastTool:          "edit",
		LastToolStatus:    "success",
		LastToolSummary:   "已修改 backend/internal/gamma.go",
		NextStep:          "先验证再继续",
	})
	if msg == nil {
		t.Fatalf("expected working state message")
	}
	text := msg.Content().Text
	for _, expected := range []string{"Mutation-heavy mode:", "Validation target: backend/internal/alpha.go", "Recent validation / blockers:", "agent 包测试通过", "Last tool: edit (success): recent mutation recorded; verify before editing again"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in compressed working state message, got %q", expected, text)
		}
	}
	for _, forbidden := range []string{"backend/internal/beta.go", "backend/internal/gamma.go"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("expected compressed working state to drop repeated mutation target %q, got %q", forbidden, text)
		}
	}
}

func TestBuildTaskStatePromptLayersIncludeExecutionJournalForMutationHeavyState(t *testing.T) {
	a := &agent{}
	layers := a.buildTaskStatePromptLayers("session-1", &sessionTaskState{
		PrimaryGoal: "收敛 mutation-heavy prompt",
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "edit", Type: "patch", Status: "success", Target: "backend/internal/alpha.go", Summary: "updated alpha"},
			{Tool: "search_replace", Type: "patch", Status: "success", Target: "backend/internal/beta.go", Summary: "updated beta"},
			{Tool: "write", Type: "write", Status: "success", Target: "backend/internal/gamma.go", Summary: "updated gamma"},
			{Tool: "run_tests", Type: "test", Status: "success", Target: "go test ./internal/llm/agent", Summary: "agent 包测试通过"},
		},
		OutstandingIssues: []string{formatTaskStateIssue("edit", "backend/internal/alpha.go", "仍需验证是否有回归")},
	})
	if len(layers) != 2 {
		t.Fatalf("expected working state and execution journal layers, got %d", len(layers))
	}
	if !strings.Contains(layers[0].Content().Text, "[WORKING STATE]") {
		t.Fatalf("expected first layer to be working state, got %q", layers[0].Content().Text)
	}
	if !strings.Contains(layers[1].Content().Text, "[EXECUTION JOURNAL]") {
		t.Fatalf("expected second layer to be execution journal, got %q", layers[1].Content().Text)
	}
	if !strings.Contains(layers[1].Content().Text, "Current mutation anchor: backend/internal/alpha.go") {
		t.Fatalf("expected execution journal layer to retain mutation anchor, got %q", layers[1].Content().Text)
	}
}

func TestBuildTaskStatePromptLayersIncludeChildWorkerCheckpointLayer(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	orch.workerMeta["worker-1"] = &workerRuntime{
		TaskID:          "task-1",
		ParentSessionID: "session-1",
		Status:          "failed",
		LastActivityAt:  time.Unix(1710000000, 0),
		FailureType:     "server_error",
		LastError:       "503 service unavailable",
		Checkpoint: &WorkerCheckpoint{
			Version:      workerCheckpointVersion,
			OriginalTask: "collect recovery evidence",
			PendingGoal:  "retry provider call with smaller output",
			ResumeHint:   "继续同一任务，不要重复已完成的 grep/read 步骤",
			Summary:      "provider retry exhausted",
			RecentToolSteps: []WorkerCheckpointStep{{
				Tool:    "grep",
				Status:  "success",
				Summary: "定位到 provider 503 路径",
			}},
		},
	}
	a := &agent{orchestrator: orch}
	layers := a.buildTaskStatePromptLayers("session-1", &sessionTaskState{PrimaryGoal: "处理子 worker 卡住", Status: "active"})
	if len(layers) != 2 {
		t.Fatalf("expected working state plus child worker layer, got %d", len(layers))
	}
	text := layers[1].Content().Text
	for _, expected := range []string{"[CHILD WORKER CHECKPOINTS]", "pending_goal=retry provider call with smaller output", "Blocker: server_error => 503 service unavailable", "Resume hint: 继续同一任务，不要重复已完成的 grep/read 步骤", "Recoverability: resume available via manage_worker"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in child worker checkpoint layer, got %q", expected, text)
		}
	}
}

func TestBuildTaskStatePromptLayersDoNotAdvertiseResumeForRunningWorker(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	orch.workerMeta["worker-1"] = &workerRuntime{
		TaskID:          "task-1",
		ParentSessionID: "session-1",
		Status:          "running",
		LastActivityAt:  time.Unix(1710000000, 0),
		Checkpoint: &WorkerCheckpoint{
			Version:      workerCheckpointVersion,
			OriginalTask: "collect recovery evidence",
			PendingGoal:  "still collecting logs",
			Summary:      "worker is in progress",
		},
	}
	a := &agent{orchestrator: orch}
	layers := a.buildTaskStatePromptLayers("session-1", &sessionTaskState{PrimaryGoal: "处理子 worker 卡住", Status: "active"})
	if len(layers) != 2 {
		t.Fatalf("expected working state plus child worker layer, got %d", len(layers))
	}
	text := layers[1].Content().Text
	if !strings.Contains(text, "Recoverability: worker still running; inspect or wait before resume") {
		t.Fatalf("expected running worker recoverability warning, got %q", text)
	}
	if strings.Contains(text, "Recoverability: resume available via manage_worker") {
		t.Fatalf("expected running worker not to advertise resume availability, got %q", text)
	}
}

func TestShouldAutoSummarizeSkipsFreshCheckpointUnderPressure(t *testing.T) {
	sessionID := "session-fresh-summary"
	a := &agent{
		provider: &capturingSummaryProvider{content: "summary"},
		sessions: newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "fresh", SummaryMessageID: "summary-msg"}),
		messages: newOrderedMessageService(message.Message{ID: "summary-msg", SessionID: sessionID, Role: message.User, UpdatedAt: 1000, Parts: []message.ContentPart{message.TextContent{Text: "[System Summary]: 已压缩"}}}),
	}
	msgHistory := []message.Message{
		{ID: "msg-1", SessionID: sessionID, Role: message.User, UpdatedAt: 2000, Parts: []message.ContentPart{message.TextContent{Text: "后续小量更新 1"}}},
		{ID: "msg-2", SessionID: sessionID, Role: message.Assistant, UpdatedAt: 3000, Parts: []message.ContentPart{message.TextContent{Text: "后续小量更新 2"}}},
		{ID: "msg-3", SessionID: sessionID, Role: message.User, UpdatedAt: 4000, Parts: []message.ContentPart{message.TextContent{Text: "后续小量更新 3"}}},
	}
	decision := a.shouldAutoSummarize(context.Background(), sessionID, msgHistory, 700, 1000, false)
	if decision.Should {
		t.Fatalf("expected fresh checkpoint to suppress auto summarize under moderate pressure, got %+v", decision)
	}
	if !decision.CheckpointFresh {
		t.Fatalf("expected checkpoint to be treated as fresh, got %+v", decision)
	}
}

func TestShouldAutoSummarizeTriggersForMutationHeavyPressure(t *testing.T) {
	sessionID := "session-mutation-heavy-summary"
	_, conn := newAgentMemoryTestService(t)
	q := db.New(conn)
	state := &sessionTaskState{
		PrimaryGoal: "收敛子链路",
		Status:      "active",
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "edit", Type: "patch", Status: "success", Target: "backend/internal/alpha.go", Summary: "updated alpha"},
			{Tool: "write", Type: "write", Status: "success", Target: "backend/internal/beta.go", Summary: "updated beta"},
		},
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if _, err := q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{Key: taskStateKey(sessionID), Value: sql.NullString{String: string(payload), Valid: true}, UpdatedAt: 5000}); err != nil {
		t.Fatalf("persist task state: %v", err)
	}
	a := &agent{
		provider: &capturingSummaryProvider{content: "summary"},
		sessions: newSummarizeTestSessionService(session.Session{ID: sessionID, Title: "mutation-heavy", SummaryMessageID: "summary-msg"}),
		messages: newOrderedMessageService(message.Message{ID: "summary-msg", SessionID: sessionID, Role: message.User, UpdatedAt: 1000, Parts: []message.ContentPart{message.TextContent{Text: "[System Summary]: 已压缩"}}}),
		q:        q,
	}
	msgHistory := []message.Message{{ID: "msg-1", SessionID: sessionID, Role: message.User, UpdatedAt: 360000 + 1000, Parts: []message.ContentPart{message.TextContent{Text: "后续大段修改"}}}}
	decision := a.shouldAutoSummarize(context.Background(), sessionID, msgHistory, 700, 1000, false)
	if !decision.Should || decision.Reason != "mutation_heavy_context_pressure" {
		t.Fatalf("expected mutation-heavy pressure to trigger auto summarize, got %+v", decision)
	}
}

func TestShouldAutoSummarizeSkipsDuringFailureCooldown(t *testing.T) {
	sessionID := "session-auto-summary-cooldown"
	a := &agent{provider: &capturingSummaryProvider{content: "summary"}}
	state := &SummarizationState{
		failureCount:    1,
		lastAttemptTime: time.Now(),
		backoffDuration: 2 * time.Second,
	}
	a.sessionSummarizeState.Store(sessionID, state)

	msgHistory := make([]message.Message, 46)
	for i := range msgHistory {
		msgHistory[i] = message.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			SessionID: sessionID,
			Role:      message.User,
			Parts:     []message.ContentPart{message.TextContent{Text: fmt.Sprintf("message %d", i)}},
		}
	}

	decision := a.shouldAutoSummarize(context.Background(), sessionID, msgHistory, 900, 1000, false)
	if decision.Should {
		t.Fatalf("expected cooldown to suppress auto summarize, got %+v", decision)
	}

	state.lastAttemptTime = time.Now().Add(-3 * time.Second)
	decision = a.shouldAutoSummarize(context.Background(), sessionID, msgHistory, 900, 1000, false)
	if !decision.Should || decision.Reason != "extreme_context_pressure" {
		t.Fatalf("expected auto summarize to resume after cooldown, got %+v", decision)
	}
}

func TestUpdateTaskStateFromToolResultsCachesWordTableLocator(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}

	payloadBytes, err := json.Marshal(map[string]any{
		"status":  "ok",
		"session": map[string]any{"id": "reader-session-2"},
		"query": map[string]any{
			"text": "通过率",
			"hits": []map[string]any{{
				"table_id":   "tbl-budget",
				"row_index":  1,
				"cell_index": 1,
				"preview":    "通过率 85%",
			}},
		},
		"exploration": map[string]any{
			"resolved": map[string]any{
				"table_id":   "tbl-budget",
				"row_index":  1,
				"cell_index": 1,
			},
			"resolved_hit": map[string]any{
				"preview": "通过率 85%",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	toolResults := &message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{
		Name:    "auramate-word-reader",
		Content: string(payloadBytes),
		IsError: false,
	}}}
	if err := agentUnderTest.updateTaskStateFromToolResults(context.Background(), "session-word-table-cache", toolResults); err != nil {
		t.Fatalf("update task state from tool results: %v", err)
	}

	updated, err := agentUnderTest.loadTaskState(context.Background(), "session-word-table-cache")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if updated == nil || updated.CachedWordTableLocator == nil {
		t.Fatalf("expected cached word table locator, got %+v", updated)
	}
	locator := updated.CachedWordTableLocator
	if locator.ReaderSessionID != "reader-session-2" || locator.TableID != "tbl-budget" {
		t.Fatalf("unexpected cached locator identity: %+v", locator)
	}
	if locator.RowIndex == nil || *locator.RowIndex != 1 || locator.CellIndex == nil || *locator.CellIndex != 1 {
		t.Fatalf("unexpected cached locator coordinates: %+v", locator)
	}
	if locator.Source != "exploration" {
		t.Fatalf("expected exploration source to win, got %+v", locator)
	}
	workingStateMsg := buildWorkingStateSystemMessage(updated)
	if workingStateMsg == nil {
		t.Fatalf("expected working state message")
	}
	workingStateText := workingStateMsg.Content().Text
	for _, expected := range []string{"reader_session_id=reader-session-2", "table_id=tbl-budget", "row_index=1", "cell_index=1", "update_table_cell with new_text shorthand"} {
		if !strings.Contains(workingStateText, expected) {
			t.Fatalf("expected %q in working state cache prompt, got %q", expected, workingStateText)
		}
	}
}

func TestUpdateTaskStateFromToolResultsForcesCorrectionHandoffAfterRepeatedVerification(t *testing.T) {
	_, conn := newAgentMemoryTestService(t)
	agentUnderTest := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		q:        db.New(conn),
	}
	sessionID := "session-correction-verification-loop"
	initialState := &sessionTaskState{
		PrimaryGoal:     "fix the reported issue",
		CorrectionMode:  true,
		CorrectionDecay: maxCorrectionDecay,
		Status:          "active",
	}
	payload, err := json.Marshal(initialState)
	if err != nil {
		t.Fatalf("marshal initial state: %v", err)
	}
	if _, err := agentUnderTest.q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{
		Key:       taskStateKey(sessionID),
		Value:     sql.NullString{String: string(payload), Valid: true},
		UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("persist initial state: %v", err)
	}

	toolResults := &message.Message{Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "call-1", Name: "run_command", Content: "verified"},
	}}
	for i := 0; i < maxCorrectionVerificationTurns; i++ {
		if err := agentUnderTest.updateTaskStateFromToolResults(context.Background(), sessionID, toolResults); err != nil {
			t.Fatalf("update task state on turn %d: %v", i+1, err)
		}
	}

	got, err := agentUnderTest.loadTaskState(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if got == nil {
		t.Fatalf("expected task state")
	}
	if got.CorrectionVerificationTurns != maxCorrectionVerificationTurns {
		t.Fatalf("expected %d correction verification turns, got %d", maxCorrectionVerificationTurns, got.CorrectionVerificationTurns)
	}
	if got.Status != "waiting_user" {
		t.Fatalf("expected waiting_user status after repeated verification, got %q", got.Status)
	}
	if got.NextStep != "Await fresh user evidence before continuing the same correction path." {
		t.Fatalf("unexpected next step: %q", got.NextStep)
	}
}

func TestBuildMemoryCapsuleMessage(t *testing.T) {
	msg := buildMemoryCapsuleMessage([]db.Memory{
		{Category: "preference", Content: "用户偏好简洁中文回复"},
		{Category: "experience", Content: "大工具输出应先摘要再进入上下文"},
	})
	if msg == nil {
		t.Fatalf("expected memory capsule message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[MEMORY CAPSULE]") || !strings.Contains(text, "用户偏好简洁中文回复") || !strings.Contains(text, "大工具输出应先摘要再进入上下文") {
		t.Fatalf("unexpected memory capsule content: %q", text)
	}
}

func TestCachedStaticSystemMessages(t *testing.T) {
	a := &agent{}
	buildCount := 0

	first := a.cachedStaticSystemMessages("coding\x1fopenai", func() []message.Message {
		buildCount++
		return []message.Message{{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static-anchor-v1"}},
		}}
	})
	second := a.cachedStaticSystemMessages("coding\x1fopenai", func() []message.Message {
		buildCount++
		return []message.Message{{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static-anchor-v2"}},
		}}
	})

	if buildCount != 1 {
		t.Fatalf("expected static prompt builder to run once for same key, got %d", buildCount)
	}
	if got := first[0].Content().String(); got != "static-anchor-v1" {
		t.Fatalf("unexpected first static content: %q", got)
	}
	if got := second[0].Content().String(); got != "static-anchor-v1" {
		t.Fatalf("expected cached static content to be reused, got %q", got)
	}

	third := a.cachedStaticSystemMessages("coding\x1fantrophic", func() []message.Message {
		buildCount++
		return []message.Message{{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static-anchor-v3"}},
		}}
	})
	if buildCount != 2 {
		t.Fatalf("expected static prompt builder to rerun for new key, got %d", buildCount)
	}
	if got := third[0].Content().String(); got != "static-anchor-v3" {
		t.Fatalf("unexpected rebuilt static content: %q", got)
	}
}

func TestCachedMemoryCapsuleMessage(t *testing.T) {
	a := &agent{}
	memories := []db.Memory{{Category: "preference", Content: "用户偏好简洁中文回复"}}

	first := a.cachedMemoryCapsuleMessage("session-1", 1, memories)
	second := a.cachedMemoryCapsuleMessage("session-1", 1, memories)
	if first == nil || second == nil {
		t.Fatalf("expected memory capsule cache to return a message")
	}
	if first != second {
		t.Fatalf("expected identical capsule version to reuse cached message pointer")
	}

	third := a.cachedMemoryCapsuleMessage("session-1", 2, memories)
	if third == nil {
		t.Fatalf("expected rebuilt memory capsule message")
	}
	if third == first {
		t.Fatalf("expected new memory version to rebuild capsule message")
	}

	empty := a.cachedMemoryCapsuleMessage("session-1", 3, nil)
	if empty != nil {
		t.Fatalf("expected nil capsule for empty memories")
	}
}

func TestBuildTurnContextMessage(t *testing.T) {
	msg := buildTurnContextMessage(
		[]string{"- [knowledge] provider cache minimum prefix is 1024 tokens"},
		[]string{"Here are recent relevant messages that may have fallen outside the hot window:", "- [user] 继续按新的方式重构上下文"},
	)
	if msg == nil {
		t.Fatalf("expected turn context message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[TURN CONTEXT]") || !strings.Contains(text, "## Relevant Knowledge") || !strings.Contains(text, "## Warm Context") {
		t.Fatalf("unexpected turn context content: %q", text)
	}
}

func newAgentMemoryTestService(t *testing.T) (memory.Service, *sql.DB) {
	t.Helper()

	// Knowledge recall depends on the MySQL ngram FULLTEXT index, so this
	// fixture uses a real MySQL test schema instead of in-memory SQLite.
	conn := dbtest.OpenMySQL(t)

	q := db.New(conn)
	mem := memory.NewService(q, conn, nil)
	if _, err := q.CreateKnowledgeNode(context.Background(), db.CreateKnowledgeNodeParams{
		ID:             "fact-1",
		Layer:          3,
		Category:       "fact",
		Content:        "Redis endpoint 是 redis://localhost:6379",
		Keywords:       sql.NullString{String: "redis endpoint cache", Valid: true},
		Source:         sql.NullString{String: "user_input", Valid: true},
		Confidence:     sql.NullFloat64{Float64: 1.0, Valid: true},
		CreatedAt:      100,
		UpdatedAt:      200,
		LastAccessedAt: sql.NullInt64{Int64: 200, Valid: true},
	}); err != nil {
		t.Fatalf("seed fact memory: %v", err)
	}

	return mem, conn
}

func TestRetrieveRAGContextIncludesDynamicFactRecall(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{memories: mem}
	critical := ragMemoryCache{}
	deep := ragMemoryCache{}

	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "redis endpoint 是什么", "normal", "search", &critical, &deep)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.TurnContext == nil {
		t.Fatalf("expected turn context with recalled fact memory")
	}
	text := layers.TurnContext.Content().Text
	if !strings.Contains(text, "Redis endpoint 是 redis://localhost:6379") {
		t.Fatalf("expected dynamic fact recall in turn context, got %q", text)
	}
	if len(deep.Memories) == 0 {
		t.Fatalf("expected deep memory cache to be populated")
	}
	if stored, ok := a.sessionMemories.Load("session-1"); !ok || len(stored.([]string)) == 0 {
		t.Fatalf("expected used memory ids to be stored for session")
	}
}

func TestRetrieveRAGContextDoesNotInvalidateCacheOnPartialRecall(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	if _, err := mem.Add(context.Background(), "fact", "Postgres DSN 是 postgres://localhost:5432/app", "user_input"); err != nil {
		t.Fatalf("seed second fact memory: %v", err)
	}

	a := &agent{memories: mem}
	critical := ragMemoryCache{}
	deep := ragMemoryCache{}

	if _, err := a.retrieveRAGContext(context.Background(), "session-1", "redis endpoint 是什么", "normal", "search", &critical, &deep); err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if a.memoryCriticalVersion != 0 {
		t.Fatalf("expected critical cache version to remain stable, got %d", a.memoryCriticalVersion)
	}
	if a.memoryDynamicVersion != 0 {
		t.Fatalf("expected dynamic cache version to remain stable, got %d", a.memoryDynamicVersion)
	}
	if a.memoryCriticalCacheLoadedAt.IsZero() {
		t.Fatalf("expected retrieval to refresh critical cache timestamp")
	}
	if a.memoryDynamicCacheLoadedAt.IsZero() {
		t.Fatalf("expected retrieval to refresh dynamic cache timestamp")
	}
	if len(deep.Memories) == 0 {
		t.Fatalf("expected deep cache to contain recalled memories")
	}
	if len(deep.Memories) >= 2 {
		t.Fatalf("expected query-specific deep cache, got %d memories", len(deep.Memories))
	}
	if !strings.Contains(deep.Memories[0].Content, "redis://localhost:6379") {
		t.Fatalf("expected deep cache to keep only recalled redis fact, got %q", deep.Memories[0].Content)
	}
}

func TestRetrieveRAGContextSeparatesCriticalAndDynamicCacheFreshness(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	if _, err := mem.Add(context.Background(), "preference", "请始终使用中文", "user_input"); err != nil {
		t.Fatalf("seed critical memory: %v", err)
	}

	a := &agent{memories: mem}
	critical := ragMemoryCache{
		Memories: []db.Memory{{
			ID:       "stale-critical",
			Category: "preference",
			Content:  "STALE_CRITICAL_MEMORY",
		}},
		LoadedAt: time.Now().Add(-10 * time.Minute),
	}
	deep := ragMemoryCache{
		Memories: []db.Memory{{
			ID:       "deep-cache",
			Category: "fact",
			Content:  "CACHE_ONLY_DEEP redis://cache-only:6379",
		}},
		LoadedAt: time.Now(),
	}

	a.memoryCacheMu.Lock()
	a.memoryCriticalCacheLoadedAt = time.Now().Add(-10 * time.Minute)
	a.memoryDynamicCacheLoadedAt = time.Now()
	a.memoryCacheMu.Unlock()

	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "redis endpoint 是什么", "normal", "search", &critical, &deep)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.MemoryCapsule == nil {
		t.Fatalf("expected memory capsule after critical reload")
	}
	if layers.TurnContext == nil {
		t.Fatalf("expected turn context when fresh deep cache is reused")
	}

	memoryText := layers.MemoryCapsule.Content().Text
	if strings.Contains(memoryText, "STALE_CRITICAL_MEMORY") {
		t.Fatalf("expected expired critical cache to be ignored, got %q", memoryText)
	}
	if !strings.Contains(memoryText, "请始终使用中文") {
		t.Fatalf("expected critical memories to be reloaded from source, got %q", memoryText)
	}

	turnText := layers.TurnContext.Content().Text
	if !strings.Contains(turnText, "CACHE_ONLY_DEEP redis://cache-only:6379") {
		t.Fatalf("expected fresh deep cache to be reused, got %q", turnText)
	}

	a.memoryCacheMu.RLock()
	criticalFreshAt := a.memoryCriticalCacheLoadedAt
	dynamicFreshAt := a.memoryDynamicCacheLoadedAt
	a.memoryCacheMu.RUnlock()
	if criticalFreshAt.IsZero() || dynamicFreshAt.IsZero() {
		t.Fatalf("expected both cache timestamps to be set")
	}
	if criticalFreshAt.Before(time.Now().Add(-1 * time.Minute)) {
		t.Fatalf("expected critical cache timestamp to refresh independently, got %v", criticalFreshAt)
	}
}

func TestRetrieveRAGContextIgnoresFreshTimestampWithoutLocalCachePayload(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	if _, err := mem.Add(context.Background(), "preference", "请始终使用中文", "user_input"); err != nil {
		t.Fatalf("seed critical memory: %v", err)
	}
	if _, err := mem.Add(context.Background(), "fact", "Redis endpoint 是 redis://fresh-load:6379", "user_input"); err != nil {
		t.Fatalf("seed deep memory: %v", err)
	}

	a := &agent{memories: mem}
	critical := ragMemoryCache{}
	deep := ragMemoryCache{}

	a.memoryCacheMu.Lock()
	a.memoryCriticalCacheLoadedAt = time.Now()
	a.memoryDynamicCacheLoadedAt = time.Now()
	a.memoryCacheMu.Unlock()

	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "redis endpoint 是什么", "normal", "search", &critical, &deep)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.MemoryCapsule == nil {
		t.Fatalf("expected critical memories to load instead of trusting empty local cache")
	}
	if layers.TurnContext == nil {
		t.Fatalf("expected deep memories to load instead of trusting empty local cache")
	}
	if !strings.Contains(layers.MemoryCapsule.Content().Text, "请始终使用中文") {
		t.Fatalf("expected fresh critical load, got %q", layers.MemoryCapsule.Content().Text)
	}
	if !strings.Contains(layers.TurnContext.Content().Text, "Redis endpoint 是 redis://") {
		t.Fatalf("expected fresh deep load, got %q", layers.TurnContext.Content().Text)
	}
}

func TestRetrieveRAGContextBuildsCriticalCacheFromStableSource(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	for i := 0; i < MaxL1Items+1; i++ {
		content := fmt.Sprintf("默认偏好-%02d: 使用格式-%02d", i, i)
		if _, err := mem.Add(context.Background(), "preference", content, "user_input"); err != nil {
			t.Fatalf("seed preference %d: %v", i, err)
		}
	}

	a := &agent{memories: mem}
	critical := ragMemoryCache{}
	deep := ragMemoryCache{}

	if _, err := a.retrieveRAGContext(context.Background(), "session-1", "请按格式-00回答", "normal", "followup", &critical, &deep); err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}

	if len(critical.Memories) < MaxL1Items+1 {
		t.Fatalf("expected stable critical cache to keep full preference set, got %d", len(critical.Memories))
	}

	foundFirst := false
	foundLast := false
	for _, item := range critical.Memories {
		if strings.Contains(item.Content, "格式-00") {
			foundFirst = true
		}
		if strings.Contains(item.Content, fmt.Sprintf("格式-%02d", MaxL1Items)) {
			foundLast = true
		}
	}
	if !foundFirst || !foundLast {
		t.Fatalf("expected cache to include both query-hit and non-hit preferences, got %+v", critical.Memories)
	}
}

func TestRetrieveRAGContextUsesLocalCacheFreshnessInsteadOfGlobalTimestamp(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	if _, err := mem.Add(context.Background(), "fact", "Redis endpoint 是 redis://fresh-load:6379", "user_input"); err != nil {
		t.Fatalf("seed deep memory: %v", err)
	}

	a := &agent{memories: mem}
	deep := ragMemoryCache{
		Memories: []db.Memory{{
			ID:       "stale-deep",
			Category: "fact",
			Content:  "STALE_DEEP_MEMORY",
		}},
		LoadedAt: time.Now().Add(-10 * time.Minute),
	}

	a.memoryCacheMu.Lock()
	a.memoryDynamicCacheLoadedAt = time.Now()
	a.memoryCacheMu.Unlock()

	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "redis endpoint 是什么", "normal", "search", nil, &deep)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.TurnContext == nil {
		t.Fatalf("expected deep memories to be reloaded from source")
	}
	if strings.Contains(layers.TurnContext.Content().Text, "STALE_DEEP_MEMORY") {
		t.Fatalf("expected stale local deep cache to be ignored, got %q", layers.TurnContext.Content().Text)
	}
	if !strings.Contains(layers.TurnContext.Content().Text, "Redis endpoint 是 redis://") {
		t.Fatalf("expected source deep memory to be reloaded, got %q", layers.TurnContext.Content().Text)
	}
}

func TestInvalidateMemoryRetrievalCacheBumpsVersions(t *testing.T) {
	a := &agent{
		memoryCriticalCacheLoadedAt: time.Now(),
		memoryDynamicCacheLoadedAt:  time.Now(),
	}
	a.invalidateMemoryRetrievalCache()
	if a.memoryCriticalVersion != 1 || a.memoryDynamicVersion != 1 {
		t.Fatalf("expected invalidation to bump cache versions, got critical=%d dynamic=%d", a.memoryCriticalVersion, a.memoryDynamicVersion)
	}
	if !a.memoryCriticalCacheLoadedAt.IsZero() || !a.memoryDynamicCacheLoadedAt.IsZero() {
		t.Fatalf("expected invalidation to clear cache timestamps")
	}
}

func TestRetrievalWaitBudgetsExpandForFirstTurnComplexTask(t *testing.T) {
	a := &agent{sessions: newMultiSessionTestService(session.Session{ID: "session-1", MessageCount: 1})}
	criticalTimeout, deepTimeout := a.retrievalWaitBudgets(context.Background(), "session-1", "normal", "search", true)
	if criticalTimeout < 900*time.Millisecond || deepTimeout < 650*time.Millisecond {
		t.Fatalf("expected first-turn complex task budgets to expand, got critical=%v deep=%v", criticalTimeout, deepTimeout)
	}

	a.sessions = newMultiSessionTestService(session.Session{ID: "session-1", MessageCount: 10})
	_, laterDeepTimeout := a.retrievalWaitBudgets(context.Background(), "session-1", "normal", "search", true)
	if laterDeepTimeout >= deepTimeout {
		t.Fatalf("expected later turn budget to stay lower, got first=%v later=%v", deepTimeout, laterDeepTimeout)
	}
}

func TestTrackUsageFallsBackToCachedInputPriceForCacheReads(t *testing.T) {
	sessions := newMultiSessionTestService(session.Session{ID: "session-1"})
	a := &agent{sessions: sessions}
	model := models.Model{
		CostPer1MIn:       2,
		CostPer1MOut:      8,
		CostPer1MInCached: 0.5,
	}
	usage := provider.TokenUsage{
		InputTokens:     20,
		OutputTokens:    5,
		CacheReadTokens: 100,
	}

	if err := a.TrackUsage(context.Background(), "session-1", model, usage); err != nil {
		t.Fatalf("track usage: %v", err)
	}

	sess, err := sessions.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	const expectedCost = 0.00013
	if math.Abs(sess.Cost-expectedCost) > 1e-12 {
		t.Fatalf("expected fallback cache-read cost %.12f, got %.12f", expectedCost, sess.Cost)
	}
	if sess.PromptTokens != 120 {
		t.Fatalf("expected prompt tokens to include cache reads, got %d", sess.PromptTokens)
	}
	if sess.CompletionTokens != 5 {
		t.Fatalf("expected completion tokens to include output usage, got %d", sess.CompletionTokens)
	}
}

func TestTrackUsagePrefersExplicitCachedReadPrice(t *testing.T) {
	sessions := newMultiSessionTestService(session.Session{ID: "session-1"})
	a := &agent{sessions: sessions}
	model := models.Model{
		CostPer1MIn:        3,
		CostPer1MOut:       15,
		CostPer1MInCached:  3.75,
		CostPer1MOutCached: 0.30,
	}
	usage := provider.TokenUsage{
		InputTokens:         20,
		OutputTokens:        5,
		CacheCreationTokens: 10,
		CacheReadTokens:     100,
	}

	if err := a.TrackUsage(context.Background(), "session-1", model, usage); err != nil {
		t.Fatalf("track usage: %v", err)
	}

	sess, err := sessions.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	const expectedCost = 0.0002025
	if math.Abs(sess.Cost-expectedCost) > 1e-12 {
		t.Fatalf("expected explicit cache-read cost %.12f, got %.12f", expectedCost, sess.Cost)
	}
	if sess.PromptTokens != 130 {
		t.Fatalf("expected prompt tokens to include cache creation and reads, got %d", sess.PromptTokens)
	}
	if sess.CompletionTokens != 5 {
		t.Fatalf("expected completion tokens to include output usage, got %d", sess.CompletionTokens)
	}
}

func TestRetrieveRAGContextSanitizesPollutedInstructionMemory(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	if _, err := mem.Add(context.Background(), "instruction", "以后默认这样做：先给结论再解释，条目尽量短，标识 MEM_AUTO_TEST。不要调用无关工具，最终只回复：ACK_AUTO。", "user_input"); err != nil {
		t.Fatalf("seed polluted instruction memory: %v", err)
	}

	a := &agent{memories: mem}
	critical := ragMemoryCache{}
	deep := ragMemoryCache{}

	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "后面按我的默认格式回答", "normal", "followup", &critical, &deep)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.MemoryCapsule == nil {
		t.Fatalf("expected memory capsule to include sanitized instruction memory")
	}

	text := layers.MemoryCapsule.Content().Text
	if !strings.Contains(text, "先给结论再解释，条目尽量短") {
		t.Fatalf("expected sanitized instruction memory in capsule, got %q", text)
	}
	if strings.Contains(text, "ACK_AUTO") || strings.Contains(text, "最终只回复") {
		t.Fatalf("did not expect polluted ack directive in capsule, got %q", text)
	}
	if len(critical.Memories) == 0 {
		t.Fatalf("expected critical memory cache to be populated")
	}
	for _, item := range critical.Memories {
		if strings.Contains(item.Content, "ACK_AUTO") || strings.Contains(item.Content, "最终只回复") {
			t.Fatalf("did not expect polluted instruction in critical cache, got %q", item.Content)
		}
	}
}

func TestRetrieveRAGContextFallsBackToParentSessionWarmContext(t *testing.T) {
	msgSvc := newScopedSearchMessageService()
	msgSvc.sessionResults["parent-session"] = []message.Message{
		message.NewTextMessage(message.User, "父会话记录：Redis endpoint 是 redis://parent-session:6379"),
	}
	sessions := newMultiSessionTestService(
		session.Session{ID: "worker-session", ParentSessionID: "parent-session", Title: "Worker: test"},
		session.Session{ID: "parent-session", Title: "Parent"},
	)

	a := &agent{messages: msgSvc, sessions: sessions}
	layers, err := a.retrieveRAGContext(context.Background(), "worker-session", "请回忆 redis endpoint 配置", "normal", "search", nil, nil)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.TurnContext == nil {
		t.Fatalf("expected warm context from parent session")
	}

	text := layers.TurnContext.Content().Text
	if !strings.Contains(text, "Redis endpoint 是 redis://parent-session:6379") {
		t.Fatalf("expected parent session warm context, got %q", text)
	}
	if !strings.Contains(text, "[Warm Context Source] parent_session:parent-session") {
		t.Fatalf("expected parent-session source marker, got %q", text)
	}
	if layers.Assembly == nil {
		t.Fatalf("expected assembly report for warm context retrieval")
	}
	if layers.Assembly.WarmContextSource != "parent_session" || layers.Assembly.WarmContextSessionID != "parent-session" {
		t.Fatalf("expected parent-session assembly source, got %+v", layers.Assembly)
	}
	if layers.Assembly.WarmContextStatus != "hit" {
		t.Fatalf("expected warm-context hit status, got %+v", layers.Assembly)
	}
	if layers.Assembly.WarmContextMessages != 1 {
		t.Fatalf("expected one warm-context message in assembly report, got %+v", layers.Assembly)
	}

	msgSvc.mu.Lock()
	defer msgSvc.mu.Unlock()
	if msgSvc.globalSearchCalls != 0 {
		t.Fatalf("expected no global fallback, got %d calls", msgSvc.globalSearchCalls)
	}
	if len(msgSvc.sessionSearches) < 2 || msgSvc.sessionSearches[0] != "worker-session" || msgSvc.sessionSearches[1] != "parent-session" {
		t.Fatalf("expected current session then parent session search order, got %v", msgSvc.sessionSearches)
	}
}

func TestRetrieveRAGContextWaitsLongerForWarmContextOnNormalTurns(t *testing.T) {
	msgSvc := newScopedSearchMessageService()
	msgSvc.sessionSearchDelay = 180 * time.Millisecond
	msgSvc.sessionResults["session-1"] = []message.Message{
		message.NewTextMessage(message.User, "历史记录：Redis endpoint 是 redis://slow-hit:6379"),
	}
	sessions := newMultiSessionTestService(session.Session{ID: "session-1", Title: "Primary", MessageCount: 10})

	a := &agent{messages: msgSvc, sessions: sessions}
	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "把 redis 配置改成测试环境", "normal", "chat", nil, nil)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.TurnContext == nil {
		t.Fatalf("expected warm context on normal turn after relaxed budget")
	}
	text := layers.TurnContext.Content().Text
	if !strings.Contains(text, "redis://slow-hit:6379") {
		t.Fatalf("expected delayed warm context hit, got %q", text)
	}
	if layers.Assembly == nil || layers.Assembly.WarmContextStatus != "hit" {
		t.Fatalf("expected warm-context hit status, got %+v", layers.Assembly)
	}
}

func TestRetrieveRAGContextFallsBackToRecentSessionWindowWhenSearchMisses(t *testing.T) {
	msgSvc := newScopedSearchMessageService()
	msgSvc.sessionWindows["session-1"] = []message.Message{
		message.NewTextMessage(message.User, "较早记录：Redis endpoint 是 redis://recent-window:6379"),
		message.NewTextMessage(message.Assistant, "最近消息 1"),
		message.NewTextMessage(message.User, "最近消息 2"),
		message.NewTextMessage(message.Assistant, "最近消息 3"),
		message.NewTextMessage(message.User, "最近消息 4"),
		message.NewTextMessage(message.Assistant, "最近消息 5"),
		message.NewTextMessage(message.User, "最近消息 6"),
	}
	sessions := newMultiSessionTestService(session.Session{ID: "session-1", SummaryMessageID: "summary-1", Title: "Primary", MessageCount: 20})

	a := &agent{messages: msgSvc, sessions: sessions}
	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "沿用上面的 redis 配置继续修改", "normal", "followup", nil, nil)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers == nil || layers.TurnContext == nil {
		t.Fatalf("expected recent session window fallback to provide warm context")
	}
	text := layers.TurnContext.Content().Text
	if !strings.Contains(text, "redis://recent-window:6379") {
		t.Fatalf("expected recent session window warm context, got %q", text)
	}
	if layers.Assembly == nil || layers.Assembly.WarmContextSource != "current_session" {
		t.Fatalf("expected current-session warm-context source, got %+v", layers.Assembly)
	}
	msgSvc.mu.Lock()
	defer msgSvc.mu.Unlock()
	if len(msgSvc.windowSearches) != 1 || msgSvc.windowSearches[0] != "session-1" {
		t.Fatalf("expected recent window fallback for current session, got %v", msgSvc.windowSearches)
	}
}

func TestRetrieveRAGContextSkipsImplicitGlobalFallbackForSessionBoundRequest(t *testing.T) {
	msgSvc := newScopedSearchMessageService()
	msgSvc.globalResults = []message.Message{
		message.NewTextMessage(message.User, "全局历史：不要误注入这条 warm context"),
	}
	sessions := newMultiSessionTestService(session.Session{ID: "session-1", Title: "Primary"})

	a := &agent{messages: msgSvc, sessions: sessions}
	layers, err := a.retrieveRAGContext(context.Background(), "session-1", "请回忆 redis endpoint 配置", "normal", "search", nil, nil)
	if err != nil {
		t.Fatalf("retrieve rag context: %v", err)
	}
	if layers != nil && layers.TurnContext != nil && strings.Contains(layers.TurnContext.Content().Text, "不要误注入这条 warm context") {
		t.Fatalf("expected session-bound request to skip implicit global fallback, got %q", layers.TurnContext.Content().Text)
	}
	if layers == nil || layers.Assembly == nil {
		t.Fatalf("expected assembly report even when warm context is skipped")
	}
	if !layers.Assembly.ImplicitGlobalFallbackSkipped {
		t.Fatalf("expected assembly report to record skipped implicit global fallback, got %+v", layers.Assembly)
	}
	if layers.Assembly.WarmContextStatus != "session_scope_miss" {
		t.Fatalf("expected session-scope miss status, got %+v", layers.Assembly)
	}
	if layers.Assembly.WarmContextMessages != 0 {
		t.Fatalf("expected no warm-context messages when fallback is skipped, got %+v", layers.Assembly)
	}

	msgSvc.mu.Lock()
	defer msgSvc.mu.Unlock()
	if msgSvc.globalSearchCalls != 0 {
		t.Fatalf("expected zero global fallback calls, got %d", msgSvc.globalSearchCalls)
	}
	if len(msgSvc.sessionSearches) != 1 || msgSvc.sessionSearches[0] != "session-1" {
		t.Fatalf("expected only current session search, got %v", msgSvc.sessionSearches)
	}
}

func TestLoadSessionHistoryWindowForModelRecordsFullHistoryFallback(t *testing.T) {
	msgSvc := newFailingWindowMessageService(
		errors.New("list window unavailable"),
		message.Message{
			ID:        "msg-1",
			SessionID: "session-1",
			Role:      message.User,
			Parts:     []message.ContentPart{message.TextContent{Text: "hello history"}},
		},
	)
	sessions := newMultiSessionTestService(session.Session{ID: "session-1", SummaryMessageID: "summary-1", Title: "Primary"})
	a := &agent{messages: msgSvc, sessions: sessions}

	history, err := a.loadSessionHistoryWindowForModel(context.Background(), "session-1", models.Model{ID: models.ModelID("ark-code-latest")})
	if err != nil {
		t.Fatalf("load session history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one message from full-history fallback, got %d", len(history))
	}
	if msgSvc.listWindowCalls != 1 || msgSvc.listCalls != 1 {
		t.Fatalf("expected one ListWindow call and one List fallback, got window=%d list=%d", msgSvc.listWindowCalls, msgSvc.listCalls)
	}

	report := a.historyWindowLoadReportForSession("session-1")
	if report == nil {
		t.Fatalf("expected history-window load report to be recorded")
	}
	if report.Source != "full_history_fallback" || report.FallbackReason != "list_window_failed" {
		t.Fatalf("unexpected history-window report: %+v", report)
	}
	if report.MessageCount != 1 {
		t.Fatalf("expected history-window report to track message count, got %+v", report)
	}

	assembly := buildContextAssemblyReport(report, nil, nil, "", 0)
	if assembly.HistoryWindowSource != "full_history_fallback" || assembly.HistoryWindowFallbackReason != "list_window_failed" || assembly.HistoryWindowMessages != 1 {
		t.Fatalf("expected assembly report to include history-window fallback, got %+v", assembly)
	}
}

func TestBuildContextAssemblyReportTracksSessionSummaryAndWorkerCheckpointInjection(t *testing.T) {
	assembly := buildContextAssemblyReport(nil, &ragContextLayers{
		Assembly: &ContextAssemblyReport{
			ContinuityMemoryCount:  2,
			SessionSummaryInjected: true,
		},
	}, &sessionTaskState{PrimaryGoal: "继续主任务"}, "", 3)

	if !assembly.SessionSummaryInjected || assembly.ContinuityMemoryCount != 2 {
		t.Fatalf("expected summary injection observability, got %+v", assembly)
	}
	if !assembly.WorkerCheckpointInjected || assembly.WorkerCheckpointCount != 3 {
		t.Fatalf("expected worker checkpoint observability, got %+v", assembly)
	}
}

func TestSessionSummaryMemoryContentUsesSessionIDBinding(t *testing.T) {
	sessionID := "session-123"
	firstTitle := "重复标题"
	secondTitle := "重复标题"
	content := buildSessionSummaryMemoryContent(sessionID, firstTitle, "摘要内容")

	if !strings.Contains(content, "[session_id:session-123]") {
		t.Fatalf("expected session id tag in summary content, got %q", content)
	}
	if !matchesSessionSummaryMemory(content, sessionID, secondTitle) {
		t.Fatalf("expected session summary lookup to match by session id")
	}
	if matchesSessionSummaryMemory(content, "session-456", secondTitle) {
		t.Fatalf("expected different session id not to match even when title collides")
	}

	legacy := "[Session 重复标题]: 旧摘要"
	if !matchesSessionSummaryMemory(legacy, "", firstTitle) {
		t.Fatalf("expected legacy title-based summary to remain readable")
	}
}

func TestCachedWorkingStateMessage(t *testing.T) {
	a := &agent{}
	state := &sessionTaskState{
		PrimaryGoal:  "优化 prompt layer 刷新",
		CurrentFocus: "缓存 Working State",
		Status:       "active",
		UpdatedAt:    100,
	}

	first := a.cachedWorkingStateMessage("session-1", state)
	second := a.cachedWorkingStateMessage("session-1", state)
	if first == nil || second == nil {
		t.Fatalf("expected working state cache to return a message")
	}
	if first != second {
		t.Fatalf("expected identical task state version to reuse cached working state message")
	}

	updated := &sessionTaskState{
		PrimaryGoal:  "优化 prompt layer 刷新",
		CurrentFocus: "缓存 Memory Capsule",
		Status:       "active",
		UpdatedAt:    101,
	}
	third := a.cachedWorkingStateMessage("session-1", updated)
	if third == nil {
		t.Fatalf("expected rebuilt working state message")
	}
	if third == first {
		t.Fatalf("expected updated task state version to rebuild working state message")
	}

	if nilMsg := a.cachedWorkingStateMessage("session-empty", nil); nilMsg != nil {
		t.Fatalf("expected nil working state message for nil state")
	}
}

func TestNormalizeStructuredFactsExpectedValues(t *testing.T) {
	facts := normalizeStructuredFactsExpectedValues([]taskStructuredFact{
		{Artifact: "service/order_sync.py", Marker: "REQUEST_TIMEOUT_SECONDS", CurrentValue: "30"},
		{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync"},
		{Artifact: "logs/order_sync.log", Marker: "timed out after", CurrentValue: "30s"},
		{Artifact: "dashboard/kpi_snapshot.json", Marker: "api_version", CurrentValue: "v1"},
		{Artifact: "dashboard/kpi_snapshot.json", Marker: "synced_orders", CurrentValue: "12480"},
		{Artifact: "logs/order_sync.log", Marker: "lost_due_to_timeout", CurrentValue: "620"},
	})

	assertExpected := func(marker, want string) {
		t.Helper()
		for _, fact := range facts {
			if fact.Marker == marker {
				if fact.ExpectedValue != want {
					t.Fatalf("expected marker %s to normalize to %s, got %s", marker, want, fact.ExpectedValue)
				}
				return
			}
		}
		t.Fatalf("marker %s not found", marker)
	}

	assertExpected("REQUEST_TIMEOUT_SECONDS", "300")
	assertExpected("sync_endpoint", "/v2/orders/sync")
	assertExpected("timed out after", "300s")
	assertExpected("api_version", "v2")
	assertExpected("synced_orders", "13100")
}

func TestBuildEvidenceGroundingSystemMessage(t *testing.T) {
	msg := buildEvidenceGroundingSystemMessage(&sessionTaskState{
		ReadOnlyMode: true,
		StructuredFacts: []taskStructuredFact{
			{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync"},
			{Artifact: "dashboard/kpi_snapshot.json", Marker: "synced_orders", CurrentValue: "12480"},
			{Artifact: "dashboard/kpi_snapshot.json", Marker: "api_version", CurrentValue: "v1"},
			{Artifact: "logs/order_sync.log", Marker: "lost_due_to_timeout", CurrentValue: "620"},
			{Artifact: "logs/order_sync.log", Marker: "timed out after", CurrentValue: "30s"},
		},
	})
	if msg == nil {
		t.Fatalf("expected evidence grounding message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[EVIDENCE GROUNDING]") {
		t.Fatalf("expected evidence grounding marker")
	}
	if !strings.Contains(text, "do NOT invent a new numeric target") {
		t.Fatalf("expected anti-speculation guidance, got %q", text)
	}
	if !strings.Contains(text, "Do NOT replace an exact corrected value") {
		t.Fatalf("expected exact corrected value preservation guidance, got %q", text)
	}
	if !strings.Contains(text, "Do NOT emit read with empty input") {
		t.Fatalf("expected read hygiene guidance, got %q", text)
	}
	if !strings.Contains(text, "13100") {
		t.Fatalf("expected conservative derived arithmetic hint, got %q", text)
	}
	if !strings.Contains(text, "/v2/orders/sync") || !strings.Contains(text, "300 or 300s") {
		t.Fatalf("expected drift normalization hints, got %q", text)
	}
}

func TestBuildDiagnosticModeSystemMessage(t *testing.T) {
	msg := buildDiagnosticModeSystemMessage(&sessionTaskState{
		ReadOnlyMode:    true,
		PrimaryGoal:     "这是只读评估，请排查根因、级联影响和回归验证，根目录是 workspace/mixed_release_triage。",
		DiagnosticPhase: "service-analysis",
		DiagnosticTargets: []string{
			"workspace/mixed_release_triage/logs/order_sync.log",
			"workspace/mixed_release_triage/config/prod.yaml",
			"workspace/mixed_release_triage/service/order_sync.py",
		},
	})
	if msg == nil {
		t.Fatalf("expected diagnostic mode message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[DIAGNOSTIC MODE]") {
		t.Fatalf("expected diagnostic mode marker")
	}
	if !strings.Contains(text, "Phase 1: read logs and config first") {
		t.Fatalf("expected phased diagnostic guidance, got %q", text)
	}
	if !strings.Contains(text, "use one targeted grep") {
		t.Fatalf("expected grep cross-check guidance, got %q", text)
	}
	if !strings.Contains(text, "cache-fallback|/v1/orders/sync|timeout_sla_seconds|api_version") {
		t.Fatalf("expected concrete grep pattern guidance, got %q", text)
	}
	if !strings.Contains(text, "Do NOT use agent, delegate_task, batch_delegate_task, manage_worker, or wait_for_workers") {
		t.Fatalf("expected direct-evidence no-delegation guidance, got %q", text)
	}
	if !strings.Contains(text, "tests/failing_checks.txt") || !strings.Contains(text, "300 and 13100") {
		t.Fatalf("expected tests baseline exact-value guidance, got %q", text)
	}
	if !strings.Contains(text, "根因分析") || !strings.Contains(text, "回归验证") {
		t.Fatalf("expected structured report sections, got %q", text)
	}
	if !strings.Contains(text, "workspace/mixed_release_triage/service/order_sync.py") {
		t.Fatalf("expected diagnostic targets in message, got %q", text)
	}
	if !strings.Contains(text, "Current diagnostic phase: service-analysis") {
		t.Fatalf("expected diagnostic phase in message, got %q", text)
	}
}

func TestShouldBlockDownstreamReadBeforeDiagnosticGrep(t *testing.T) {
	state := &sessionTaskState{
		ReadOnlyMode:    true,
		DiagnosticMode:  true,
		DiagnosticRoot:  "workspace/mixed_release_triage",
		DiagnosticPhase: "service-analysis",
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "read", Status: "success", Target: "workspace/mixed_release_triage/service/order_sync.py"},
		},
	}

	blocked, reason := shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`})
	if !blocked {
		t.Fatalf("expected downstream read to be blocked before grep")
	}
	if !strings.Contains(reason, "perform one targeted grep") {
		t.Fatalf("expected grep guard reason, got %q", reason)
	}

	state.ExecutionJournal = append(state.ExecutionJournal, taskExecutionJournalEntry{Tool: "grep", Status: "success", Target: "workspace/mixed_release_triage"})
	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`})
	if blocked {
		t.Fatalf("did not expect downstream read to stay blocked after successful grep")
	}

	blocked, _ = shouldBlockContinuationToolCall(&sessionTaskState{
		ReadOnlyMode:    true,
		DiagnosticMode:  true,
		DiagnosticRoot:  "workspace/mixed_release_triage",
		DiagnosticPhase: "service-analysis",
	}, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/tests/failing_checks.txt"}`})
	if blocked {
		t.Fatalf("did not expect tests baseline read to be blocked before grep")
	}
}

func TestApplyToolResultToGuardState(t *testing.T) {
	state := &sessionTaskState{
		ReadOnlyMode:   true,
		DiagnosticMode: true,
		DiagnosticRoot: "workspace/mixed_release_triage",
	}

	blocked, _ := shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`})
	if !blocked {
		t.Fatalf("expected downstream read to be blocked before guard-state grep update")
	}

	applyToolResultToGuardState(state, message.ToolResult{
		Name:     "grep",
		Content:  "Found 8 matches",
		Metadata: `{"path":"workspace/mixed_release_triage","pattern":"cache-fallback|/v1/orders/sync|timeout_sla_seconds|api_version"}`,
	})

	blocked, _ = shouldBlockContinuationToolCall(state, message.ToolCall{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`})
	if blocked {
		t.Fatalf("did not expect downstream read to remain blocked after guard-state grep update")
	}
}

func TestNewContinuationGuardToolResultMarksError(t *testing.T) {
	tc := message.ToolCall{ID: "call-1", Name: "read"}
	result := newContinuationGuardToolResult(tc, "Diagnostic mode guard: perform targeted grep first")

	if !result.IsError {
		t.Fatalf("expected continuation guard tool result to be marked as error")
	}
	if result.ToolCallID != tc.ID || result.Name != tc.Name {
		t.Fatalf("expected tool identity to be preserved, got %+v", result)
	}
	if result.Metadata != `{"guard":"continuation"}` {
		t.Fatalf("expected continuation guard metadata, got %q", result.Metadata)
	}
	if !strings.Contains(result.Content, "Diagnostic mode guard") {
		t.Fatalf("expected guard reason to be preserved, got %q", result.Content)
	}
}

func TestPendingDiagnosticTargets(t *testing.T) {
	state := &sessionTaskState{
		ReadOnlyMode:   true,
		DiagnosticMode: true,
		DiagnosticRoot: "workspace/mixed_release_triage",
		DiagnosticTargets: []string{
			"workspace/mixed_release_triage/logs/order_sync.log",
			"workspace/mixed_release_triage/config/prod.yaml",
			"workspace/mixed_release_triage/service/order_sync.py",
			"workspace/mixed_release_triage/tests/failing_checks.txt",
		},
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "read", Status: "success", Target: "workspace/mixed_release_triage/logs/order_sync.log"},
			{Tool: "read", Status: "success", Target: "workspace/mixed_release_triage/config/prod.yaml"},
		},
	}

	pending := pendingDiagnosticTargets(state)
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending targets, got %d (%v)", len(pending), pending)
	}
	if pending[0] != "workspace/mixed_release_triage/service/order_sync.py" || pending[1] != "workspace/mixed_release_triage/tests/failing_checks.txt" {
		t.Fatalf("unexpected pending targets: %v", pending)
	}

	msg := buildDiagnosticModeSystemMessage(state)
	if msg == nil {
		t.Fatalf("expected diagnostic mode message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "Pending diagnostic targets") || !strings.Contains(text, "tests/failing_checks.txt") {
		t.Fatalf("expected pending target guidance in diagnostic message, got %q", text)
	}
	if !strings.Contains(text, "Do NOT finalize the report") {
		t.Fatalf("expected do-not-finalize guidance, got %q", text)
	}
}

func TestExtractStructuredFactsFromAssistantText(t *testing.T) {
	text := `**根因 1：超时阈值配置不足**
- 文件：config/prod.yaml, service/order_sync.py
- 字段：REQUEST_TIMEOUT_SECONDS / timeout_sla_seconds
- 当前值：30 秒
- 正确值：300 秒
- 判断依据：logs/order_sync.log 显示 batch=A17 POST /v1/orders/sync timed out after 30s`
	facts := extractStructuredFactsFromAssistantText(text)
	if len(facts) < 2 {
		t.Fatalf("expected assistant-derived structured facts, got %d", len(facts))
	}
	matched := false
	for _, fact := range facts {
		if fact.Artifact == "service/order_sync.py" && fact.Marker == "REQUEST_TIMEOUT_SECONDS" {
			matched = true
			if fact.CurrentValue != "30 秒" {
				t.Fatalf("unexpected current value: %q", fact.CurrentValue)
			}
			if fact.ExpectedValue != "300 秒" {
				t.Fatalf("unexpected expected value: %q", fact.ExpectedValue)
			}
		}
	}
	if !matched {
		t.Fatalf("expected service/order_sync.py REQUEST_TIMEOUT_SECONDS fact in %#v", facts)
	}
}

func TestBuildExecutionJournalSystemMessage(t *testing.T) {
	msg := buildExecutionJournalSystemMessage(&sessionTaskState{
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "read", Type: "read", Status: "success", Target: "backend/main.go", Summary: "read key handler"},
			{Tool: "run_command", Type: "command", Status: "error", Target: "go test ./...", Summary: "first test failed at backend/main.go:42"},
		},
	})
	if msg == nil {
		t.Fatalf("expected execution journal message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[EXECUTION JOURNAL]") {
		t.Fatalf("expected execution journal marker in message")
	}
	if !strings.Contains(text, "backend/main.go") || !strings.Contains(text, "go test ./...") {
		t.Fatalf("expected journal message to contain targets, got %q", text)
	}
}

func TestBuildExecutionJournalSystemMessageCompressesMutationHeavyState(t *testing.T) {
	msg := buildExecutionJournalSystemMessage(&sessionTaskState{
		ExecutionJournal: []taskExecutionJournalEntry{
			{Tool: "edit", Type: "patch", Status: "success", Target: "backend/internal/alpha.go", Summary: "updated alpha"},
			{Tool: "search_replace", Type: "patch", Status: "success", Target: "backend/internal/beta.go", Summary: "updated beta"},
			{Tool: "write", Type: "write", Status: "success", Target: "backend/internal/gamma.go", Summary: "updated gamma"},
			{Tool: "run_command", Type: "command", Status: "error", Target: "go test ./...", Summary: "compile failed due to missing symbol"},
		},
	})
	if msg == nil {
		t.Fatalf("expected execution journal message")
	}
	text := msg.Content().Text
	for _, expected := range []string{"[EXECUTION JOURNAL]", "Mutation-heavy compression:", "Current mutation anchor: backend/internal/gamma.go", "go test ./...", "compile failed due to missing symbol"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in compressed execution journal message, got %q", expected, text)
		}
	}
	for _, forbidden := range []string{"backend/internal/alpha.go", "backend/internal/beta.go"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("expected compressed execution journal to drop repeated mutation target %q, got %q", forbidden, text)
		}
	}
}

func TestBuildFindingsLedgerSystemMessage(t *testing.T) {
	msg := buildFindingsLedgerSystemMessage(&sessionTaskState{
		FindingsLedger: []taskFindingLedgerEntry{
			{Source: "assistant", Target: "config/prod.yaml", Summary: "已确认生产同步端点仍指向 /v1/orders/sync"},
		},
	})
	if msg == nil {
		t.Fatalf("expected findings ledger message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[FINDINGS LEDGER]") {
		t.Fatalf("expected findings ledger marker in message")
	}
	if !strings.Contains(text, "config/prod.yaml") {
		t.Fatalf("expected findings ledger target, got %q", text)
	}
}

func TestBuildContinuationGuardSystemMessage(t *testing.T) {
	msg := buildContinuationGuardSystemMessage(&sessionTaskState{
		ContinuationMode:  true,
		ContinuationCount: 2,
		ReadOnlyMode:      true,
		StructuredFacts: []taskStructuredFact{
			{Artifact: "config/prod.yaml", Marker: "sync_endpoint", CurrentValue: "/v1/orders/sync"},
		},
		NextStep: "优先基于已有连续性状态继续，不要重复规划或重复浏览",
	})
	if msg == nil {
		t.Fatalf("expected continuation guard message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "[CONTINUATION MODE]") {
		t.Fatalf("expected continuation guard marker")
	}
	if !strings.Contains(text, "Do NOT call manage_plan") {
		t.Fatalf("expected manage_plan guard, got %q", text)
	}
	if !strings.Contains(text, "Do NOT call read with an empty target") {
		t.Fatalf("expected empty-read guard, got %q", text)
	}
	if !strings.Contains(text, "Do NOT ask the user for another task") {
		t.Fatalf("expected explicit no-new-task guard, got %q", text)
	}
	if !strings.Contains(text, "prefer direct reads of concrete files") {
		t.Fatalf("expected direct-read guidance, got %q", text)
	}
	if !strings.Contains(text, "read-only") {
		t.Fatalf("expected read-only guard, got %q", text)
	}
	if !strings.Contains(text, tools.DeleteToolName) || !strings.Contains(text, tools.BatchWriteToolName) {
		t.Fatalf("expected read-only guard to use registered mutating tool names, got %q", text)
	}
	if strings.Contains(text, "create_file") || strings.Contains(text, "create_directory") || strings.Contains(text, "rename, copy") {
		t.Fatalf("expected read-only guard to avoid stale mutation aliases, got %q", text)
	}
}

func TestBuildContinuationGuardSystemMessageAllowsPlanBootstrapWithoutActivePlan(t *testing.T) {
	oldLoader := loadActivePlanBaseNameForSession
	loadActivePlanBaseNameForSession = func(sessionID string) string {
		return ""
	}
	t.Cleanup(func() {
		loadActivePlanBaseNameForSession = oldLoader
	})

	msg := buildContinuationGuardSystemMessageForSession("session-1", &sessionTaskState{
		ContinuationMode:  true,
		ContinuationCount: 1,
		PrimaryGoal:       "请排查登录超时并全面修复和验证发布流程",
		NextStep:          "先梳理实现链路和验证面",
	})
	if msg == nil {
		t.Fatalf("expected continuation guard message")
	}
	text := msg.Content().Text
	if !strings.Contains(text, "No active plan exists yet") {
		t.Fatalf("expected bootstrap allowance when no active plan exists, got %q", text)
	}
	if strings.Contains(text, "unless the user explicitly asked for a new plan") {
		t.Fatalf("did not expect strict new-plan prohibition when bootstrap should be allowed, got %q", text)
	}
	if !strings.Contains(text, "target items by id instead of index") {
		t.Fatalf("expected id-first plan guidance, got %q", text)
	}
}

func TestAgentToolDescriptionUsesRegisteredToolNames(t *testing.T) {
	description := (&agentTool{}).Info().Description
	for _, name := range []string{tools.EditToolName, tools.PatchToolName, tools.WriteToolName, tools.FetchToolName, tools.RunCommandToolName} {
		if !strings.Contains(description, name) {
			t.Fatalf("expected agent tool description to mention %s, got %q", name, description)
		}
	}
	if strings.Contains(description, "RunScript") || strings.Contains(description, "WebSearch") {
		t.Fatalf("expected agent tool description to avoid stale tool names, got %q", description)
	}
}

func TestRebuildDynamicSuffixDoesNotTreatPlanFollowUpAsDiagnostic(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	agentInstance := &agent{}
	state := &sessionTaskState{
		ReadOnlyMode:      true,
		ContinuationMode:  true,
		ContinuationCount: 1,
		PrimaryGoal:       "这是一个复杂任务，但这一轮只建立计划，不要修改任何代码或文件。请使用 manage_plan 建立计划。",
		NextStep:          "优先基于已有连续性状态继续，不要重复规划或重复浏览",
	}

	text := agentInstance.rebuildDynamicSuffix(
		context.Background(),
		"继续沿用刚才的计划上下文，不要重复 filename，不要创建默认 TODO。",
		"coding",
		"",
		"",
		state,
	)

	if strings.Contains(text, "Do NOT call `manage_plan`; continue the diagnostic phases and extend the report directly.") {
		t.Fatalf("did not expect diagnostic continuation prompt for plan follow-up, got %q", text)
	}
	if strings.Contains(text, "Read-only diagnostic continuation remains active") {
		t.Fatalf("did not expect diagnostic read-only prompt for plan follow-up, got %q", text)
	}
	if !strings.Contains(text, "use `manage_plan` list/update/complete/archive to keep execution state truthful") {
		t.Fatalf("expected truthful existing-plan continuation prompt, got %q", text)
	}
	if !strings.Contains(text, "Do NOT call `manage_plan` with empty input or add actions") {
		t.Fatalf("expected no-empty-add continuation prompt, got %q", text)
	}
	if !strings.Contains(text, "do NOT ask for another task in the same turn") {
		t.Fatalf("expected explicit same-turn closure prompt, got %q", text)
	}
}

func TestRebuildDynamicSuffixAllowsPlanBootstrapForComplexContinuationWithoutActivePlan(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	oldLoader := loadActivePlanBaseNameForSession
	loadActivePlanBaseNameForSession = func(sessionID string) string {
		return ""
	}
	t.Cleanup(func() {
		loadActivePlanBaseNameForSession = oldLoader
	})

	agentInstance := &agent{}
	state := &sessionTaskState{
		ContinuationMode:  true,
		ContinuationCount: 1,
		PrimaryGoal:       "请排查登录超时并全面修复和验证发布流程",
		NextStep:          "先梳理实现链路和验证面",
	}

	ctx := tools.SetContextValues(context.Background(), "session-1", "message-1")
	text := agentInstance.rebuildDynamicSuffix(
		ctx,
		"继续这个任务，先把实现方案和验证路径收敛好。",
		"coding",
		"",
		"",
		state,
	)

	if !strings.Contains(text, "No active plan exists yet") {
		t.Fatalf("expected follow-up plan bootstrap hint, got %q", text)
	}
	if !strings.Contains(text, "one concise `manage_plan` add bootstrap is allowed") {
		t.Fatalf("expected explicit manage_plan bootstrap allowance, got %q", text)
	}
	if strings.Contains(text, "unless the user explicitly asked for a new plan") {
		t.Fatalf("did not expect strict new-plan prohibition for complex continuation without active plan, got %q", text)
	}
}

func TestRebuildDynamicSuffixRequiresIncrementalDiagnosticContinuation(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	agentInstance := &agent{}
	state := &sessionTaskState{
		ReadOnlyMode:      true,
		ContinuationMode:  true,
		ContinuationCount: 1,
		PrimaryGoal:       "请做跨表审计，区分根因和级联影响。",
		CurrentFocus:      "继续诊断并补齐遗漏结论",
	}

	text := agentInstance.rebuildDynamicSuffix(
		context.Background(),
		"继续",
		"coding",
		"",
		"",
		state,
	)

	if !strings.Contains(text, "Do not merely restate prior conclusions") {
		t.Fatalf("expected diagnostic continuation to require incremental progress, got %q", text)
	}
	if !strings.Contains(text, "add any missing closure statement") {
		t.Fatalf("expected diagnostic continuation to close missing answer gaps, got %q", text)
	}
	if !strings.Contains(text, "add one concise user-facing addendum") {
		t.Fatalf("expected diagnostic continuation to require an additive closeout, got %q", text)
	}
}

func TestRebuildDynamicSuffixDiscouragesInstructionRestatement(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	agentInstance := &agent{}
	text := agentInstance.rebuildDynamicSuffix(
		context.Background(),
		"please fix the bug",
		"coding",
		"",
		"",
		nil,
	)

	for _, expected := range []string{
		"focused only on new judgment or the next action",
		"Do not restate the user's request",
		"recap already-known evidence",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in dynamic suffix, got %q", expected, text)
		}
	}
}

func TestContextLimitsUseRaisedHeadroomRatios(t *testing.T) {
	if got := contextSoftLimitTokens(256000); got != 153600 {
		t.Fatalf("expected raised soft limit 153600, got %d", got)
	}
	if got := contextHardLimitTokens(256000); got != 217600 {
		t.Fatalf("expected raised hard limit 217600, got %d", got)
	}
}

func TestRebuildDynamicSuffixAddsWordTableEditGuidance(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	agentInstance := &agent{}
	state := &sessionTaskState{
		CachedWordTableLocator: &sessionWordTableLocator{
			ReaderSessionID: "reader-session-3",
			TableID:         "tbl-risk",
			RowIndex:        intPtr(2),
			CellIndex:       intPtr(1),
		},
	}

	text := agentInstance.rebuildDynamicSuffix(
		context.Background(),
		"[WORD_CONTEXT]\nediting_policy: interface_only\nnotes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作必须通过 go-word 接口完成。\n[/WORD_CONTEXT]\n把表格里的通过率改成92%。",
		"coding",
		"",
		"",
		state,
	)

	for _, expected := range []string{"action=`update_table_cell`", "`new_text` plus `reader_session_id`", "reader_session_id=reader-session-3", "table_id=tbl-risk", "row_index=2", "cell_index=1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in word table edit dynamic suffix, got %q", expected, text)
		}
	}
}

func TestBuildSilentTurnRecoveryMessageUsesSystemRole(t *testing.T) {
	recoveryText := "SYSTEM MONITOR: test recovery"
	msg := buildSilentTurnRecoveryMessage(recoveryText)

	if msg.Role != message.System {
		t.Fatalf("expected system role, got %q", msg.Role)
	}

	if got := msg.Content().Text; got != recoveryText {
		t.Fatalf("expected recovery text %q, got %q", recoveryText, got)
	}
}

func TestIsInternalControlMessageText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "monitor message", text: "SYSTEM MONITOR: retry with a different tool", want: true},
		{name: "checkpoint message", text: "SYSTEM CHECKPOINT: stop looping", want: true},
		{name: "compression marker", text: "SYSTEM: 上下文已压缩。", want: true},
		{name: "normal user text", text: "请继续修复这个问题", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInternalControlMessageText(tt.text); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestFilterSessionHistoryMessages(t *testing.T) {
	msgs := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "请修复 backend 卡死问题"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "SYSTEM MONITOR: switch strategy"}}},
		{Role: message.System, Parts: []message.ContentPart{message.TextContent{Text: "SYSTEM MONITOR: transient guidance"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "SYSTEM: 上下文已压缩。"}}},
	}

	filtered := filterSessionHistoryMessages(msgs)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 messages after filtering, got %d", len(filtered))
	}
	if filtered[0].Content().Text != "请修复 backend 卡死问题" {
		t.Fatalf("expected normal user message to remain, got %q", filtered[0].Content().Text)
	}
	if filtered[1].Role != message.System || filtered[1].Content().Text != "SYSTEM MONITOR: transient guidance" {
		t.Fatalf("expected system guidance to remain available for transient replay, got role=%q text=%q", filtered[1].Role, filtered[1].Content().Text)
	}
}

func TestShouldSerializeToolCalls(t *testing.T) {
	parallelOnly := []message.ToolCall{
		{Name: "read", Input: `{"file_path":"backend/main.go"}`},
		{Name: "grep", Input: `{"pattern":"Summarize","path":"backend"}`},
	}
	if shouldSerializeToolCalls(parallelOnly) {
		t.Fatalf("expected read-only batch to stay parallel")
	}

	withMutation := []message.ToolCall{
		{Name: "read", Input: `{"file_path":"backend/main.go"}`},
		{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
	}
	if !shouldSerializeToolCalls(withMutation) {
		t.Fatalf("expected mutating batch to be serialized")
	}

	differentFileMutations := []message.ToolCall{
		{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
		{Name: "edit", Input: `{"file_path":"backend/README.md","old_string":"foo","new_string":"bar"}`},
	}
	if shouldSerializeToolCalls(differentFileMutations) {
		t.Fatalf("expected different-file mutating batch to avoid full serialization")
	}
}

func TestBuildToolExecutionWavesForState(t *testing.T) {
	t.Run("diagnostic batch runs grep before downstream reads", func(t *testing.T) {
		state := &sessionTaskState{
			ReadOnlyMode:   true,
			DiagnosticMode: true,
			DiagnosticRoot: "workspace/mixed_release_triage",
		}
		waves := buildToolExecutionWavesForState(state, []message.ToolCall{
			{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/service/order_sync.py"}`},
			{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`},
			{Name: "grep", Input: `{"path":"workspace/mixed_release_triage","pattern":"cache-fallback|/v1/orders/sync|timeout_sla_seconds|api_version"}`},
			{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/ops/risk_isolation_notes.md"}`},
			{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/tests/failing_checks.txt"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 4}, {2}, {1, 3}})
	})

	t.Run("completed diagnostic grep keeps batch parallel", func(t *testing.T) {
		state := &sessionTaskState{
			ReadOnlyMode:   true,
			DiagnosticMode: true,
			DiagnosticRoot: "workspace/mixed_release_triage",
			ExecutionJournal: []taskExecutionJournalEntry{
				{Tool: "grep", Status: "success", Target: "workspace/mixed_release_triage"},
			},
		}
		waves := buildToolExecutionWavesForState(state, []message.ToolCall{
			{Name: "read", Input: `{"file_path":"workspace/mixed_release_triage/dashboard/kpi_snapshot.json"}`},
			{Name: "grep", Input: `{"path":"workspace/mixed_release_triage","pattern":"cache-fallback"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 1}})
	})
}

func TestBuildToolExecutionWaves(t *testing.T) {
	t.Run("read only stays in one wave", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "read", Input: `{"file_path":"backend/main.go"}`},
			{Name: "grep", Input: `{"pattern":"agent","path":"backend"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 1}})
	})

	t.Run("different file mutations share one wave", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
			{Name: "edit", Input: `{"file_path":"backend/README.md","old_string":"foo","new_string":"bar"}`},
			{Name: "write", Input: `{"file_path":"docs/plan.md","content":"x"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 1, 2}})
	})

	t.Run("same file conflicts move to later wave", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
			{Name: "edit", Input: `{"file_path":"backend/README.md","old_string":"foo","new_string":"bar"}`},
			{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"bar","new_string":"baz"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 1}, {2}})
	})

	t.Run("patch and batch write use extracted targets", func(t *testing.T) {
		patchPayload, err := json.Marshal(map[string]string{
			"patch_text": strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: backend/main.go",
				"@@",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
		})
		if err != nil {
			t.Fatalf("failed to marshal patch payload: %v", err)
		}

		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "patch", Input: string(patchPayload)},
			{Name: "batch_write", Input: `{"files":{"backend/README.md":"x","docs/plan.md":"y"}}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0, 1}})
	})

	t.Run("mixed mutating and read falls back to serial", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
			{Name: "read", Input: `{"file_path":"backend/README.md"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0}, {1}})
	})

	t.Run("unknown mutating target falls back to serial", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "edit", Input: `not-json`},
			{Name: "edit", Input: `{"file_path":"backend/README.md","old_string":"foo","new_string":"bar"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0}, {1}})
	})

	t.Run("normalized path conflicts are serialized", func(t *testing.T) {
		normalized := filepath.ToSlash(filepath.Join("backend", ".", "main.go"))
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "edit", Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`},
			{Name: "edit", Input: `{"file_path":"` + normalized + `","old_string":"bar","new_string":"baz"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0}, {1}})
	})

	t.Run("all manage plan calls are serialized", func(t *testing.T) {
		waves := buildToolExecutionWaves([]message.ToolCall{
			{Name: "manage_plan", Input: `{"action":"add","item":"one"}`},
			{Name: "manage_plan", Input: `{"action":"add","item":"two","filename":"other-plan"}`},
			{Name: "manage_plan", Input: `{"action":"complete","id":"abc12345"}`},
		})
		assertExecutionWaves(t, waves, [][]int{{0}, {1}, {2}})
	})
}

func assertExecutionWaves(t *testing.T, got, want [][]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d waves, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("expected wave %d size %d, got %d (%v)", i, len(want[i]), len(got[i]), got)
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("expected waves %v, got %v", want, got)
			}
		}
	}
}

func TestExecuteAssistantToolCallsStartsDisjointEditsTogether(t *testing.T) {
	probe := newBarrierEditTool(8, 8)
	agentUnderTest := newToolExecutionTestAgent(probe)
	assistantMsg := buildAssistantToolMessage("session-disjoint", buildEditToolCalls(
		"backend/a.go",
		"backend/b.go",
		"backend/c.go",
		"backend/d.go",
		"backend/e.go",
		"backend/f.go",
		"backend/g.go",
		"backend/h.go",
	))

	resultCh := make(chan error, 1)
	go func() {
		_, toolMessage, err := agentUnderTest.executeAssistantToolCalls(context.Background(), assistantMsg.SessionID, "请并行修改这些文件", []message.Message{assistantMsg}, assistantMsg, []message.Message{assistantMsg}, nil)
		if err == nil && toolMessage != nil {
			if got := len(toolMessage.ToolResults()); got != 8 {
				err = fmt.Errorf("expected 8 tool results, got %d", got)
			}
		}
		resultCh <- err
	}()

	probe.waitForMilestone(t, 8, 5*time.Second)
	if probe.startedCount() != 8 {
		t.Fatalf("expected all 8 disjoint edits to start before release, got %d", probe.startedCount())
	}
	probe.release()
	if err := <-resultCh; err != nil {
		t.Fatalf("executeAssistantToolCalls returned error: %v", err)
	}
	if probe.maxConcurrentRuns() < 8 {
		t.Fatalf("expected 8 concurrent disjoint edits, got max %d", probe.maxConcurrentRuns())
	}
}

func TestExecuteAssistantToolCallsDelaysConflictingEditWave(t *testing.T) {
	probe := newBarrierEditTool(2)
	agentUnderTest := newToolExecutionTestAgent(probe)
	assistantMsg := buildAssistantToolMessage("session-conflict", []message.ToolCall{
		{
			ID:    "tool-0",
			Name:  "edit",
			Input: `{"file_path":"backend/main.go","old_string":"foo","new_string":"bar"}`,
		},
		{
			ID:    "tool-1",
			Name:  "edit",
			Input: `{"file_path":"backend/README.md","old_string":"foo","new_string":"bar"}`,
		},
		{
			ID:    "tool-2",
			Name:  "edit",
			Input: `{"file_path":"backend/main.go","old_string":"bar","new_string":"baz"}`,
		},
	})

	resultCh := make(chan error, 1)
	go func() {
		_, toolMessage, err := agentUnderTest.executeAssistantToolCalls(context.Background(), assistantMsg.SessionID, "请修改这些文件", []message.Message{assistantMsg}, assistantMsg, []message.Message{assistantMsg}, nil)
		if err == nil && toolMessage != nil {
			if got := len(toolMessage.ToolResults()); got != 3 {
				err = fmt.Errorf("expected 3 tool results, got %d", got)
			}
		}
		resultCh <- err
	}()

	probe.waitForMilestone(t, 2, 5*time.Second)
	if probe.startedCount() != 2 {
		t.Fatalf("expected only first wave to start while blocked, got %d starts", probe.startedCount())
	}
	counts := probe.startedPathCounts()
	if counts["backend/main.go"] != 1 || counts["backend/README.md"] != 1 {
		t.Fatalf("expected first wave to contain one main.go and one README.md edit, got %#v", counts)
	}
	probe.release()
	if err := <-resultCh; err != nil {
		t.Fatalf("executeAssistantToolCalls returned error: %v", err)
	}
	if probe.startedCount() != 3 {
		t.Fatalf("expected all 3 edits to run after release, got %d", probe.startedCount())
	}
	if probe.maxConcurrentRuns() < 2 {
		t.Fatalf("expected conflicting batch to still run first wave concurrently, got max %d", probe.maxConcurrentRuns())
	}
}

func TestExecuteAssistantToolCallsEmitsToolResultForMissingTool(t *testing.T) {
	assistantMsg := buildAssistantToolMessage("session-missing-tool", []message.ToolCall{{
		ID:    "tool-missing",
		Name:  "unloaded-skill",
		Input: `{"action":"run"}`,
	}})
	events := make(chan AgentEvent, 4)
	agentUnderTest := &agent{
		Broker:      pubsub.NewBroker[AgentEvent](),
		messages:    newInMemoryMessageService(),
		allTools:    map[string]tools.BaseTool{},
		activeTools: map[string]bool{},
	}

	_, toolMessage, err := agentUnderTest.executeAssistantToolCalls(context.Background(), assistantMsg.SessionID, "请执行未加载技能", []message.Message{assistantMsg}, assistantMsg, []message.Message{assistantMsg}, events)
	if err != nil {
		t.Fatalf("executeAssistantToolCalls returned error: %v", err)
	}
	if toolMessage == nil {
		t.Fatalf("expected tool message to be created")
	}
	results := toolMessage.ToolResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(results))
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "Tool not found: unloaded-skill") {
		t.Fatalf("expected missing tool result to be persisted, got %+v", results[0])
	}

	if len(events) != 2 {
		t.Fatalf("expected tool_use and tool_result events, got %d", len(events))
	}
	toolUseEvent := <-events
	toolResultEvent := <-events
	if toolUseEvent.Type != AgentEventTypeToolUse {
		t.Fatalf("expected first event to be tool_use, got %s", toolUseEvent.Type)
	}
	if toolResultEvent.Type != AgentEventTypeToolResult {
		t.Fatalf("expected second event to be tool_result, got %s", toolResultEvent.Type)
	}
	if toolResultEvent.ToolResult == nil {
		t.Fatalf("expected tool_result payload")
	}
	if !toolResultEvent.ToolResult.IsError || !strings.Contains(toolResultEvent.ToolResult.Content, "Tool not found: unloaded-skill") {
		t.Fatalf("expected missing tool error event, got %+v", toolResultEvent.ToolResult)
	}
	expectedExecutionID := buildToolExecutionID(assistantMsg.ID, &assistantMsg.ToolCalls()[0])
	if toolUseEvent.ToolExecutionID != expectedExecutionID || toolResultEvent.ToolExecutionID != expectedExecutionID {
		t.Fatalf("expected execution id %q, got tool_use=%q tool_result=%q", expectedExecutionID, toolUseEvent.ToolExecutionID, toolResultEvent.ToolExecutionID)
	}
}

func TestExecuteAssistantToolCallsEmitsToolResultWhenToolPanics(t *testing.T) {
	assistantMsg := buildAssistantToolMessage("session-panic-tool", []message.ToolCall{{
		ID:    "tool-panic",
		Name:  "panic_tool",
		Input: `{}`,
	}})
	events := make(chan AgentEvent, 4)
	agentUnderTest := newToolExecutionTestAgent(panicTool{})

	_, toolMessage, err := agentUnderTest.executeAssistantToolCalls(context.Background(), assistantMsg.SessionID, "请执行会 panic 的工具", []message.Message{assistantMsg}, assistantMsg, []message.Message{assistantMsg}, events)
	if err != nil {
		t.Fatalf("executeAssistantToolCalls returned error: %v", err)
	}
	if toolMessage == nil {
		t.Fatalf("expected tool message to be created")
	}
	results := toolMessage.ToolResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(results))
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "Tool execution panicked") {
		t.Fatalf("expected panic result to be persisted, got %+v", results[0])
	}
	if len(events) != 2 {
		t.Fatalf("expected tool_use and tool_result events, got %d", len(events))
	}
	<-events
	toolResultEvent := <-events
	if toolResultEvent.Type != AgentEventTypeToolResult {
		t.Fatalf("expected second event to be tool_result, got %s", toolResultEvent.Type)
	}
	if toolResultEvent.ToolResult == nil || !toolResultEvent.ToolResult.IsError || !strings.Contains(toolResultEvent.ToolResult.Content, "Tool execution panicked") {
		t.Fatalf("expected panic tool error event, got %+v", toolResultEvent.ToolResult)
	}
}

type inMemoryMessageService struct {
	*pubsub.Broker[message.Message]
	mu         sync.Mutex
	messages   map[string]message.Message
	createRuns int
	updateRuns int
}

func newInMemoryMessageService() *inMemoryMessageService {
	return &inMemoryMessageService{
		Broker:   pubsub.NewBroker[message.Message](),
		messages: make(map[string]message.Message),
	}
}

func (s *inMemoryMessageService) Create(_ context.Context, sessionID string, params message.CreateMessageParams) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createRuns++
	msg := message.Message{
		ID:        fmt.Sprintf("msg-%d", s.createRuns),
		SessionID: sessionID,
		Role:      params.Role,
		Parts:     append([]message.ContentPart(nil), params.Parts...),
		Model:     params.Model,
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
	s.messages[msg.ID] = msg
	return msg, nil
}

func (s *inMemoryMessageService) Update(_ context.Context, msg message.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateRuns++
	msg.UpdatedAt = time.Now().UnixMilli()
	s.messages[msg.ID] = msg
	return nil
}

func (s *inMemoryMessageService) Get(_ context.Context, id string) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.messages[id]
	if !ok {
		return message.Message{}, fmt.Errorf("message %s not found", id)
	}
	return msg, nil
}

func (s *inMemoryMessageService) List(_ context.Context, sessionID string) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]message.Message, 0)
	for _, msg := range s.messages {
		if msg.SessionID == sessionID {
			result = append(result, msg)
		}
	}
	return result, nil
}

func (s *inMemoryMessageService) ListWindow(ctx context.Context, sessionID string, _ string, _ int) ([]message.Message, error) {
	return s.List(ctx, sessionID)
}

func (s *inMemoryMessageService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, id)
	return nil
}

func (s *inMemoryMessageService) DeleteSessionMessages(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, msg := range s.messages {
		if msg.SessionID == sessionID {
			delete(s.messages, id)
		}
	}
	return nil
}

func (s *inMemoryMessageService) Search(_ context.Context, _ string, _ int) ([]message.Message, error) {
	return nil, nil
}

func (s *inMemoryMessageService) SearchInSession(_ context.Context, _ string, _ string, _ int) ([]message.Message, error) {
	return nil, nil
}

type barrierEditTool struct {
	releaseCh    chan struct{}
	milestones   map[int]chan struct{}
	mu           sync.Mutex
	startedPaths []string
	running      int
	maxRunning   int
}

func newBarrierEditTool(milestones ...int) *barrierEditTool {
	tool := &barrierEditTool{
		releaseCh:  make(chan struct{}),
		milestones: make(map[int]chan struct{}, len(milestones)),
	}
	for _, milestone := range milestones {
		tool.milestones[milestone] = make(chan struct{})
	}
	return tool
}

func (t *barrierEditTool) Info() tools.ToolInfo {
	return tools.ToolInfo{Name: "edit", Description: "test edit tool"}
}

func (t *barrierEditTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	var payload struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(params.Input), &payload); err != nil {
		return tools.NewTextErrorResponse("invalid input"), err
	}

	t.mu.Lock()
	t.startedPaths = append(t.startedPaths, payload.FilePath)
	t.running++
	if t.running > t.maxRunning {
		t.maxRunning = t.running
	}
	started := len(t.startedPaths)
	if milestoneCh, ok := t.milestones[started]; ok {
		close(milestoneCh)
		delete(t.milestones, started)
	}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.running--
	}()

	select {
	case <-ctx.Done():
		return tools.NewTextErrorResponse(ctx.Err().Error()), ctx.Err()
	case <-t.releaseCh:
		return tools.NewTextResponse(payload.FilePath), nil
	}
}

type panicTool struct{}

func (panicTool) Info() tools.ToolInfo {
	return tools.ToolInfo{Name: "panic_tool"}
}

func (panicTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	panic("boom")
}

func (t *barrierEditTool) release() {
	close(t.releaseCh)
}

func (t *barrierEditTool) waitForMilestone(testingT *testing.T, milestone int, timeout time.Duration) {
	testingT.Helper()
	ch, ok := t.milestones[milestone]
	if !ok {
		testingT.Fatalf("milestone %d not registered", milestone)
	}
	select {
	case <-ch:
	case <-time.After(timeout):
		testingT.Fatalf("timed out waiting for milestone %d", milestone)
	}
}

func (t *barrierEditTool) startedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.startedPaths)
}

func (t *barrierEditTool) maxConcurrentRuns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxRunning
}

func (t *barrierEditTool) startedPathCounts() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	counts := make(map[string]int, len(t.startedPaths))
	for _, path := range t.startedPaths {
		counts[path]++
	}
	return counts
}

func newToolExecutionTestAgent(tool tools.BaseTool) *agent {
	return &agent{
		Broker:      pubsub.NewBroker[AgentEvent](),
		messages:    newInMemoryMessageService(),
		allTools:    map[string]tools.BaseTool{tool.Info().Name: tool},
		activeTools: map[string]bool{tool.Info().Name: true},
	}
}

func buildAssistantToolMessage(sessionID string, toolCalls []message.ToolCall) message.Message {
	msg := message.Message{ID: "assistant-tool-batch", SessionID: sessionID, Role: message.Assistant, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli()}
	for _, toolCall := range toolCalls {
		msg.AddToolCall(toolCall)
	}
	return msg
}

func buildEditToolCalls(paths ...string) []message.ToolCall {
	toolCalls := make([]message.ToolCall, 0, len(paths))
	for index, path := range paths {
		toolCalls = append(toolCalls, message.ToolCall{
			ID:    fmt.Sprintf("tool-%d", index),
			Name:  "edit",
			Input: fmt.Sprintf(`{"file_path":%q,"old_string":"foo","new_string":"bar"}`, path),
		})
	}
	return toolCalls
}

func TestShouldTriggerSilentTurnRecovery(t *testing.T) {
	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			name: "empty assistant content triggers recovery",
			msg: message.Message{
				Role:  message.Assistant,
				Parts: []message.ContentPart{},
			},
			want: true,
		},
		{
			name: "plain text does not trigger recovery",
			msg: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "已完成处理"},
				},
			},
			want: false,
		},
		{
			name: "reasoning content triggers recovery because it is not user-facing",
			msg: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ReasoningContent{Thinking: "继续分析中"},
				},
			},
			want: true,
		},
		{
			name: "tool calls do not trigger recovery",
			msg: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{Name: "read", Input: `{}`},
				},
			},
			want: false,
		},
		{
			name: "thinking tag text is handled elsewhere",
			msg: message.Message{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "<thinking>继续思考</thinking>"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldTriggerSilentTurnRecovery(tt.msg)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestSanitizeMessageForModelReplayStripsThinkingButKeepsToolCalls(t *testing.T) {
	msg := message.Message{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "user said do x"},
			message.TextContent{Text: "<thinking>user asked for x</thinking>\n\nVisible answer"},
			message.ToolCall{ID: "call-1", Name: "read", Input: `{"file_path":"a.go"}`, Type: "function"},
			message.Finish{Reason: message.FinishReasonToolUse},
		},
	}

	sanitized, keep := sanitizeMessageForModelReplay(msg)
	if !keep {
		t.Fatal("expected assistant tool-call message to be kept")
	}
	if sanitized.ReasoningContent().Thinking != "" {
		t.Fatalf("expected reasoning content to be stripped, got %q", sanitized.ReasoningContent().Thinking)
	}
	if got := sanitized.Content().Text; got != "Visible answer" {
		t.Fatalf("expected visible text only, got %q", got)
	}
	if calls := sanitized.ToolCalls(); len(calls) != 1 || calls[0].ID != "call-1" {
		t.Fatalf("expected tool call to be preserved, got %#v", calls)
	}
	if msg.ReasoningContent().Thinking == "" {
		t.Fatal("expected original message to remain unchanged")
	}
}

func TestSanitizeMessageForModelReplayDropsPureThinkingMessage(t *testing.T) {
	msg := message.Message{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "private scratchpad"},
			message.TextContent{Text: "<thinking>still private</thinking>"},
			message.Finish{Reason: message.FinishReasonEndTurn},
		},
	}

	_, keep := sanitizeMessageForModelReplay(msg)
	if keep {
		t.Fatal("expected pure thinking assistant message to be dropped from model replay")
	}
}

func TestStripThinkingBlocksForModelReplayHandlesVariants(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "uppercase tag",
			in:   "<Thinking>User said do x</Thinking>\nVisible",
			want: "Visible",
		},
		{
			name: "tag attributes",
			in:   `<thinking phase="plan">private</thinking>Visible`,
			want: "Visible",
		},
		{
			name: "escaped tag",
			in:   "&lt;thinking&gt;private&lt;/thinking&gt;\nVisible",
			want: "Visible",
		},
		{
			name: "unclosed tag",
			in:   "Visible before\n<thinking>private tail",
			want: "Visible before",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripThinkingBlocksForModelReplay(tt.in); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestSanitizeMessagesForModelReplayPreservesToolResultPairing(t *testing.T) {
	msgs := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "<thinking>call read</thinking>"},
				message.ToolCall{ID: "call-1", Name: "read", Input: `{}`, Type: "function"},
			},
		},
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call-1", Name: "read", Content: "ok"},
			},
		},
	}

	sanitized := sanitizeMessagesForModelReplay(msgs)
	if len(sanitized) != 2 {
		t.Fatalf("expected assistant tool call and tool result to remain paired, got %d messages", len(sanitized))
	}
	if got := sanitized[0].Content().Text; got != "" {
		t.Fatalf("expected thinking text to be stripped from tool-call message, got %q", got)
	}
	if len(sanitized[0].ToolCalls()) != 1 || len(sanitized[1].ToolResults()) != 1 {
		t.Fatalf("expected tool call/result pair to be preserved, got %#v", sanitized)
	}
}

func TestSanitizeMessageForModelReplayStripsThinkingFromDelegationToolResultsOnly(t *testing.T) {
	msgs := []message.Message{
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "call-worker",
					Name:       "wait_for_workers",
					Content:    "<thinking>用户要求分析 worker 输出</thinking>\n\nWorker summary",
				},
				message.ToolResult{
					ToolCallID: "call-read",
					Name:       "read",
					Content:    "<thinking>this is literal file content</thinking>\n\npackage main",
				},
				message.ToolResult{
					ToolCallID: "call-empty-worker",
					Name:       "manage_worker",
					Content:    `<Thinking phase="plan">private worker scratchpad</Thinking>`,
				},
			},
		},
	}

	sanitized := sanitizeMessagesForModelReplay(msgs)
	if len(sanitized) != 1 {
		t.Fatalf("expected tool message to remain, got %d messages", len(sanitized))
	}
	results := sanitized[0].ToolResults()
	if len(results) != 3 {
		t.Fatalf("expected both tool results to remain, got %#v", results)
	}
	if got := results[0].Content; got != "Worker summary" {
		t.Fatalf("expected delegation tool thinking to be stripped, got %q", got)
	}
	if got := results[1].Content; !strings.Contains(got, "<thinking>this is literal file content</thinking>") {
		t.Fatalf("expected read tool literal thinking content to be preserved, got %q", got)
	}
	if got := results[2].Content; got != "[System: Delegated worker thinking omitted from model replay.]" {
		t.Fatalf("expected pure delegation thinking to be replaced by a stable placeholder, got %q", got)
	}
	if original := msgs[0].ToolResults()[0].Content; !strings.Contains(original, "用户要求") {
		t.Fatalf("expected original tool result to remain unchanged, got %q", original)
	}
}

func TestNewAgent(t *testing.T) {
}

func TestCreateAgentProviderFallsBackToCustomMainModelWhenAuraModelsUnavailable(t *testing.T) {
	loadEmptyAgentTestConfig(t)

	cfg := config.Get()
	if cfg == nil {
		t.Fatal("expected config to be loaded")
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[config.AgentName]config.Agent)
	}

	originalCoder, hadCoder := cfg.Agents[config.AgentCoder]
	originalMainModel := cfg.MainModel
	originalCustomMain, hadCustomMain := models.SupportedModels[routing.CustomMainModelID]
	t.Cleanup(func() {
		if hadCoder {
			cfg.Agents[config.AgentCoder] = originalCoder
		} else {
			delete(cfg.Agents, config.AgentCoder)
		}
		cfg.MainModel = originalMainModel
		if hadCustomMain {
			models.SupportedModels[routing.CustomMainModelID] = originalCustomMain
		} else {
			delete(models.SupportedModels, routing.CustomMainModelID)
		}
	})

	cfg.Agents[config.AgentCoder] = config.Agent{Model: models.QwenPlus, MaxTokens: 8192}
	cfg.MainModel = &config.MainModelConfig{
		Provider:  models.ProviderCustom,
		Model:     "Qwen3.6-35B-A3B",
		APIKey:    "test-key",
		BaseURL:   "http://127.0.0.1:18080/v1",
		MaxTokens: 8192,
	}
	models.SupportedModels[routing.CustomMainModelID] = models.Model{
		ID:               routing.CustomMainModelID,
		Name:             "Custom Main Model",
		Provider:         models.ProviderCustom,
		APIKey:           "test-key",
		BaseURL:          "http://127.0.0.1:18080/v1",
		APIModel:         "Qwen3.6-35B-A3B",
		DefaultMaxTokens: 8192,
	}

	resolvedProvider, err := createAgentProvider(config.AgentCoder)
	if err != nil {
		t.Fatalf("expected custom main model fallback to succeed, got error: %v", err)
	}
	if resolvedProvider.Model().ID != routing.CustomMainModelID {
		t.Fatalf("expected fallback model %q, got %q", routing.CustomMainModelID, resolvedProvider.Model().ID)
	}
}

func TestLoadTool(t *testing.T) {
}

func TestGetTool(t *testing.T) {
}

func TestListAvailableTools(t *testing.T) {
}

func TestInspectTool(t *testing.T) {
}

func TestIsLoaded(t *testing.T) {
}

func TestModel(t *testing.T) {
}

func TestCancel(t *testing.T) {
}

func TestIsBusy(t *testing.T) {
}

func TestIsSessionBusy(t *testing.T) {
}

func TestRun(t *testing.T) {
}

func TestResolveExcessiveModificationAction(t *testing.T) {
	if got := resolveExcessiveModificationAction(false); got != excessiveModificationActionCheckpoint {
		t.Fatalf("expected checkpoint action before user confirmation, got %v", got)
	}

	if got := resolveExcessiveModificationAction(true); got != excessiveModificationActionStop {
		t.Fatalf("expected stop action after checkpoint was already injected, got %v", got)
	}
}

func TestExcessiveModificationStopParts(t *testing.T) {
	parts := excessiveModificationStopParts()
	if len(parts) != 1 {
		t.Fatalf("expected one stop message part, got %d", len(parts))
	}

	textPart, ok := parts[0].(message.TextContent)
	if !ok {
		t.Fatalf("expected text content part, got %T", parts[0])
	}

	if !strings.Contains(textPart.Text, "我已经进行了多次代码修改") {
		t.Fatalf("expected stop message to explain modification loop, got %q", textPart.Text)
	}
	if !strings.Contains(textPart.Text, "请你确认") {
		t.Fatalf("expected stop message to request user confirmation, got %q", textPart.Text)
	}
}
