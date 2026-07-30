-- +goose Up
CREATE TABLE public.chatbot_flows
(
    id            serial                              NOT NULL,
    name          character varying                   NOT NULL,
    description   text,
    "companyId"   integer                             NOT NULL,
    "connectionId" integer,
    "triggerType" character varying                   NOT NULL DEFAULT 'keyword',
    "triggerValue" character varying,
    "flowData"    jsonb                               NOT NULL DEFAULT '{}',
    "isActive"    boolean                             NOT NULL DEFAULT false,
    "createdBy"   integer,
    "createdAt"   timestamp with time zone            NOT NULL DEFAULT now(),
    "updatedAt"   timestamp with time zone            NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT chatbot_flows_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE,
    CONSTRAINT chatbot_flows_connection_fk FOREIGN KEY ("connectionId")
        REFERENCES public.connections (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE SET NULL
);

CREATE TABLE public.flow_executions
(
    id               serial                              NOT NULL,
    "flowId"         integer                             NOT NULL,
    "conversationId" integer                             NOT NULL,
    "companyId"      integer                             NOT NULL,
    status           character varying                   NOT NULL DEFAULT 'running',
    "currentNodeId"  character varying,
    context          jsonb                               NOT NULL DEFAULT '{}',
    "errorMessage"   text,
    "createdAt"      timestamp with time zone            NOT NULL DEFAULT now(),
    "updatedAt"      timestamp with time zone            NOT NULL DEFAULT now(),
    "completedAt"    timestamp with time zone,
    PRIMARY KEY (id),
    CONSTRAINT flow_executions_flow_fk FOREIGN KEY ("flowId")
        REFERENCES public.chatbot_flows (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE,
    CONSTRAINT flow_executions_conversation_fk FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
);

CREATE INDEX flow_executions_waiting_idx
    ON public.flow_executions ("conversationId", status)
    WHERE status = 'waiting';

-- +goose Down
DROP TABLE IF EXISTS public.flow_executions;
DROP TABLE IF EXISTS public.chatbot_flows;
