-- +goose Up
ALTER TABLE messages ADD COLUMN "mediaSource" jsonb NOT NULL DEFAULT '{}';
UPDATE messages SET "mediaType"='system', "isRead"=true, "isDeleted"=true
WHERE "mediaType"='unknown' AND text IN ('protocolMessage','senderKeyDistributionMessage','messageContextInfo')
AND COALESCE("mediaPath",'')='';

-- +goose Down
ALTER TABLE messages DROP COLUMN "mediaSource";
