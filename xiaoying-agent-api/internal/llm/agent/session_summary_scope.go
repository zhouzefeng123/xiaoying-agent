package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/session"
)

const milestoneSummaryStateKey = "agent_milestone_summary"

func normalizeSessionSummaryText(content string) string {
	if checkpoint, ok := parseSummarySessionCheckpoint(content); ok && checkpoint != nil && strings.TrimSpace(checkpoint.Summary) != "" {
		return strings.TrimSpace(checkpoint.Summary)
	}
	content = stripSummaryCheckpointBlocks(content)
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "[System Summary]:")
	return strings.TrimSpace(content)
}

func checkpointSessionID(sessionCheckpoint *summarySessionCheckpoint, taskCheckpoint *summaryTaskCheckpoint) string {
	if sessionCheckpoint != nil && strings.TrimSpace(sessionCheckpoint.SessionID) != "" {
		return strings.TrimSpace(sessionCheckpoint.SessionID)
	}
	if taskCheckpoint != nil {
		return strings.TrimSpace(taskCheckpoint.SessionID)
	}
	return ""
}

func checkpointSessionTitle(sessionCheckpoint *summarySessionCheckpoint) string {
	if sessionCheckpoint == nil {
		return ""
	}
	return strings.TrimSpace(sessionCheckpoint.SessionTitle)
}

func buildSessionSummaryCheckpointMemoryContent(sessionID, sessionTitle, summaryText string, _ *summaryTaskCheckpoint) string {
	return buildSessionSummaryMemoryContent(sessionID, sessionTitle, summaryText)
}

func buildSessionSummaryPseudoMemory(sess session.Session, summaryMsg message.Message) (db.Memory, bool) {
	sessionCheckpoint, _ := parseSummarySessionCheckpoint(summaryMsg.Content().String())
	taskCheckpoint, _ := parseSummaryTaskCheckpoint(summaryMsg.Content().String())
	summaryText := normalizeSessionSummaryText(summaryMsg.Content().String())
	if summaryText == "" {
		return db.Memory{}, false
	}

	updatedAt := summaryMsg.UpdatedAt
	if updatedAt == 0 {
		updatedAt = summaryMsg.CreatedAt
	}
	if updatedAt == 0 {
		updatedAt = sess.UpdatedAt
	}
	if updatedAt == 0 {
		updatedAt = sess.CreatedAt
	}
	if updatedAt == 0 {
		updatedAt = time.Now().UnixMilli()
	}

	createdAt := sess.CreatedAt
	if createdAt == 0 {
		createdAt = updatedAt
	}

	return db.Memory{
		ID:             fmt.Sprintf("session-summary:%s", sess.ID),
		Category:       "session_summary",
		Content:        buildSessionSummaryCheckpointMemoryContent(firstNonEmpty(sess.ID, checkpointSessionID(sessionCheckpoint, taskCheckpoint)), firstNonEmpty(sess.Title, checkpointSessionTitle(sessionCheckpoint)), summaryText, taskCheckpoint),
		Source:         sql.NullString{String: "SessionSummary", Valid: true},
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		Confidence:     sql.NullFloat64{Float64: 1, Valid: true},
		LastAccessedAt: sql.NullInt64{Int64: updatedAt, Valid: true},
	}, true
}

func buildMilestoneSummaryMemory(summaryText string, updatedAt int64) db.Memory {
	summaryText = strings.TrimSpace(summaryText)
	if updatedAt == 0 {
		updatedAt = time.Now().UnixMilli()
	}
	return db.Memory{
		ID:             milestoneSummaryStateKey,
		Category:       "session_summary",
		Content:        fmt.Sprintf("[Milestone Summary]: %s", summaryText),
		Source:         sql.NullString{String: "TreeSummarizer", Valid: true},
		CreatedAt:      updatedAt,
		UpdatedAt:      updatedAt,
		Confidence:     sql.NullFloat64{Float64: 0.95, Valid: true},
		LastAccessedAt: sql.NullInt64{Int64: updatedAt, Valid: true},
	}
}

