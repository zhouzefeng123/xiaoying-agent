package message

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/google/uuid"
)

type CreateMessageParams struct {
	Role  MessageRole
	Parts []ContentPart
	Model models.ModelID
}

type Service interface {
	pubsub.Suscriber[Message]
	Create(ctx context.Context, sessionID string, params CreateMessageParams) (Message, error)
	Update(ctx context.Context, message Message) error
	Get(ctx context.Context, id string) (Message, error)
	List(ctx context.Context, sessionID string) ([]Message, error)
	ListWindow(ctx context.Context, sessionID string, summaryMessageID string, limit int) ([]Message, error)
	Delete(ctx context.Context, id string) error
	DeleteSessionMessages(ctx context.Context, sessionID string) error
	Search(ctx context.Context, query string, limit int) ([]Message, error)
	SearchInSession(ctx context.Context, sessionID string, query string, limit int) ([]Message, error)
}

type service struct {
	*pubsub.Broker[Message]
	q        db.Querier
	embedder embedding.Embedder
}

func NewService(q db.Querier, embedder embedding.Embedder) Service {
	return &service{
		Broker:   pubsub.NewBroker[Message](),
		q:        q,
		embedder: embedder,
	}
}

func (s *service) Delete(ctx context.Context, id string) error {
	message, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	err = s.q.DeleteMessage(ctx, message.ID)
	if err != nil {
		return err
	}
	s.Publish(pubsub.DeletedEvent, message)
	return nil
}

type ScoredMessage struct {
	Message Message
	Score   float32
}

func (s *service) Search(ctx context.Context, query string, limit int) ([]Message, error) {
	return s.search(ctx, query, limit, "")
}

func (s *service) SearchInSession(ctx context.Context, sessionID string, query string, limit int) ([]Message, error) {
	return s.search(ctx, query, limit, sessionID)
}

func (s *service) search(ctx context.Context, query string, limit int, sessionID string) ([]Message, error) {
	if s.embedder == nil {
		return nil, nil
	}

	// 1. Generate query embedding
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	queryVec := embeddings[0]

	// 2. Fetch candidate messages with embeddings
	var messages []db.Message
	if sessionID != "" {
		messages, err = s.q.ListSessionMessagesWithEmbedding(ctx, sessionID)
	} else {
		messages, err = s.q.ListUserMessagesWithEmbedding(ctx)
	}
	if err != nil {
		return nil, err
	}

	// 3. Calculate similarity
	var scored []ScoredMessage
	for _, m := range messages {
		if !m.Embedding.Valid {
			continue
		}
		var vec []float32
		if err := json.Unmarshal([]byte(m.Embedding.String), &vec); err != nil {
			continue
		}
		score := cosineSimilarity(queryVec, vec)
		// Convert db.Message to Message
		msg, err := s.fromDBItem(m)
		if err != nil {
			continue
		}
		scored = append(scored, ScoredMessage{Message: msg, Score: score})
	}

	// 4. Sort and limit
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	result := make([]Message, 0, limit)
	for i := 0; i < len(scored) && i < limit; i++ {
		// Only return relevant results (e.g. score > 0.3)
		if scored[i].Score > 0.3 {
			result = append(result, scored[i].Message)
		}
	}
	return result, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

func ExtractTextForEmbedding(parts []ContentPart) string {
	var sb strings.Builder
	for _, part := range parts {
		switch current := part.(type) {
		case TextContent:
			sb.WriteString(current.Text)
			sb.WriteString(" ")
		case ToolResult:
			content := current.Content
			if len(content) > 1000 {
				content = content[:1000]
			}
			sb.WriteString(content)
			sb.WriteString(" ")
		case ReasoningContent:
			sb.WriteString(current.Thinking)
			sb.WriteString(" ")
		}
	}
	return strings.TrimSpace(sb.String())
}

func (s *service) Create(ctx context.Context, sessionID string, params CreateMessageParams) (Message, error) {
	if params.Role != Assistant {
		params.Parts = append(params.Parts, Finish{
			Reason: "stop",
		})
	}
	partsJSON, err := marshallParts(params.Parts)
	if err != nil {
		return Message{}, err
	}

	var embeddingJSON sql.NullString
	// OPTIMIZATION: Generate embeddings for ALL relevant roles (User, Assistant, Tool)
	// This enables RAG to retrieve previous AI responses (e.g. code snippets) and Tool outputs (e.g. file contents).
	if s.embedder != nil {
		// Extract text content for embedding
		textContent := ExtractTextForEmbedding(params.Parts)

		if textContent != "" {
			// Limit text length to avoid token limit errors in embedder (usually ~8k tokens)
			if len(textContent) > 8000 {
				textContent = textContent[:8000]
			}

			embeddings, err := s.embedder.Embed(ctx, []string{textContent})
			if err == nil && len(embeddings) > 0 {
				b, _ := json.Marshal(embeddings[0])
				embeddingJSON = sql.NullString{String: string(b), Valid: true}
			} else if err != nil {
				logging.Error("Failed to generate embedding for message", "error", err)
			}
		}
	}

	dbMessage, err := s.q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      string(params.Role),
		Parts:     string(partsJSON),
		Model:     sql.NullString{String: string(params.Model), Valid: true},
		Embedding: embeddingJSON,
	})
	if err != nil {
		return Message{}, err
	}
	message, err := s.fromDBItem(dbMessage)
	if err != nil {
		return Message{}, err
	}
	s.Publish(pubsub.CreatedEvent, message)
	return message, nil
}

