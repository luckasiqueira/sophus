-- +goose Up
ALTER TABLE public.contacts
    ADD COLUMN company character varying(160) NOT NULL DEFAULT '';

CREATE INDEX contacts_company_lower_idx ON public.contacts (lower(company));

ALTER TABLE public.whatsapp_widgets
    ADD COLUMN "iconOnly" boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE public.whatsapp_widgets DROP COLUMN IF EXISTS "iconOnly";
DROP INDEX IF EXISTS public.contacts_company_lower_idx;
ALTER TABLE public.contacts DROP COLUMN IF EXISTS company;
