-- +goose Up
-- Knowledge and actions the flow connected to the AI agent block, captured
-- when the flow hands the conversation over so later edits to the flow do not
-- change a session already running.
ALTER TABLE public.ai_agent_sessions
    ADD COLUMN extensions jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE public.ai_agent_sessions DROP COLUMN IF EXISTS extensions;
