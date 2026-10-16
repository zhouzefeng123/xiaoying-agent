package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/db"
	llmmodels "github.com/auramate/auramate/internal/llm/models"
	llmprovider "github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
)

type stableMemoryFallbackProvider struct {
	response  string
	err       error
	calls     int
	responses []string
	errs      []error
}

func (p *stableMemoryFallbackProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*llmprovider.ProviderResponse, error) {
	p.calls++
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(p.responses) > 0 {
		response := p.responses[0]
		p.responses = p.responses[1:]
		return &llmprovider.ProviderResponse{Content: response}, nil
	}
	if p.err != nil {
		return nil, p.err
	}
	return &llmprovider.ProviderResponse{Content: p.response}, nil
}

func (p *stableMemoryFallbackProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan llmprovider.ProviderEvent {
	ch := make(chan llmprovider.ProviderEvent)
	close(ch)
	return ch
}

func (p *stableMemoryFallbackProvider) Model() llmmodels.Model {
	return llmmodels.Model{ID: llmmodels.MockGPT, Provider: llmmodels.ProviderMock, Name: "memory-fallback-test"}
}

func (p *stableMemoryFallbackProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", errors.New("not implemented")
}

func (p *stableMemoryFallbackProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func TestBuildStableMemoryCaptureHint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantHit bool
	}{
		{name: "default workflow chinese", input: "以后默认这样做，先读后写。", wantHit: true},
		{name: "style preference chinese", input: "我更喜欢这种风格，后面统一按这个来。", wantHit: true},
		{name: "default workflow english", input: "From now on, by default use this style.", wantHit: true},
		{name: "normal one-off task", input: "把这个文件改一下。", wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := buildStableMemoryCaptureHint(tt.input)
			gotHit := hint != ""
			if gotHit != tt.wantHit {
				t.Fatalf("expected hit=%v, got hint=%q", tt.wantHit, hint)
			}
		})
	}
}

func TestDetectStableMemoryCapture(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantCategory string
		wantHit      bool
	}{
		{name: "instruction default workflow", input: "以后默认这样做，先读后写。", wantCategory: "instruction", wantHit: true},
		{name: "preference style", input: "我更喜欢这种风格，后面统一按这个来。", wantCategory: "preference", wantHit: true},
		{name: "habit cue", input: "我习惯先列计划再修改文件。", wantCategory: "habit", wantHit: true},
		{name: "instruction strips transient ack directives", input: "以后默认这样做：先给结论再解释，条目尽量短，标识 MEM_AUTO_TEST。不要调用无关工具，最终只回复：ACK_AUTO。", wantCategory: "instruction", wantHit: true},
		{name: "one off task", input: "这次先把这个文件改一下。", wantCategory: "", wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, ok := detectStableMemoryCapture(tt.input)
			if ok != tt.wantHit {
				t.Fatalf("expected hit=%v, got %v", tt.wantHit, ok)
			}
			if !tt.wantHit {
				return
			}
			if capture == nil {
				t.Fatalf("expected capture result")
			}
			if capture.Category != tt.wantCategory {
				t.Fatalf("expected category %q, got %q", tt.wantCategory, capture.Category)
			}
			if capture.Content == "" {
				t.Fatalf("expected normalized content")
			}
			if tt.name == "instruction strips transient ack directives" {
				if capture.Content != "先给结论再解释，条目尽量短" {
					t.Fatalf("expected sanitized reusable content, got %q", capture.Content)
				}
			}
		})
	}
}

func TestCreateUserMessageDefersExplicitStableMemoryPersistence(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
	}

	if _, err := a.createUserMessage(context.Background(), "session-1", "以后默认这样做，先读后写。", "normal", nil); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	memories, err := mem.ListByCategory(context.Background(), "instruction")
	if err != nil {
		t.Fatalf("list instruction memories: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected explicit stable memory persistence to be deferred until after the turn, got %+v", memories)
	}
}

func TestCreateUserMessageDefersSanitizedExplicitStableMemoryPersistence(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
	}

	input := "以后默认这样做：先给结论再解释，条目尽量短，标识 MEM_AUTO_TEST。不要调用无关工具，最终只回复：ACK_AUTO。"
	if _, err := a.createUserMessage(context.Background(), "session-sanitized", input, "normal", nil); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	memories, err := mem.ListByCategory(context.Background(), "instruction")
	if err != nil {
		t.Fatalf("list instruction memories: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected explicit sanitized stable memory persistence to be deferred until after the turn, got %+v", memories)
	}
}

