-- +goose Up
UPDATE public.ai_credentials
SET model = CASE
    WHEN provider = 'gemini' AND model = 'gemini-2.0-flash' THEN 'gemini-2.5-flash'
    WHEN provider = 'claude' AND model = 'claude-3-5-haiku-latest' THEN 'claude-haiku-4-5-20251001'
    WHEN provider = 'claude' AND model = 'claude-sonnet-4-20250514' THEN 'claude-sonnet-5'
    WHEN provider = 'claude' AND model = 'claude-opus-4-20250514' THEN 'claude-opus-5'
    WHEN provider = 'groq' AND model = 'llama-3.3-70b-versatile' THEN 'openai/gpt-oss-20b'
    WHEN provider = 'groq' AND model IN ('llama-3.1-8b-instant', 'meta-llama/llama-4-scout-17b-16e-instruct', 'meta-llama/llama-4-maverick-17b-128e-instruct') THEN 'openai/gpt-oss-20b'
    WHEN provider = 'deepseek' AND model = 'deepseek-chat' THEN 'deepseek-flash'
    WHEN provider = 'deepseek' AND model = 'deepseek-reasoner' THEN 'deepseek-v4-pro'
    ELSE model
END,
revision = revision + 1,
"updatedAt" = now()
WHERE (provider = 'gemini' AND model = 'gemini-2.0-flash')
   OR (provider = 'claude' AND model IN ('claude-3-5-haiku-latest', 'claude-sonnet-4-20250514', 'claude-opus-4-20250514'))
   OR (provider = 'groq' AND model IN ('llama-3.3-70b-versatile', 'llama-3.1-8b-instant', 'meta-llama/llama-4-scout-17b-16e-instruct', 'meta-llama/llama-4-maverick-17b-128e-instruct'))
   OR (provider = 'deepseek' AND model IN ('deepseek-chat', 'deepseek-reasoner'));

-- +goose Down
-- Model migrations are intentionally not reversed because prior models have been retired.
SELECT 1;
