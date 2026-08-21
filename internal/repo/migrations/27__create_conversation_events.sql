-- +goose Up
CREATE TABLE public.conversation_events
(
    id               bigserial                   NOT NULL,
    "companyId"      integer                     NOT NULL,
    "conversationId" integer                     NOT NULL,
    "executionId"    integer,
    "nodeId"         character varying(100),
    "eventType"      character varying(30)       NOT NULL,
    content          text                        NOT NULL,
    metadata         jsonb                       NOT NULL DEFAULT '{}',
    "createdAt"      timestamp with time zone    NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT conversation_events_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT conversation_events_conversation_fk FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT conversation_events_execution_fk FOREIGN KEY ("executionId")
        REFERENCES public.flow_executions (id) ON UPDATE CASCADE ON DELETE SET NULL,
    CONSTRAINT conversation_events_type_check CHECK ("eventType" IN ('note', 'history')),
    CONSTRAINT conversation_events_content_check CHECK (char_length(content) BETWEEN 1 AND 10000)
);

CREATE UNIQUE INDEX conversation_events_flow_node_unique
    ON public.conversation_events ("executionId", "nodeId", "eventType")
    WHERE "executionId" IS NOT NULL AND "nodeId" IS NOT NULL;

CREATE INDEX conversation_events_timeline_idx
    ON public.conversation_events ("conversationId", "createdAt" DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS public.conversation_events;
