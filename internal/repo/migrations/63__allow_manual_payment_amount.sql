-- +goose Up
ALTER TABLE public.payments ADD COLUMN "manualAmount" boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE public.payments DROP COLUMN "manualAmount";
