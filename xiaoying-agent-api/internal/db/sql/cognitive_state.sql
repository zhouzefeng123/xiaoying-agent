-- NOTE: SetCognitiveState is hand written in queries_manual.go because MySQL
-- has no RETURNING clause and spells the upsert as ON DUPLICATE KEY UPDATE.

-- name: GetCognitiveState :one
SELECT `key`, value, updated_at FROM cognitive_state WHERE `key` = ? LIMIT 1;

-- name: ListAllCognitiveStates :many
SELECT `key`, value, updated_at FROM cognitive_state;
