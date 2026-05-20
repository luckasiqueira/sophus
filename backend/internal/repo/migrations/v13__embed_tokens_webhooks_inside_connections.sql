ALTER TABLE IF EXISTS public.connections
    ADD COLUMN webhook uuid NOT NULL;

ALTER TABLE IF EXISTS public.connections
    ADD COLUMN "apiToken" character varying(32);

ALTER TABLE IF EXISTS public.connections
    ADD COLUMN "connectionKey" uuid;

drop table if exists webhooks cascade;

drop table if exists tokens cascade;

