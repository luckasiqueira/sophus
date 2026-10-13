-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS messages_message_id_id_idx
    ON public.messages ("messageId", id DESC)
    INCLUDE ("conversationId");

CREATE INDEX CONCURRENTLY IF NOT EXISTS conversations_contact_connection_latest_idx
    ON public.conversations ("contactId", "connectionId", id DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS connections_webhook_idx
    ON public.connections (webhook);

CREATE INDEX CONCURRENTLY IF NOT EXISTS connections_api_token_idx
    ON public.connections ("apiToken")
    WHERE "apiToken" IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS scheduled_flow_messages_processing_claimed_idx
    ON public.scheduled_flow_messages ("claimedAt", id)
    WHERE status = 'processing';

CREATE INDEX CONCURRENTLY IF NOT EXISTS flow_executions_running_recovery_idx
    ON public.flow_executions ("updatedAt", id)
    WHERE status = 'running' AND "waitReason" IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.flow_executions_running_recovery_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.scheduled_flow_messages_processing_claimed_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.connections_api_token_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.connections_webhook_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.conversations_contact_connection_latest_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.messages_message_id_id_idx;
