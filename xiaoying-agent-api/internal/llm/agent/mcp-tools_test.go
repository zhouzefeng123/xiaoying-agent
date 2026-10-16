package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/mcp"
	mcp_protocol "github.com/mark3labs/mcp-go/mcp"
)

type fakeMCPService struct {
	getToolsCalls int
	getTools      func(ctx context.Context) (map[string][]mcp_protocol.Tool, error)
}

func (f *fakeMCPService) GetClient(ctx context.Context, name string) (mcp.Client, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) CloseAll() {}

func (f *fakeMCPService) GetTools(ctx context.Context) (map[string][]mcp_protocol.Tool, error) {
	f.getToolsCalls++
	return f.getTools(ctx)
}

func resetMcpToolsCacheForTest() {
	mcpToolsCacheMu.Lock()
	defer mcpToolsCacheMu.Unlock()
	mcpToolsCache = nil
	mcpToolsCacheAt = time.Time{}
}

func TestGetMcpToolsCachesRecentDiscovery(t *testing.T) {
	resetMcpToolsCacheForTest()
	originalTTL := mcpToolsCacheTTL
	defer func() {
		mcpToolsCacheTTL = originalTTL
		resetMcpToolsCacheForTest()
	}()
	mcpToolsCacheTTL = time.Minute

	svc := &fakeMCPService{
		getTools: func(ctx context.Context) (map[string][]mcp_protocol.Tool, error) {
			return map[string][]mcp_protocol.Tool{
				"demo": {
					{Name: "ping", Description: "ping tool"},
				},
			}, nil
		},
	}

	first := GetMcpTools(context.Background(), nil, svc)
	second := GetMcpTools(context.Background(), nil, svc)

	if svc.getToolsCalls != 1 {
		t.Fatalf("expected one MCP discovery call, got %d", svc.getToolsCalls)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected cached MCP tools to be returned, got %d and %d", len(first), len(second))
	}
}

func TestGetMcpToolsAppliesStartupTimeout(t *testing.T) {
	resetMcpToolsCacheForTest()
	originalTimeout := mcpToolsDiscoveryTimeout
	originalTTL := mcpToolsCacheTTL
	defer func() {
		mcpToolsDiscoveryTimeout = originalTimeout
		mcpToolsCacheTTL = originalTTL
		resetMcpToolsCacheForTest()
	}()
	mcpToolsDiscoveryTimeout = 20 * time.Millisecond
	mcpToolsCacheTTL = 10 * time.Millisecond

	svc := &fakeMCPService{
		getTools: func(ctx context.Context) (map[string][]mcp_protocol.Tool, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	start := time.Now()
	tools := GetMcpTools(context.Background(), nil, svc)
	duration := time.Since(start)

	if len(tools) != 0 {
		t.Fatalf("expected no MCP tools on timeout, got %d", len(tools))
	}
	if duration > 250*time.Millisecond {
		t.Fatalf("expected startup MCP discovery to timeout quickly, took %s", duration)
	}
}

func TestGetMcpToolsRegistersReadableAliases(t *testing.T) {
	resetMcpToolsCacheForTest()
	defer resetMcpToolsCacheForTest()

	svc := &fakeMCPService{
		getTools: func(ctx context.Context) (map[string][]mcp_protocol.Tool, error) {
			return map[string][]mcp_protocol.Tool{
				"local-sse-8012": {
					{Name: "search_customers", Description: "搜索CRM客户列表，支持限制返回条数"},
				},
			}, nil
		},
	}

	got := GetMcpTools(context.Background(), nil, svc)
	names := make(map[string]struct{}, len(got))
	for _, tool := range got {
		names[tool.Info().Name] = struct{}{}
	}

	for _, want := range []string{"local-sse-8012_search_customers", "search_customers", "crm_search_customers"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("expected MCP tool alias %q in %v", want, names)
		}
	}
}

func TestDeriveMCPFocusedToolAllowlistMatchesCRMCustomerRequest(t *testing.T) {
	primary := NewMcpTool("local-sse-8012", mcp_protocol.Tool{
		Name:        "search_customers",
		Description: "搜索CRM客户列表，支持限制返回条数",
	}, nil, nil).(*mcpTool)

	agentUnderTest := &agent{
		allTools: map[string]llmtools.BaseTool{
			"read":                            staticTestTool{name: "read"},
			"local-sse-8012_search_customers": primary,
			"search_customers":                newMCPAliasTool("search_customers", primary),
			"crm_search_customers":            newMCPAliasTool("crm_search_customers", primary),
		},
		activeTools: map[string]bool{
			"read":                            true,
			"local-sse-8012_search_customers": true,
			"search_customers":                true,
			"crm_search_customers":            true,
		},
	}

	allowlist := agentUnderTest.deriveMCPFocusedToolAllowlist("查询 CRM 客户，返回前三条真实数据")
	for _, want := range []string{"local-sse-8012_search_customers", "search_customers", "crm_search_customers", llmtools.ManageToolsToolName, llmtools.ManageMcpToolName} {
		if _, ok := allowlist[want]; !ok {
			t.Fatalf("expected allowlist to include %q, got %v", want, allowlist)
		}
	}
	if _, ok := allowlist["read"]; ok {
		t.Fatalf("expected unrelated read tool to be excluded, got %v", allowlist)
	}
}

func TestIsMCPSessionNotFoundError(t *testing.T) {
	if !isMCPSessionNotFoundError(errors.New("request failed with status 404: Could not find session")) {
		t.Fatal("expected session-not-found MCP error to be detected")
	}
	if isMCPSessionNotFoundError(errors.New("permission denied")) {
		t.Fatal("did not expect unrelated error to be detected")
	}
}

type staticTestTool struct {
	name string
}

func (t staticTestTool) Info() llmtools.ToolInfo {
	return llmtools.ToolInfo{Name: t.name, Description: "test tool"}
}

func (t staticTestTool) Run(ctx context.Context, call llmtools.ToolCall) (llmtools.ToolResponse, error) {
	return llmtools.NewTextResponse("ok"), nil
}
