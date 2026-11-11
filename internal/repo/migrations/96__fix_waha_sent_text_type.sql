-- +goose Up
-- Texts sent through the WAHA bridge before the send-response fix were stored
-- with the whole GOWS event as content, classified as an unknown "Message"
-- type and shown as "Conteúdo indisponível". They are plain texts.
UPDATE public.messages
SET "mediaType" = 'text',
    "displayData" = COALESCE("displayData", '{}'::jsonb) - 'title'
WHERE "isFromMe" = true
  AND "mediaType" = 'unknown'
  AND "displayData"->>'title' = 'Message'
  AND COALESCE("mediaPath", '') = '';

-- +goose Down
-- Data fix only; nothing to undo.
SELECT 1;