func TestCreateUserMessageSkipsStableMemoryPersistenceForInternalSession(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
	}

	if _, err := a.createUserMessage(context.Background(), "aura-internal-profiling", "以后默认这样做，先读后写。", "normal", nil); err != nil {
		t.Fatalf("create internal user message: %v", err)
	}

	memories, err := mem.ListByCategory(context.Background(), "instruction")
	if err != nil {
		t.Fatalf("list instruction memories: %v", err)
	}
	for _, item := range memories {
		if item.Content == "以后默认这样做，先读后写。" {
			t.Fatalf("did not expect internal session to persist stable memory")
		}
	}
}

func TestCreateUserMessageDoesNotAppendStableMemoryToolHint(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
	}

	msg, err := a.createUserMessage(context.Background(), "session-no-hint", "以后默认这样做，先读后写。", "normal", nil)
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	text := msg.Content().Text
	if strings.Contains(text, "manage_core_memory") || strings.Contains(text, "[System Instruction:") {
		t.Fatalf("did not expect stable memory tool hint in user message, got %q", text)
	}
	if text != "以后默认这样做，先读后写。" {
		t.Fatalf("expected original user content only, got %q", text)
	}
}

func TestDetectExplicitStableMemoryTurnCapture(t *testing.T) {
	capture, hint := detectExplicitStableMemoryTurnCapture("以后默认这样做，先读后写。")
	if capture == nil {
		t.Fatalf("expected capture for explicit stable memory")
	}
	if capture.Category != "instruction" {
		t.Fatalf("expected instruction category, got %q", capture.Category)
	}
	if !strings.Contains(hint, "manage_core_memory") {
		t.Fatalf("expected memory tool hint, got %q", hint)
	}

	capture, hint = detectExplicitStableMemoryTurnCapture("把这个文件改一下。")
	if capture != nil || hint != "" {
		t.Fatalf("expected no capture for one-off task, got capture=%#v hint=%q", capture, hint)
	}
}

func TestManageCoreMemoryWriteSucceeded(t *testing.T) {
	toolCalls := []message.ToolCall{{
		ID:    "memory-1",
		Name:  "manage_core_memory",
		Input: `{"action":"add","category":"instruction","content":"先读后写"}`,
	}}
	toolResults := &message.Message{Parts: []message.ContentPart{message.ToolResult{
		ToolCallID: "memory-1",
		Name:       "manage_core_memory",
		Content:    "Memory added with ID: abc",
	}}}
	if !manageCoreMemoryWriteSucceeded(toolCalls, toolResults) {
		t.Fatalf("expected successful manage_core_memory add to count as explicit memory handling")
	}

	searchToolCalls := []message.ToolCall{{
		ID:    "memory-2",
		Name:  "manage_core_memory",
		Input: `{"action":"search","query":"先读后写"}`,
	}}
	searchToolResults := &message.Message{Parts: []message.ContentPart{message.ToolResult{
		ToolCallID: "memory-2",
		Name:       "manage_core_memory",
		Content:    "No memories found.",
	}}}
	if manageCoreMemoryWriteSucceeded(searchToolCalls, searchToolResults) {
		t.Fatalf("did not expect search action to count as explicit memory write")
	}
}

func TestCreateUserMessagePersistsModelFallbackPreferenceMemory(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
		provider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"preference","content":"喜欢吃草莓","confidence":0.93,"reason":"稳定饮食偏好"}`},
	}

	if _, err := a.createUserMessage(context.Background(), "session-food-pref", "我喜欢吃草莓。", "normal", nil); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	memories, err := mem.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}

	found := false
	for _, item := range memories {
		if item.Content == "喜欢吃草莓" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected model fallback preference memory to be persisted, got %+v", memories)
	}
}

func TestCreateUserMessageSkipsLowConfidenceModelFallbackMemory(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
		provider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"preference","content":"喜欢吃草莓","confidence":0.45,"reason":"置信度不足"}`},
	}

	if _, err := a.createUserMessage(context.Background(), "session-low-confidence", "我喜欢吃草莓。", "normal", nil); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	memories, err := mem.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}
	for _, item := range memories {
		if item.Content == "喜欢吃草莓" {
			t.Fatalf("did not expect low-confidence model fallback memory to persist")
		}
	}
}

