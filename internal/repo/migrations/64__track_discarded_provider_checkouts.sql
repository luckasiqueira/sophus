-- +goose Up
ALTER TABLE public.payments ADD COLUMN "ignoredAt" timestamptz;

-- +goose Down
ALTER TABLE public.payments DROP COLUMN "ignoredAt";
