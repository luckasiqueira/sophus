-- +goose Up
CREATE INDEX messages_latest_contact_idx
    ON public.messages ("conversationId", "createdAt" DESC, id DESC)
    WHERE "isFromMe" = false AND "isDeleted" = false;

-- +goose Down
DROP INDEX IF EXISTS public.messages_latest_contact_idx;
