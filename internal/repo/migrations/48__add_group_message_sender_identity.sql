-- +goose Up
ALTER TABLE public.messages
ADD COLUMN "senderName" character varying NOT NULL DEFAULT '';

CREATE INDEX contacts_connection_jid_lookup_idx
ON public.contacts ("connectionId", jid)
WHERE COALESCE("isGroup", false) = false AND COALESCE(jid, '') <> '';

CREATE INDEX contacts_connection_lid_lookup_idx
ON public.contacts ("connectionId", lid)
WHERE COALESCE("isGroup", false) = false AND COALESCE(lid, '') <> '';

-- +goose Down
DROP INDEX IF EXISTS public.contacts_connection_lid_lookup_idx;
DROP INDEX IF EXISTS public.contacts_connection_jid_lookup_idx;
ALTER TABLE public.messages DROP COLUMN IF EXISTS "senderName";
