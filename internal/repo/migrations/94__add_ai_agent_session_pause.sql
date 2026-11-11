-- +goose Up
-- The "Pausar a IA" action silences the agent for a while (or until a person
-- takes over) without transferring or closing the conversation.
ALTER TABLE public.ai_agent_sessions DROP CONSTRAINT IF EXISTS ai_agent_sessions_status_check;
ALTER TABLE public.ai_agent_sessions
    ADD CONSTRAINT ai_agent_sessions_status_check CHECK (status IN ('active', 'paused', 'ended', 'failed')),
    ADD COLUMN "pausedUntil" timestamp with time zone;

-- +goose Down
UPDATE public.ai_agent_sessions SET status = 'ended', "endedAt" = COALESCE("endedAt", now()) WHERE status = 'paused';
ALTER TABLE public.ai_agent_sessions DROP CONSTRAINT IF EXISTS ai_agent_sessions_status_check;
ALTER TABLE public.ai_agent_sessions
    DROP COLUMN IF EXISTS "pausedUntil",
    ADD CONSTRAINT ai_agent_sessions_status_check CHECK (status IN ('active', 'ended', 'failed'));
