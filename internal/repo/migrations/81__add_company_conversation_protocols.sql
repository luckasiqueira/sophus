-- +goose Up
LOCK TABLE conversations IN ACCESS EXCLUSIVE MODE;

ALTER TABLE conversations
    ADD COLUMN "companyId" integer REFERENCES companies(id) ON DELETE CASCADE,
    ADD COLUMN protocol bigint;

WITH numbered AS (
    SELECT conversation.id, contact."companyId",
        ROW_NUMBER() OVER (PARTITION BY contact."companyId" ORDER BY conversation."createdAt", conversation.id) AS protocol
    FROM conversations conversation
    JOIN contacts contact ON contact.id = conversation."contactId"
)
UPDATE conversations conversation SET "companyId" = numbered."companyId", protocol = numbered.protocol
FROM numbered WHERE numbered.id = conversation.id;

ALTER TABLE conversations
    ALTER COLUMN "companyId" SET NOT NULL,
    ALTER COLUMN protocol SET NOT NULL,
    ADD CONSTRAINT conversations_protocol_positive CHECK (protocol > 0),
    ADD CONSTRAINT conversations_company_protocol_key UNIQUE ("companyId", protocol);

CREATE TABLE company_conversation_protocols (
    "companyId" integer PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    "lastProtocol" bigint NOT NULL CHECK ("lastProtocol" > 0)
);
INSERT INTO company_conversation_protocols ("companyId", "lastProtocol")
SELECT "companyId", MAX(protocol) FROM conversations GROUP BY "companyId";

-- +goose StatementBegin
CREATE FUNCTION assign_company_conversation_protocol() RETURNS trigger AS $$
DECLARE
    owner_company integer;
BEGIN
    SELECT "companyId" INTO owner_company FROM contacts WHERE id = NEW."contactId";
    IF owner_company IS NULL THEN
        RAISE EXCEPTION 'Conversation contact has no company' USING ERRCODE = '23503';
    END IF;
    IF NEW."companyId" IS NOT NULL AND NEW."companyId" <> owner_company THEN
        RAISE EXCEPTION 'Conversation company does not own its contact' USING ERRCODE = '23514';
    END IF;
    IF NEW."connectionId" IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM connections WHERE id = NEW."connectionId" AND "companyId" = owner_company
    ) THEN
        RAISE EXCEPTION 'Conversation connection belongs to another company' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.protocol IS NOT NULL THEN
            RAISE EXCEPTION 'Conversation protocol is assigned automatically' USING ERRCODE = '23514';
        END IF;
        -- The upsert locks only this company's counter. Allocation participates
        -- in the insert transaction, preventing races and rolled-back increments.
        INSERT INTO company_conversation_protocols ("companyId", "lastProtocol") VALUES (owner_company, 1)
        ON CONFLICT ("companyId") DO UPDATE
            SET "lastProtocol" = company_conversation_protocols."lastProtocol" + 1
        RETURNING "lastProtocol" INTO NEW.protocol;
        NEW."companyId" := owner_company;
    ELSIF NEW."companyId" IS DISTINCT FROM OLD."companyId" OR NEW.protocol IS DISTINCT FROM OLD.protocol THEN
        RAISE EXCEPTION 'Conversation company and protocol cannot be changed' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER conversations_assign_company_protocol
BEFORE INSERT OR UPDATE OF "companyId", protocol, "contactId", "connectionId" ON conversations
FOR EACH ROW EXECUTE FUNCTION assign_company_conversation_protocol();

-- +goose Down
DROP TRIGGER conversations_assign_company_protocol ON conversations;
DROP FUNCTION assign_company_conversation_protocol();
ALTER TABLE conversations DROP COLUMN protocol, DROP COLUMN "companyId";
DROP TABLE company_conversation_protocols;
