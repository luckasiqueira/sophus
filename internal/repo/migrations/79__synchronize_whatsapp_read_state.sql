-- +goose Up
ALTER TABLE messages ADD COLUMN "whatsappReadSynced" boolean NOT NULL DEFAULT false;
CREATE TABLE message_read_receipts (
    "connectionId" integer NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    "messageId" text NOT NULL,
    PRIMARY KEY ("connectionId","messageId")
);
CREATE INDEX messages_pending_whatsapp_read_idx ON messages ("conversationId",id)
WHERE "isFromMe"=false AND "isRead"=true AND "whatsappReadSynced"=false AND "isDeleted"=false;

-- +goose Down
DROP TABLE message_read_receipts;
DROP INDEX messages_pending_whatsapp_read_idx;
ALTER TABLE messages DROP COLUMN "whatsappReadSynced";
