-- +goose Up
ALTER TABLE IF EXISTS public.webhooks
    ADD CONSTRAINT connection FOREIGN KEY (connectionid)
        REFERENCES public.connections (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID;
CREATE INDEX IF NOT EXISTS fki_connection
    ON public.webhooks(connectionid);

ALTER TABLE IF EXISTS public.connections
    ADD CONSTRAINT company FOREIGN KEY (companyid)
        REFERENCES public.companies (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID;
CREATE INDEX IF NOT EXISTS fki_company
    ON public.connections(companyid);