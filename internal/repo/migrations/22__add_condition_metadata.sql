-- +goose Up
ALTER TABLE public.contacts
    ADD COLUMN email character varying(320) NOT NULL DEFAULT '';

ALTER TABLE public.conversations
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}';

ALTER TABLE public.connections
    ADD COLUMN type character varying(40) NOT NULL DEFAULT 'whatsapp_qrcode';

-- +goose Down
ALTER TABLE public.connections DROP COLUMN IF EXISTS type;
ALTER TABLE public.conversations DROP COLUMN IF EXISTS tags;
ALTER TABLE public.contacts DROP COLUMN IF EXISTS email;
