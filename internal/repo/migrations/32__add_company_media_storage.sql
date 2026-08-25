-- +goose Up
ALTER TABLE public.plans
    ADD COLUMN "storageLimitGB" integer NOT NULL DEFAULT 5,
    ADD CONSTRAINT plans_storage_limit_check CHECK ("storageLimitGB" BETWEEN 1 AND 100000);

ALTER TABLE public.companies
    ADD COLUMN "mediaRetentionDays" integer,
    ADD CONSTRAINT companies_media_retention_check CHECK ("mediaRetentionDays" IN (30, 90, 120));

CREATE TABLE public.company_storage_usage (
    "companyId" integer PRIMARY KEY REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "usedBytes" bigint NOT NULL DEFAULT 0 CHECK ("usedBytes" >= 0),
    "reservedBytes" bigint NOT NULL DEFAULT 0 CHECK ("reservedBytes" >= 0),
	"reservationUpdatedAt" timestamp with time zone,
    "fileCount" integer NOT NULL DEFAULT 0 CHECK ("fileCount" >= 0),
    "scannedAt" timestamp with time zone,
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now()
);

INSERT INTO public.company_storage_usage ("companyId")
SELECT id FROM public.companies
ON CONFLICT ("companyId") DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS public.company_storage_usage;
ALTER TABLE public.companies
    DROP CONSTRAINT IF EXISTS companies_media_retention_check,
    DROP COLUMN IF EXISTS "mediaRetentionDays";
ALTER TABLE public.plans
    DROP CONSTRAINT IF EXISTS plans_storage_limit_check,
    DROP COLUMN IF EXISTS "storageLimitGB";
