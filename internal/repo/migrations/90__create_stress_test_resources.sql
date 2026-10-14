-- +goose Up
-- Records created by the super-admin stress test tool. Reverting a test deletes
-- exactly these rows, and background workers use it to leave simulated agents
-- and instances out of real traffic (auto-assignment, Evolution status sync).
CREATE TABLE public.stress_test_resources (
    "resourceType" character varying(20) NOT NULL CHECK ("resourceType" IN ('contact', 'agent', 'connection')),
    "resourceId" integer NOT NULL,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON DELETE CASCADE,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY ("resourceType", "resourceId")
);
CREATE INDEX stress_test_resources_company_idx ON public.stress_test_resources ("companyId", "resourceType");

-- Contacts generated before this table existed carry the customFields marker.
INSERT INTO public.stress_test_resources ("resourceType", "resourceId", "companyId")
SELECT 'contact', id, "companyId" FROM public.contacts
WHERE "companyId" IS NOT NULL AND "customFields" @> '{"stressTest": true}'
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE public.stress_test_resources;
