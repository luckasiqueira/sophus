-- +goose Up
ALTER TABLE public.chatbot_flows
    ADD COLUMN revision integer NOT NULL DEFAULT 1,
    ADD COLUMN priority integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT chatbot_flows_priority_check CHECK (priority BETWEEN -1000 AND 1000);

ALTER TABLE public.flow_executions
    ADD COLUMN "flowDataSnapshot" jsonb,
    ADD COLUMN "flowRevision" integer,
    ADD COLUMN "snapshotIsLegacy" boolean NOT NULL DEFAULT true,
    ADD COLUMN "claimVersion" integer NOT NULL DEFAULT 1,
    ADD COLUMN "resumeAt" timestamp with time zone,
    ADD COLUMN "waitReason" character varying(40);

UPDATE public.flow_executions
SET status = 'failed',
    "errorMessage" = 'execução encerrada durante migração para snapshots duráveis',
    "completedAt" = now(),
    "updatedAt" = now()
WHERE status IN ('running', 'waiting');

UPDATE public.conversations conversation
SET status = 'pending', "agentId" = NULL, "updatedAt" = now()
WHERE conversation.status = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM public.flow_executions execution
      WHERE execution."conversationId" = conversation.id
        AND execution.status IN ('running', 'waiting')
  );

UPDATE public.flow_executions execution
SET "flowDataSnapshot" = flow."flowData",
    "flowRevision" = flow.revision
FROM public.chatbot_flows flow
WHERE flow.id = execution."flowId";

UPDATE public.flow_executions
SET "flowDataSnapshot" = '{"nodes":[],"edges":[]}'::jsonb
WHERE "flowDataSnapshot" IS NULL;

ALTER TABLE public.flow_executions
    ALTER COLUMN "flowDataSnapshot" SET NOT NULL,
    ALTER COLUMN "flowRevision" SET NOT NULL;

ALTER TABLE public.flow_executions
    DROP CONSTRAINT flow_executions_flow_fk,
    ADD CONSTRAINT flow_executions_flow_fk FOREIGN KEY ("flowId")
        REFERENCES public.chatbot_flows (id)
        ON UPDATE CASCADE
        ON DELETE NO ACTION;

CREATE INDEX flow_executions_due_resume_idx
    ON public.flow_executions ("resumeAt", id)
    WHERE status = 'waiting' AND "resumeAt" IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS public.flow_executions_due_resume_idx;

ALTER TABLE public.flow_executions
    DROP CONSTRAINT flow_executions_flow_fk,
    ADD CONSTRAINT flow_executions_flow_fk FOREIGN KEY ("flowId")
        REFERENCES public.chatbot_flows (id)
        ON UPDATE CASCADE
        ON DELETE CASCADE,
    DROP COLUMN IF EXISTS "waitReason",
    DROP COLUMN IF EXISTS "resumeAt",
    DROP COLUMN IF EXISTS "snapshotIsLegacy",
    DROP COLUMN IF EXISTS "flowRevision",
    DROP COLUMN IF EXISTS "claimVersion",
    DROP COLUMN IF EXISTS "flowDataSnapshot";

ALTER TABLE public.chatbot_flows
    DROP CONSTRAINT IF EXISTS chatbot_flows_priority_check,
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS revision;
