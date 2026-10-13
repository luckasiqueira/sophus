-- +goose Up
CREATE TABLE message_receipts (
    "connectionId" integer NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    "messageId" text NOT NULL,
    status smallint NOT NULL CHECK (status BETWEEN 1 AND 3),
    PRIMARY KEY ("connectionId", "messageId")
);

-- +goose Down
DROP TABLE message_receipts;
