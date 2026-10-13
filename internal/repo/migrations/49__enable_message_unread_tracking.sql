-- +goose Up
UPDATE public.messages SET "isRead" = true WHERE "isRead" = false;

CREATE INDEX messages_unread_conversation_idx
ON public.messages ("conversationId")
WHERE "isFromMe" = false AND "isRead" = false AND "isDeleted" = false;

-- +goose Down
DROP INDEX IF EXISTS public.messages_unread_conversation_idx;
