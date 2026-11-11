-- +goose Up
-- How many of the customer's latest images in the chat history an AI agent
-- receives as pictures. Existing agents keep sending text only.
ALTER TABLE public.ai_agents
    ADD COLUMN "historyImageLimit" integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT ai_agents_history_image_check CHECK ("historyImageLimit" BETWEEN 0 AND 10);

-- +goose Down
ALTER TABLE public.ai_agents
    DROP CONSTRAINT IF EXISTS ai_agents_history_image_check,
    DROP COLUMN IF EXISTS "historyImageLimit";
