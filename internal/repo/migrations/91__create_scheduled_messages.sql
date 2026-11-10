-- +goose Up
-- Messages scheduled by agents for a contact. The conversation is resolved at
-- send time, so a schedule survives the contact's conversations being closed.
CREATE TABLE public.scheduled_messages
(
    id               bigserial                   NOT NULL,
    "companyId"      integer                     NOT NULL,
    "contactId"      integer                     NOT NULL,
    "connectionId"   integer                     NOT NULL,
    "agentId"        integer,
    body             text                        NOT NULL DEFAULT '',
    "mediaKind"      character varying(20),
    "mediaPath"      character varying(255),
    "mediaName"      character varying(255),
    "mediaSize"      bigint,
    "scheduledFor"   timestamp with time zone    NOT NULL,
    status           character varying(20)       NOT NULL DEFAULT 'pending',
    attempts         integer                     NOT NULL DEFAULT 0,
    "claimVersion"   integer                     NOT NULL DEFAULT 0,
    "claimedAt"      timestamp with time zone,
    "sentAt"         timestamp with time zone,
    "conversationId" integer,
    "lastError"      text,
    "createdAt"      timestamp with time zone    NOT NULL DEFAULT now(),
    "updatedAt"      timestamp with time zone    NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT scheduled_messages_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_messages_contact_fk FOREIGN KEY ("contactId")
        REFERENCES public.contacts (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_messages_connection_fk FOREIGN KEY ("connectionId")
        REFERENCES public.connections (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_messages_agent_fk FOREIGN KEY ("agentId")
        REFERENCES public.agents (id) ON UPDATE CASCADE ON DELETE SET NULL,
    CONSTRAINT scheduled_messages_conversation_fk FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) ON UPDATE CASCADE ON DELETE SET NULL,
    CONSTRAINT scheduled_messages_status_check CHECK (status IN ('pending', 'processing', 'sent', 'failed', 'canceled')),
    CONSTRAINT scheduled_messages_body_check CHECK (char_length(body) <= 4096),
    CONSTRAINT scheduled_messages_media_check CHECK (
        ("mediaPath" IS NULL AND "mediaKind" IS NULL)
        OR ("mediaPath" IS NOT NULL AND "mediaKind" IN ('image', 'video', 'audio', 'document'))),
    CONSTRAINT scheduled_messages_content_check CHECK (btrim(body) <> '' OR "mediaPath" IS NOT NULL),
    CONSTRAINT scheduled_messages_attempts_check CHECK (attempts BETWEEN 0 AND 3)
);

CREATE INDEX scheduled_messages_due_idx
    ON public.scheduled_messages ("scheduledFor", id)
    WHERE status = 'pending';

CREATE INDEX scheduled_messages_company_idx
    ON public.scheduled_messages ("companyId", status, "scheduledFor" DESC);

CREATE INDEX scheduled_messages_media_idx
    ON public.scheduled_messages ("companyId", "mediaPath")
    WHERE "mediaPath" IS NOT NULL AND status IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS public.scheduled_messages;