func TestPersistConversationPreferenceMemory(t *testing.T) {
	mem, conn := newAgentMemoryTestService(t)
	provider := &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"preference","content":"回答默认先给结论并保持简洁","confidence":0.92,"reason":"用户在多轮对话中反复强调简洁和先给结论"}`}
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		q:                 db.New(conn),
		summarizeProvider: provider,
	}

	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "先帮我看看这个问题。"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "我先检查一下。"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "别铺垫太多，直接说结论。"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "明白。"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "之后也尽量短一点。"}}},
	}

	a.persistConversationPreferenceMemory(context.Background(), "session-conversation-pref", history)
	a.persistConversationPreferenceMemory(context.Background(), "session-conversation-pref", history)

	memories, err := mem.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}

	found := false
	for _, item := range memories {
		if item.Content == "回答默认先给结论并保持简洁" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected conversation preference memory to persist, got %+v", memories)
	}
	if provider.calls != 1 {
		t.Fatalf("expected conversation preference inference to run once for the same window, got %d", provider.calls)
	}
}

func TestPersistConversationPreferenceMemoryRetriesAfterTransientDetectionFailure(t *testing.T) {
	mem, conn := newAgentMemoryTestService(t)
	provider := &stableMemoryFallbackProvider{responses: []string{
		"not-json",
		`{"should_remember":true,"category":"preference","content":"回答默认先给结论并保持简洁","confidence":0.92,"reason":"用户在多轮对话中反复强调简洁和先给结论"}`,
	}}
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		q:                 db.New(conn),
		summarizeProvider: provider,
	}

	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "先帮我看看这个问题。"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "我先检查一下。"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "别铺垫太多，直接说结论。"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "明白。"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "之后也尽量短一点。"}}},
	}

	a.persistConversationPreferenceMemory(context.Background(), "session-conversation-pref-retry", history)
	a.persistConversationPreferenceMemory(context.Background(), "session-conversation-pref-retry", history)

	memories, err := mem.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}

	found := false
	for _, item := range memories {
		if item.Content == "回答默认先给结论并保持简洁" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected conversation preference memory to persist after retry, got %+v", memories)
	}
	if provider.calls != 2 {
		t.Fatalf("expected transient detection failure to be retried for the same window, got %d calls", provider.calls)
	}
}

func TestPersistProjectSummaryMemoryStoresInstruction(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		summarizeProvider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"instruction","content":"默认工作流: 先读日志再改代码","confidence":0.94,"reason":"跨任务可复用的项目工作流"}`},
	}

	a.persistProjectSummaryMemory("session-project-memory", "修复后端问题", "本轮任务先查看日志再修改代码，避免误判根因；这个流程在该项目里多次适用。")

	memories, err := mem.ListByCategory(context.Background(), "instruction")
	if err != nil {
		t.Fatalf("list instruction memories: %v", err)
	}

	found := false
	for _, item := range memories {
		if item.Content == "默认工作流: 先读日志再改代码" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected project instruction memory to persist, got %+v", memories)
	}
}

func TestPersistProjectSummaryMemorySkipsLowConfidence(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		summarizeProvider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"experience","content":"记忆去重经验: 手动去重前先 dry-run","confidence":0.55,"reason":"置信度不足"}`},
	}

	a.persistProjectSummaryMemory("session-project-low-confidence", "记忆去重复盘", "本轮任务分析了手动去重风险，先 dry-run 更安全。")

	memories, err := mem.ListByCategory(context.Background(), "experience")
	if err != nil {
		t.Fatalf("list experience memories: %v", err)
	}
	for _, item := range memories {
		if item.Content == "记忆去重经验: 手动去重前先 dry-run" {
			t.Fatalf("did not expect low-confidence project memory to persist")
		}
	}
}

func TestPersistProjectSummaryMemorySkipsInternalSession(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		summarizeProvider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"fact","content":"后端开发服务: 在 backend 目录使用 air 启动","confidence":0.96,"reason":"稳定项目事实"}`},
	}

	a.persistProjectSummaryMemory("aura-project-internal", "内部调度", "内部摘要提到 backend 目录使用 air 启动。")

	memories, err := mem.ListByCategory(context.Background(), "fact")
	if err != nil {
		t.Fatalf("list fact memories: %v", err)
	}
	for _, item := range memories {
		if item.Content == "后端开发服务: 在 backend 目录使用 air 启动" {
			t.Fatalf("did not expect internal session to persist project summary memory")
		}
	}
}

func TestPersistProjectSummaryMemorySkipsTaskSnapshotContent(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:            pubsub.NewBroker[AgentEvent](),
		messages:          newInMemoryMessageService(),
		memories:          mem,
		summarizeProvider: &stableMemoryFallbackProvider{response: `{"should_remember":true,"category":"instruction","content":"当前进度: 已完成后端优化，下一步补测试","confidence":0.97,"reason":"项目推进情况"}`},
	}

	a.persistProjectSummaryMemory("session-project-tasklike", "记忆系统优化", "本轮任务主要是优化记忆系统并补测试。")

	memories, err := mem.ListByCategory(context.Background(), "instruction")
	if err != nil {
		t.Fatalf("list instruction memories: %v", err)
	}
	for _, item := range memories {
		if item.Content == "当前进度: 已完成后端优化，下一步补测试" {
			t.Fatalf("did not expect task snapshot content to persist as durable memory")
		}
	}
}

