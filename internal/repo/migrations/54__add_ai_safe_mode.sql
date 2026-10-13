-- +goose Up
ALTER TABLE public.ai_agents
    ADD COLUMN "safeMode" boolean NOT NULL DEFAULT true;

ALTER TABLE public.ai_agent_sessions
    ADD COLUMN "safeMode" boolean;

-- +goose Down
ALTER TABLE public.ai_agent_sessions
    DROP COLUMN IF EXISTS "safeMode";

ALTER TABLE public.ai_agents
    DROP COLUMN IF EXISTS "safeMode";
