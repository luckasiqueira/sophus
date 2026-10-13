-- +goose Up
ALTER TABLE public.contacts ADD COLUMN "customFields" jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE public.nps_evaluations (
    id bigserial PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "conversationId" integer NOT NULL REFERENCES public.conversations(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "executionId" integer REFERENCES public.flow_executions(id) ON UPDATE CASCADE ON DELETE SET NULL,
    "nodeId" character varying(100) NOT NULL,
    score smallint,
    category character varying(12),
    response text,
    status character varying(20) NOT NULL,
    "answeredAt" timestamp with time zone,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT nps_score_check CHECK (score IS NULL OR score BETWEEN 0 AND 10),
    CONSTRAINT nps_category_check CHECK (category IS NULL OR category IN ('detractor', 'passive', 'promoter')),
    CONSTRAINT nps_status_check CHECK (status IN ('waiting', 'answered', 'invalid', 'timed_out')),
    CONSTRAINT nps_execution_node_unique UNIQUE ("executionId", "nodeId")
);

CREATE INDEX nps_company_created_idx ON public.nps_evaluations ("companyId", "createdAt" DESC);

-- +goose Down
DROP TABLE IF EXISTS public.nps_evaluations;
ALTER TABLE public.contacts DROP COLUMN IF EXISTS "customFields";