func TestDetectLatestStableMemoryCapture(t *testing.T) {
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "帮我看下这个错误"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "好的"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "以后默认这样做，先读后写。"}}},
	}

	capture, ok := detectLatestStableMemoryCapture(history)
	if !ok || capture == nil {
		t.Fatalf("expected latest stable memory capture")
	}
	if capture.Category != "instruction" {
		t.Fatalf("expected instruction category, got %q", capture.Category)
	}
}

func TestUpdateTaskStateFromToolResultsKeepsTaskFactSessionLocal(t *testing.T) {
	mem, conn := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
		q:        db.New(conn),
	}

	toolResults := &message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{
		Name:     "read",
		Metadata: "backend/config.yaml",
		Content:  "endpoint: https://api.example.com/v1\n",
		IsError:  false,
	}}}

	if err := a.updateTaskStateFromToolResults(context.Background(), "session-task-fact", toolResults); err != nil {
		t.Fatalf("update task state from tool results: %v", err)
	}

	state, err := a.loadTaskState(context.Background(), "session-task-fact")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if state == nil {
		t.Fatalf("expected task state to be persisted")
	}

	foundFact := false
	for _, fact := range state.StructuredFacts {
		if fact.Artifact == "backend/config.yaml" && fact.Marker == "endpoint" && fact.CurrentValue == "https://api.example.com/v1" {
			foundFact = true
			break
		}
	}
	if !foundFact {
		t.Fatalf("expected task fact to remain in session task state, got %+v", state.StructuredFacts)
	}

	memories, err := mem.ListByCategory(context.Background(), "fact")
	if err != nil {
		t.Fatalf("list fact memories: %v", err)
	}

	for _, item := range memories {
		if item.Content == "backend/config.yaml 中 endpoint = https://api.example.com/v1" {
			t.Fatalf("did not expect task fact to persist as long-term memory, got %+v", memories)
		}
	}
}

func TestUpdateTaskStateFromToolResultsKeepsTaskErrorExperienceSessionLocal(t *testing.T) {
	mem, conn := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
		q:        db.New(conn),
	}

	toolResults := &message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{
		Name:    "read",
		Content: "Error: File not found: C:/workspace/memory_missing_case.txt",
		IsError: true,
	}}}

	if err := a.updateTaskStateFromToolResults(context.Background(), "session-task-error", toolResults); err != nil {
		t.Fatalf("update task state from tool results: %v", err)
	}

	state, err := a.loadTaskState(context.Background(), "session-task-error")
	if err != nil {
		t.Fatalf("load task state: %v", err)
	}
	if state == nil {
		t.Fatalf("expected task state to be persisted")
	}
	if state.LastToolStatus != "error" {
		t.Fatalf("expected session task state to retain error status, got %+v", state)
	}
	foundIssue := false
	for _, issue := range state.OutstandingIssues {
		if strings.Contains(issue, "memory_missing_case.txt") {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Fatalf("expected task error to remain in session task state, got %+v", state.OutstandingIssues)
	}

	memories, err := mem.ListByCategory(context.Background(), "experience")
	if err != nil {
		t.Fatalf("list experience memories: %v", err)
	}

	for _, item := range memories {
		if strings.Contains(item.Content, "memory_missing_case.txt") && strings.Contains(item.Content, "不存在") {
			t.Fatalf("did not expect task error experience to persist as long-term memory, got %+v", memories)
		}
	}
}

func TestReflectOnSessionSkipsDurableReflectionMemory(t *testing.T) {
	mem, _ := newAgentMemoryTestService(t)
	a := &agent{
		Broker:   pubsub.NewBroker[AgentEvent](),
		messages: newInMemoryMessageService(),
		memories: mem,
		provider: &stableMemoryFallbackProvider{err: errors.New("reflection provider unavailable")},
	}

	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "读取这个文件"}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{
			Name:    "read",
			Content: "Error: File not found: C:/workspace/memory_missing_case.txt",
			IsError: true,
		}}},
	}

	a.reflectOnSession(context.Background(), "session-reflection-session-local", history)

	memories, err := mem.ListByCategory(context.Background(), "experience")
	if err != nil {
		t.Fatalf("list experience memories: %v", err)
	}
	for _, item := range memories {
		if strings.Contains(item.Content, "memory_missing_case.txt") && strings.Contains(item.Content, "不存在") {
			t.Fatalf("did not expect completion reflection to persist as long-term memory, got %+v", memories)
		}
	}
}
