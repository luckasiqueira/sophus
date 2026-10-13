-- +goose Up
CREATE UNIQUE INDEX conversations_url_unique ON public.conversations (url);

-- +goose Down
DROP INDEX IF EXISTS public.conversations_url_unique;
