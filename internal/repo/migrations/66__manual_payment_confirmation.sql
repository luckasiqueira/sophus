-- +goose Up
ALTER TABLE public.payments
    ADD COLUMN "manualConfirmedAt" timestamptz,
    ADD COLUMN "manualConfirmedBy" integer REFERENCES public.platform_admins(id) ON DELETE SET NULL,
    ADD COLUMN "manualPaymentReason" text;

-- +goose Down
ALTER TABLE public.payments
    DROP COLUMN "manualPaymentReason",
    DROP COLUMN "manualConfirmedBy",
    DROP COLUMN "manualConfirmedAt";
