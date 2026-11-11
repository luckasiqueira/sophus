-- +goose Up
-- Recent results of the AI agent's actions (e.g. API responses), so the agent
-- still knows them when answering the customer's next messages.
ALTER TABLE public.ai_agent_sessions
    ADD COLUMN "actionMemory" jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE public.ai_agent_sessions DROP COLUMN IF EXISTS "actionMemory";
