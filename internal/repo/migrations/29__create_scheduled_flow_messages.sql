-- +goose Up
CREATE TABLE public.scheduled_flow_messages
(
    id               bigserial                   NOT NULL,
    "companyId"      integer                     NOT NULL,
    "conversationId" integer                     NOT NULL,
    "connectionId"   integer                     NOT NULL,
    "executionId"    integer,
    "nodeId"         character varying(100)      NOT NULL,
    message          text                        NOT NULL,
    status           character varying(20)       NOT NULL DEFAULT 'pending',
    attempts         integer                     NOT NULL DEFAULT 0,
    "claimVersion"   integer                     NOT NULL DEFAULT 0,
    "dueAt"          timestamp with time zone    NOT NULL,
    "claimedAt"      timestamp with time zone,
    "sentAt"         timestamp with time zone,
    "lastError"      text,
    "createdAt"      timestamp with time zone    NOT NULL DEFAULT now(),
    "updatedAt"      timestamp with time zone    NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT scheduled_flow_messages_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_flow_messages_conversation_fk FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_flow_messages_connection_fk FOREIGN KEY ("connectionId")
        REFERENCES public.connections (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT scheduled_flow_messages_execution_fk FOREIGN KEY ("executionId")
        REFERENCES public.flow_executions (id) ON UPDATE CASCADE ON DELETE SET NULL,
    CONSTRAINT scheduled_flow_messages_status_check CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    CONSTRAINT scheduled_flow_messages_message_check CHECK (char_length(message) BETWEEN 1 AND 10000),
    CONSTRAINT scheduled_flow_messages_attempts_check CHECK (attempts BETWEEN 0 AND 3)
);

CREATE UNIQUE INDEX scheduled_flow_messages_node_unique
    ON public.scheduled_flow_messages ("executionId", "nodeId")
    WHERE "executionId" IS NOT NULL;

CREATE INDEX scheduled_flow_messages_due_idx
    ON public.scheduled_flow_messages ("dueAt", id)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS public.scheduled_flow_messages;
