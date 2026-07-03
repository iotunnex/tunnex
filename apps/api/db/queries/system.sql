-- name: GenerateID :one
-- Returns a fresh time-ordered UUIDv7 from the database. Demonstrates the sqlc
-- pipeline and the uuid override; callers may also generate v7 ids in Go.
SELECT uuid_generate_v7() AS id;
