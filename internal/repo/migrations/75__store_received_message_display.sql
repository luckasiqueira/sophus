-- +goose Up
ALTER TABLE public.messages ADD COLUMN "displayData" jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE public.messages DROP COLUMN "displayData";
