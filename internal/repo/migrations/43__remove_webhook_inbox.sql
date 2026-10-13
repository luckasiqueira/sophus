-- +goose Up
DROP TABLE IF EXISTS public.webhook_inbox;

-- +goose Down
SELECT 1;
