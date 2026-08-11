-- name: GetReviewMinorNotes :many
SELECT n.id, n.review_id, n.attempt_generation, n.file_path, n.line, n.severity, n.title, n.created_at
FROM review_minor_notes n
JOIN reviews r ON r.id = n.review_id
WHERE n.review_id = $1 AND n.attempt_generation = r.attempt_generation
ORDER BY n.file_path, n.line, n.id;
