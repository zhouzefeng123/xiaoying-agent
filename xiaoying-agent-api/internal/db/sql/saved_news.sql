-- name: ListSavedNews :many
SELECT id, title, summary, category, url, published_at, created_at FROM saved_news
ORDER BY created_at DESC
LIMIT ?;
