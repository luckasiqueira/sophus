-- +goose Up
CREATE INDEX IF NOT EXISTS conversations_contact_connection_history_idx
ON public.conversations ("contactId", "connectionId", id);

-- +goose Down
DROP INDEX IF EXISTS public.conversations_contact_connection_history_idx;
