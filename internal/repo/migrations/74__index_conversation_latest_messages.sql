-- +goose NO TRANSACTION
-- +goose Up
-- The sidebar previews the latest incoming OR outgoing message. Its old
-- incoming-only index does not cover this query predicate.
CREATE INDEX CONCURRENTLY IF NOT EXISTS messages_latest_conversation_idx
    ON public.messages ("conversationId", "createdAt" DESC, id DESC)
    INCLUDE ("mediaType") WHERE "isDeleted" = false;
CREATE INDEX CONCURRENTLY IF NOT EXISTS messages_conversation_history_idx
    ON public.messages ("conversationId", "createdAt" DESC, id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.messages_conversation_history_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.messages_latest_conversation_idx;
