package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/auramate/auramate/internal/db"
)

type memoryEvalSuite struct {
	WriteCases  []memoryWriteEvalCase  `json:"write_cases"`
	SearchCases []memorySearchEvalCase `json:"search_cases"`
}

type memoryEvalSeed struct {
	ID             string `json:"id"`
	Layer          int64  `json:"layer"`
	Category       string `json:"category"`
	Content        string `json:"content"`
	Source         string `json:"source"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	LastAccessedAt int64  `json:"last_accessed_at"`
}

type memoryWriteEvalCase struct {
	Name     string                 `json:"name"`
	Seed     []memoryEvalSeed       `json:"seed"`
	Request  memoryWriteEvalRequest `json:"request"`
	Expected memoryWriteEvalExpect  `json:"expected"`
}

type memoryWriteEvalRequest struct {
	Category           string   `json:"category"`
	Content            string   `json:"content"`
	Source             string   `json:"source"`
	Trigger            string   `json:"trigger"`
	Evidence           []string `json:"evidence"`
	ConflictKey        string   `json:"conflict_key"`
	AllowOverwrite     bool     `json:"allow_overwrite"`
	AllowSemanticDedup bool     `json:"allow_semantic_dedup"`
}

type memoryWriteEvalExpect struct {
	Action           MemoryWriteAction       `json:"action"`
	MemoryID         string                  `json:"memory_id"`
	ReasonCodes      []MemoryWriteReasonCode `json:"reason_codes"`
	FinalMemoryCount int                     `json:"final_memory_count"`
	TotalMemoryCount int                     `json:"total_memory_count"`
	FinalContent     string                  `json:"final_content"`
	FinalSource      string                  `json:"final_source"`
}

type memorySearchEvalCase struct {
	Name     string                  `json:"name"`
	Seed     []memoryEvalSeed        `json:"seed"`
	Request  memorySearchEvalRequest `json:"request"`
	Expected memorySearchEvalExpect  `json:"expected"`
}

type memorySearchEvalRequest struct {
	Mode                string `json:"mode"`
	Query               string `json:"query"`
	Category            string `json:"category"`
	Stage               string `json:"stage"`
	Limit               int    `json:"limit"`
	MaxFTSCandidates    *int   `json:"max_fts_candidates,omitempty"`
	MaxVectorCandidates *int   `json:"max_vector_candidates,omitempty"`
}

type memorySearchEvalExpect struct {
	ResultIDs         []string                 `json:"result_ids"`
	ResultCount       int                      `json:"result_count"`
	NormalizedLimit   int                      `json:"normalized_limit"`
	FTSCandidateLimit int                      `json:"fts_candidate_limit"`
	VecCandidateLimit int                      `json:"vector_candidate_limit"`
	FallbackPath      string                   `json:"fallback_path"`
	UsedChannels      []string                 `json:"used_channels"`
	ReasonCodes       []MemorySearchReasonCode `json:"reason_codes"`
}

func loadMemoryEvalSuite(t *testing.T) memoryEvalSuite {
	t.Helper()
	path := filepath.Join("testdata", "policy_eval_cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read eval suite: %v", err)
	}
	var suite memoryEvalSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatalf("unmarshal eval suite: %v", err)
	}
	return suite
}

func seedEvalCase(t *testing.T, q *db.Queries, seeds []memoryEvalSeed) {
	t.Helper()
	for _, seed := range seeds {
		seedKnowledgeNode(t, q, seed.ID, seed.Layer, seed.Category, seed.Content, seed.Source, seed.CreatedAt, seed.UpdatedAt, seed.LastAccessedAt)
	}
}

func requireWriteReasonsSubset(t *testing.T, got []MemoryWriteReasonCode, want []MemoryWriteReasonCode) {
	t.Helper()
	for _, expected := range want {
		found := false
		for _, actual := range got {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected write reason %s, got %v", expected, got)
		}
	}
}

func requireSearchReasonsSubset(t *testing.T, got []MemorySearchReasonCode, want []MemorySearchReasonCode) {
	t.Helper()
	for _, expected := range want {
		found := false
		for _, actual := range got {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected search reason %s, got %v", expected, got)
		}
	}
}

func equalStringSliceNormalized(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestMemoryPolicyEvaluationSuite(t *testing.T) {
	suite := loadMemoryEvalSuite(t)
	for _, tc := range suite.WriteCases {
		t.Run(tc.Name, func(t *testing.T) {
			q, conn := newMemoryTestDB(t)
			seedEvalCase(t, q, tc.Seed)
			svc := NewService(q, conn, nil)

			result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
				Category:           tc.Request.Category,
				Content:            tc.Request.Content,
				Source:             tc.Request.Source,
				Trigger:            tc.Request.Trigger,
				Evidence:           tc.Request.Evidence,
				ConflictKey:        tc.Request.ConflictKey,
				AllowOverwrite:     tc.Request.AllowOverwrite,
				AllowSemanticDedup: tc.Request.AllowSemanticDedup,
			})
			if err != nil {
				t.Fatalf("add with policy: %v", err)
			}
			if result.Decision.Action != tc.Expected.Action {
				t.Fatalf("expected action %s, got %s", tc.Expected.Action, result.Decision.Action)
			}
			if result.Memory == nil {
				t.Fatalf("expected memory result")
			}
			finalMemoryID := result.Memory.ID
			if tc.Expected.MemoryID != "" && result.Memory.ID != tc.Expected.MemoryID {
				t.Fatalf("expected memory id %s, got %s", tc.Expected.MemoryID, result.Memory.ID)
			}
			if tc.Expected.MemoryID != "" {
				finalMemoryID = tc.Expected.MemoryID
			}
			requireWriteReasonsSubset(t, result.Decision.ReasonCodes, tc.Expected.ReasonCodes)

			memories, err := svc.ListByCategory(context.Background(), tc.Request.Category)
			if err != nil {
				t.Fatalf("list by category: %v", err)
			}
			if len(memories) != tc.Expected.FinalMemoryCount {
				t.Fatalf("expected final memory count %d, got %d", tc.Expected.FinalMemoryCount, len(memories))
			}
			if tc.Expected.TotalMemoryCount > 0 {
				allMemories, err := svc.List(context.Background())
				if err != nil {
					t.Fatalf("list all memories: %v", err)
				}
				if len(allMemories) != tc.Expected.TotalMemoryCount {
					t.Fatalf("expected total memory count %d, got %d", tc.Expected.TotalMemoryCount, len(allMemories))
				}
			}
			current, err := svc.Get(context.Background(), finalMemoryID)
			if err != nil {
				t.Fatalf("get final memory: %v", err)
			}
			if current.Content != tc.Expected.FinalContent {
				t.Fatalf("expected final content %q, got %q", tc.Expected.FinalContent, current.Content)
			}
			if current.Source.String != tc.Expected.FinalSource {
				t.Fatalf("expected final source %q, got %q", tc.Expected.FinalSource, current.Source.String)
			}
		})
	}
}

func TestMemorySearchEvaluationSuite(t *testing.T) {
	suite := loadMemoryEvalSuite(t)
	for _, tc := range suite.SearchCases {
		t.Run(tc.Name, func(t *testing.T) {
			q, conn := newMemoryTestDB(t)
			seedEvalCase(t, q, tc.Seed)
			svc := NewService(q, conn, nil)

			var (
				result *MemorySearchResult
				err    error
			)
			switch tc.Request.Mode {
			case "category":
				req := DefaultCategorySearchRequest(tc.Request.Query, tc.Request.Category, tc.Request.Limit)
				req.Stage = tc.Request.Stage
				if tc.Request.MaxFTSCandidates != nil {
					req.MaxFTSCandidates = *tc.Request.MaxFTSCandidates
				}
				if tc.Request.MaxVectorCandidates != nil {
					req.MaxVectorCandidates = *tc.Request.MaxVectorCandidates
				}
				result, err = svc.SearchByCategoryWithPolicy(context.Background(), req)
			case "global":
				req := DefaultSearchRequest(tc.Request.Query, tc.Request.Limit)
				req.Stage = tc.Request.Stage
				if tc.Request.MaxFTSCandidates != nil {
					req.MaxFTSCandidates = *tc.Request.MaxFTSCandidates
				}
				if tc.Request.MaxVectorCandidates != nil {
					req.MaxVectorCandidates = *tc.Request.MaxVectorCandidates
				}
				result, err = svc.SearchWithPolicy(context.Background(), req)
			default:
				t.Fatalf("unsupported search mode %q", tc.Request.Mode)
			}
			if err != nil {
				t.Fatalf("search with policy: %v", err)
			}

			gotIDs := make([]string, 0, len(result.Memories))
			for _, memory := range result.Memories {
				gotIDs = append(gotIDs, memory.ID)
			}
			if len(tc.Expected.ResultIDs) > 0 {
				if len(gotIDs) < len(tc.Expected.ResultIDs) {
					t.Fatalf("expected at least %d result ids, got %v", len(tc.Expected.ResultIDs), gotIDs)
				}
				if !reflect.DeepEqual(gotIDs[:len(tc.Expected.ResultIDs)], tc.Expected.ResultIDs) {
					t.Fatalf("expected result ids prefix %v, got %v", tc.Expected.ResultIDs, gotIDs)
				}
			}
			if tc.Expected.ResultCount > 0 && len(result.Memories) != tc.Expected.ResultCount {
				t.Fatalf("expected result count %d, got %d", tc.Expected.ResultCount, len(result.Memories))
			}
			if tc.Expected.NormalizedLimit > 0 && result.Decision.Limit != tc.Expected.NormalizedLimit {
				t.Fatalf("expected normalized limit %d, got %d", tc.Expected.NormalizedLimit, result.Decision.Limit)
			}
			if tc.Expected.FTSCandidateLimit > 0 && result.Decision.FTSCandidateLimit != tc.Expected.FTSCandidateLimit {
				t.Fatalf("expected fts candidate limit %d, got %d", tc.Expected.FTSCandidateLimit, result.Decision.FTSCandidateLimit)
			}
			if tc.Expected.VecCandidateLimit > 0 && result.Decision.VecCandidateLimit != tc.Expected.VecCandidateLimit {
				t.Fatalf("expected vector candidate limit %d, got %d", tc.Expected.VecCandidateLimit, result.Decision.VecCandidateLimit)
			}
			if result.Decision.FallbackPath != tc.Expected.FallbackPath {
				t.Fatalf("expected fallback path %q, got %q", tc.Expected.FallbackPath, result.Decision.FallbackPath)
			}
			if !equalStringSliceNormalized(result.Decision.UsedChannels, tc.Expected.UsedChannels) {
				t.Fatalf("expected used channels %v, got %v", tc.Expected.UsedChannels, result.Decision.UsedChannels)
			}
			requireSearchReasonsSubset(t, result.Decision.ReasonCodes, tc.Expected.ReasonCodes)
		})
	}
}
