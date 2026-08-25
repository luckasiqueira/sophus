-- +goose Up
LOCK TABLE public.companies, public.agents IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.companies
        GROUP BY lower(btrim(email)) HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'Existem empresas com emails duplicados; corrija-os antes da migration 31';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.agents
        GROUP BY lower(btrim(email)) HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'Existem agentes com emails duplicados; corrija-os antes da migration 31';
    END IF;
END
$$;
-- +goose StatementEnd

UPDATE public.companies SET email = lower(btrim(email));
UPDATE public.agents SET email = lower(btrim(email));

CREATE UNIQUE INDEX companies_email_ci_unique ON public.companies (lower(btrim(email)));
DROP INDEX IF EXISTS public.agents_email_ci_idx;
CREATE UNIQUE INDEX agents_email_ci_unique ON public.agents (lower(btrim(email)));

-- +goose Down
DROP INDEX IF EXISTS public.agents_email_ci_unique;
CREATE INDEX agents_email_ci_idx ON public.agents (lower(email));
DROP INDEX IF EXISTS public.companies_email_ci_unique;
