-- +goose Up
ALTER TABLE IF EXISTS public.tokens
    ADD COLUMN "connectionId" integer NOT NULL;
ALTER TABLE IF EXISTS public.tokens
    ADD CONSTRAINT connection FOREIGN KEY ("connectionId")
        REFERENCES public.connections (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID;
CREATE INDEX IF NOT EXISTS fki_connection
    ON public.tokens("connectionId");