func (a *agent) continuitySessionIDs(ctx context.Context, sessionID string) map[string]struct{} {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	allowed := map[string]struct{}{sessionID: {}}
	if a.sessions == nil {
		return allowed
	}
	currentID := sessionID
	for depth := 0; depth < 8; depth++ {
		sess, err := a.sessions.Get(ctx, currentID)
		if err != nil {
			break
		}
		parentID := strings.TrimSpace(sess.ParentSessionID)
		if parentID == "" {
			break
		}
		if _, exists := allowed[parentID]; exists {
			break
		}
		allowed[parentID] = struct{}{}
		currentID = parentID
	}
	return allowed
}

func (a *agent) listRecentSessionSummaryMemories(ctx context.Context, sessionID string, limit int) ([]db.Memory, error) {
	if a.sessions == nil || a.messages == nil {
		return nil, nil
	}

	sessions, err := a.sessions.List(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make([]db.Memory, 0, len(sessions))
	allowedSessionIDs := a.continuitySessionIDs(ctx, sessionID)
	for _, sess := range sessions {
		if len(allowedSessionIDs) > 0 {
			if _, ok := allowedSessionIDs[sess.ID]; !ok {
				continue
			}
		}
		if strings.TrimSpace(sess.SummaryMessageID) == "" {
			continue
		}
		if isInternalMemoryCaptureSession(sess.ID) {
			continue
		}
		if strings.TrimSpace(sess.ParentSessionID) != "" && len(allowedSessionIDs) == 0 {
			continue
		}

		summaryMsg, err := a.messages.Get(ctx, sess.SummaryMessageID)
		if err != nil {
			continue
		}

		pseudo, ok := buildSessionSummaryPseudoMemory(sess, summaryMsg)
		if ok {
			summaries = append(summaries, pseudo)
		}
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt != summaries[j].UpdatedAt {
			return summaries[i].UpdatedAt > summaries[j].UpdatedAt
		}
		return summaries[i].ID < summaries[j].ID
	})

	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	return summaries, nil
}

func (a *agent) loadMilestoneSummaryMemory(ctx context.Context) (db.Memory, bool) {
	if a.q == nil {
		return db.Memory{}, false
	}

	row, err := a.q.GetCognitiveState(ctx, milestoneSummaryStateKey)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logging.Warn("Failed to load milestone summary state", "error", err)
		}
		return db.Memory{}, false
	}
	if !row.Value.Valid || strings.TrimSpace(row.Value.String) == "" {
		return db.Memory{}, false
	}

	return buildMilestoneSummaryMemory(row.Value.String, row.UpdatedAt), true
}

func (a *agent) saveMilestoneSummary(ctx context.Context, summaryText string) error {
	if a.q == nil {
		return nil
	}

	summaryText = strings.TrimSpace(summaryText)
	if summaryText == "" {
		return nil
	}

	_, err := a.q.SetCognitiveState(ctx, db.SetCognitiveStateParams{
		Key:       milestoneSummaryStateKey,
		Value:     sql.NullString{String: summaryText, Valid: true},
		UpdatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return err
	}

	a.invalidateMemoryRetrievalCache()
	return nil
}

func (a *agent) listContinuityMemories(ctx context.Context, sessionID string, limit int) ([]db.Memory, error) {
	recent, err := a.listRecentSessionSummaryMemories(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}

	continuity := make([]db.Memory, 0, len(recent)+1)
	if strings.TrimSpace(sessionID) == "" {
		if milestone, ok := a.loadMilestoneSummaryMemory(ctx); ok {
			continuity = append(continuity, milestone)
		}
	}
	continuity = append(continuity, recent...)

	if limit > 0 && len(continuity) > limit {
		continuity = continuity[:limit]
	}

	return continuity, nil
}
