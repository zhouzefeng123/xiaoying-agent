-- NOTE: CreateKnowledgeNode / UpdateKnowledgeNode are hand written in
-- queries_manual.go because MySQL has no RETURNING clause.

-- name: GetKnowledgeNode :one
SELECT * FROM knowledge_tree WHERE id = ? LIMIT 1;

-- name: ListKnowledgeByLayer :many
SELECT * FROM knowledge_tree
WHERE layer = ?
ORDER BY confidence DESC, access_count DESC;

-- name: ListKnowledgeByParent :many
SELECT * FROM knowledge_tree
WHERE parent_id = ?
ORDER BY created_at DESC;

-- name: SearchKnowledge :many
SELECT * FROM knowledge_tree
WHERE
    (layer = sqlc.arg('layer') OR sqlc.arg('layer') = 0) AND -- If layer param is 0, search all layers
    (content LIKE sqlc.arg('content_pattern') OR keywords LIKE sqlc.arg('keywords_pattern'))
ORDER BY layer ASC, confidence DESC
LIMIT ?;

-- name: UpdateKnowledgeNodeConfidence :exec
UPDATE knowledge_tree
SET confidence = coalesce(sqlc.narg('confidence'), confidence),
    layer = coalesce(sqlc.narg('layer'), layer),
    last_accessed_at = coalesce(sqlc.narg('last_accessed_at'), last_accessed_at)
WHERE id = sqlc.arg('id');

-- name: IncrementAccessCount :exec
UPDATE knowledge_tree SET access_count = access_count + 1 WHERE id = ?;

-- name: DeleteKnowledgeNode :exec
DELETE FROM knowledge_tree WHERE id = ?;

-- GraphRAG Support: Knowledge Edges
-- NOTE: CreateKnowledgeEdge is hand written in queries_manual.go (dialect
-- neutral upsert instead of MySQL's ON DUPLICATE KEY UPDATE).

-- name: GetKnowledgeEdges :many
SELECT e.*, k.content as target_content, k.category as target_category
FROM knowledge_edges e
JOIN knowledge_tree k ON e.target_id = k.id
WHERE e.source_id = ?
ORDER BY e.weight DESC;

-- name: GetIncomingEdges :many
SELECT e.*, k.content as source_content, k.category as source_category
FROM knowledge_edges e
JOIN knowledge_tree k ON e.source_id = k.id
WHERE e.target_id = ?
ORDER BY e.weight DESC;

-- name: DeleteKnowledgeEdge :exec
DELETE FROM knowledge_edges
WHERE source_id = ? AND target_id = ? AND relation = ?;
