-- +goose Up
ALTER TABLE public.ai_credentials DROP CONSTRAINT ai_credentials_provider_check;
ALTER TABLE public.ai_credentials ADD CONSTRAINT ai_credentials_provider_check
    CHECK (provider IN ('openai', 'deepseek', 'groq', 'gemini', 'claude'));

-- +goose Down
ALTER TABLE public.ai_credentials DROP CONSTRAINT ai_credentials_provider_check;
ALTER TABLE public.ai_credentials ADD CONSTRAINT ai_credentials_provider_check
    CHECK (provider IN ('openai', 'deepseek', 'gemini', 'claude'));
