-- +goose Up
ALTER TABLE public.companies ADD COLUMN "originalAdminId" integer
    REFERENCES public.agents(id) ON DELETE RESTRICT;

-- Older accounts did not store the founder explicitly. Preserve the earliest
-- surviving account, even if its role has subsequently changed.
UPDATE public.companies company SET "originalAdminId" = (
    SELECT agent.id FROM public.agents agent WHERE agent."companyId" = company.id
    ORDER BY agent."createdAt", agent.id LIMIT 1
);

-- +goose StatementBegin
CREATE FUNCTION public.protect_original_company_admin() RETURNS trigger AS $$
BEGIN
    IF OLD."originalAdminId" IS NOT NULL AND NEW."originalAdminId" IS DISTINCT FROM OLD."originalAdminId" THEN
        RAISE EXCEPTION 'original company administrator cannot be replaced';
    END IF;
    IF NEW."originalAdminId" IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM public.agents WHERE id = NEW."originalAdminId" AND "companyId" = NEW.id
    ) THEN
        RAISE EXCEPTION 'original company administrator must belong to the company';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER companies_protect_original_admin BEFORE UPDATE OF "originalAdminId" ON public.companies
    FOR EACH ROW EXECUTE FUNCTION public.protect_original_company_admin();

-- +goose Down
DROP TRIGGER companies_protect_original_admin ON public.companies;
DROP FUNCTION public.protect_original_company_admin();
ALTER TABLE public.companies DROP COLUMN "originalAdminId";
