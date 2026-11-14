-- +goose Up
-- Base extensions. Tables are introduced by the phase that needs them
-- (see docs/DATABASE_DESIGN.md).
CREATE EXTENSION IF NOT EXISTS citext;  -- case-insensitive emails
CREATE EXTENSION IF NOT EXISTS vector;  -- pgvector for semantic retrieval

-- +goose Down
DROP EXTENSION IF EXISTS vector;
DROP EXTENSION IF EXISTS citext;
