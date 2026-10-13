-- +goose Up
ALTER TABLE messages ADD COLUMN IF NOT EXISTS "editSource" jsonb NOT NULL DEFAULT '{}';
UPDATE messages SET "mediaType"='system', "isRead"=true, "isDeleted"=true
WHERE "mediaType"='unknown' AND text='secretEncryptedMessage' AND COALESCE("mediaPath",'')='';

-- +goose Down
ALTER TABLE messages DROP COLUMN "editSource";