func (s *service) DeleteSessionMessages(ctx context.Context, sessionID string) error {
	messages, err := s.List(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.SessionID == sessionID {
			err = s.Delete(ctx, message.ID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *service) Update(ctx context.Context, message Message) error {
	parts, err := marshallParts(message.Parts)
	if err != nil {
		return err
	}
	finishedAt := sql.NullInt64{}
	if f := message.FinishPart(); f != nil {
		finishedAt.Int64 = f.Time
		finishedAt.Valid = true
	}

	// OPTIMIZATION: Update Embedding if content changed significantly?
	// Calculating embedding on every update (streaming chunks) is too expensive.
	// We should only update embedding when the message is FINISHED.
	var embeddingJSON sql.NullString
	if message.IsFinished() && s.embedder != nil {
		textContent := ExtractTextForEmbedding(message.Parts)
		if textContent != "" {
			if len(textContent) > 8000 {
				textContent = textContent[:8000]
			}
			embeddings, err := s.embedder.Embed(ctx, []string{textContent})
			if err == nil && len(embeddings) > 0 {
				b, _ := json.Marshal(embeddings[0])
				embeddingJSON = sql.NullString{String: string(b), Valid: true}
			} else if err != nil {
				logging.Error("Failed to update embedding for message", "error", err)
			}
		}
	}

	err = s.q.UpdateMessage(ctx, db.UpdateMessageParams{
		ID:         message.ID,
		Parts:      string(parts),
		FinishedAt: finishedAt,
		Embedding:  embeddingJSON, // Pass the new embedding (or null if not finished/failed)
	})
	if err != nil {
		return err
	}
	message.UpdatedAt = time.Now().Unix()
	s.Publish(pubsub.UpdatedEvent, message)
	return nil
}

func (s *service) Get(ctx context.Context, id string) (Message, error) {
	dbMessage, err := s.q.GetMessage(ctx, id)
	if err != nil {
		return Message{}, err
	}
	return s.fromDBItem(dbMessage)
}

func (s *service) List(ctx context.Context, sessionID string) ([]Message, error) {
	dbMessages, err := s.q.ListMessagesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]Message, len(dbMessages))
	for i, dbMessage := range dbMessages {
		messages[i], err = s.fromDBItem(dbMessage)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

func (s *service) ListWindow(ctx context.Context, sessionID string, summaryMessageID string, limit int) ([]Message, error) {
	if limit <= 0 {
		return s.List(ctx, sessionID)
	}

	dbRecent, err := s.q.ListRecentMessagesBySession(ctx, db.ListRecentMessagesBySessionParams{
		SessionID: sessionID,
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, err
	}

	recent := make([]Message, 0, len(dbRecent))
	for _, dbMessage := range dbRecent {
		msg, convErr := s.fromDBItem(dbMessage)
		if convErr != nil {
			return nil, convErr
		}
		recent = append(recent, msg)
	}

	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	if summaryMessageID == "" {
		return recent, nil
	}

	summaryMsg, err := s.Get(ctx, summaryMessageID)
	if err != nil {
		return recent, nil
	}

	for index, msg := range recent {
		if msg.ID == summaryMessageID {
			return recent[index:], nil
		}
	}

	if len(recent) >= limit {
		recent = recent[1:]
	}
	out := make([]Message, 0, len(recent)+1)
	summaryMsg.Role = User
	out = append(out, summaryMsg)
	out = append(out, recent...)
	return out, nil
}

func (s *service) fromDBItem(item db.Message) (Message, error) {
	parts, err := unmarshallParts([]byte(item.Parts))
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:        item.ID,
		SessionID: item.SessionID,
		Role:      MessageRole(item.Role),
		Parts:     parts,
		Model:     models.ModelID(item.Model.String),
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

type partType string

const (
	reasoningType      partType = "reasoning"
	textType           partType = "text"
	imageURLType       partType = "image_url"
	binaryType         partType = "binary"
	attachmentMetaType partType = "attachment_meta"
	toolCallType       partType = "tool_call"
	toolResultType     partType = "tool_result"
	finishType         partType = "finish"
)

type partWrapper struct {
	Type partType    `json:"type"`
	Data ContentPart `json:"data"`
}

func marshallParts(parts []ContentPart) ([]byte, error) {
	wrappedParts := make([]partWrapper, len(parts))

	for i, part := range parts {
		var typ partType

		switch part.(type) {
		case ReasoningContent:
			typ = reasoningType
		case TextContent:
			typ = textType
		case ImageURLContent:
			typ = imageURLType
		case BinaryContent:
			typ = binaryType
		case AttachmentMetaContent:
			typ = attachmentMetaType
		case ToolCall:
			typ = toolCallType
		case ToolResult:
			typ = toolResultType
		case Finish:
			typ = finishType
		default:
			return nil, fmt.Errorf("unknown part type: %T", part)
		}

		wrappedParts[i] = partWrapper{
			Type: typ,
			Data: part,
		}
	}
	return json.Marshal(wrappedParts)
}

func unmarshallParts(data []byte) ([]ContentPart, error) {
	temp := []json.RawMessage{}

	if err := json.Unmarshal(data, &temp); err != nil {
		return nil, err
	}

	parts := make([]ContentPart, 0)

	for _, rawPart := range temp {
		var wrapper struct {
			Type partType        `json:"type"`
			Data json.RawMessage `json:"data"`
		}

		if err := json.Unmarshal(rawPart, &wrapper); err != nil {
			return nil, err
		}

		switch wrapper.Type {
		case reasoningType:
			part := ReasoningContent{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case textType:
			part := TextContent{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case imageURLType:
			part := ImageURLContent{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
		case binaryType:
			part := BinaryContent{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case attachmentMetaType:
			part := AttachmentMetaContent{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case toolCallType:
			part := ToolCall{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case toolResultType:
			part := ToolResult{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case finishType:
			part := Finish{}
			if err := json.Unmarshal(wrapper.Data, &part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("unknown part type: %s", wrapper.Type)
		}

	}

	return parts, nil
}

// ExtractTextFromJSON parses the JSON string of message parts and returns the concatenated text content.
func ExtractTextFromJSON(jsonStr string) string {
	parts, err := unmarshallParts([]byte(jsonStr))
	if err != nil {
		return jsonStr // Fallback to raw string if parsing fails
	}
	var sb strings.Builder
	for _, part := range parts {
		if txt, ok := part.(TextContent); ok {
			sb.WriteString(txt.Text)
		}
	}
	return sb.String()
}

func ExtractEmbeddingTextFromJSON(jsonStr string) string {
	parts, err := unmarshallParts([]byte(jsonStr))
	if err != nil {
		return jsonStr
	}
	return ExtractTextForEmbedding(parts)
}
