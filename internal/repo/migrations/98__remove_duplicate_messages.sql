-- +goose Up
-- Concurrent deliveries of the same WhatsApp message could both pass the
-- "already stored?" check and be saved twice (seen with received images).
-- Keep one row per conversation and WhatsApp message ID: the one that holds
-- the media, then a non-deleted one, then the oldest.
CREATE TEMP TABLE message_duplicates ON COMMIT DROP AS
SELECT ranked.id AS duplicate_id, ranked.survivor_id
FROM (
    SELECT id,
        FIRST_VALUE(id) OVER (
            PARTITION BY "conversationId", "messageId"
            ORDER BY (COALESCE("mediaPath", '') <> '') DESC, COALESCE("isDeleted", false) ASC, id ASC
        ) AS survivor_id
    FROM public.messages
    WHERE BTRIM(COALESCE("messageId", '')) <> ''
) ranked
WHERE ranked.id <> ranked.survivor_id;

-- Reactions point at the row id and would be cascaded away: move them to the
-- surviving row (one reaction per sender is kept).
INSERT INTO public.message_reactions ("messageId", sender, emoji, "isFromMe", "eventId", "updatedAt")
SELECT DISTINCT ON (duplicate.survivor_id, reaction.sender)
    duplicate.survivor_id, reaction.sender, reaction.emoji, reaction."isFromMe", reaction."eventId", reaction."updatedAt"
FROM public.message_reactions reaction
JOIN message_duplicates duplicate ON duplicate.duplicate_id = reaction."messageId"
ORDER BY duplicate.survivor_id, reaction.sender, reaction."updatedAt" DESC
ON CONFLICT ("messageId", sender) DO NOTHING;

DELETE FROM public.messages message
USING message_duplicates duplicate
WHERE message.id = duplicate.duplicate_id;

-- +goose Down
-- Data cleanup only; removed duplicates cannot be restored.
SELECT 1;
