-- +goose Up
CREATE TABLE public.company_tags (
    id bigserial PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    name character varying(80) NOT NULL,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT company_tags_name_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 80)
);

CREATE UNIQUE INDEX company_tags_company_name_unique ON public.company_tags ("companyId", lower(btrim(name)));

CREATE TABLE public.contact_tag_assignments (
    "contactId" integer NOT NULL REFERENCES public.contacts(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "tagId" bigint NOT NULL REFERENCES public.company_tags(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY ("contactId", "tagId")
);

INSERT INTO public.company_tags ("companyId", name)
SELECT co."companyId", min(btrim(tag))
FROM public.conversations cv
JOIN public.connections co ON co.id = cv."connectionId"
CROSS JOIN LATERAL unnest(cv.tags) tag
WHERE btrim(tag) <> ''
GROUP BY co."companyId", lower(btrim(tag))
ON CONFLICT DO NOTHING;

INSERT INTO public.contact_tag_assignments ("contactId", "tagId")
SELECT DISTINCT cv."contactId", catalog.id
FROM public.conversations cv
JOIN public.connections co ON co.id = cv."connectionId"
CROSS JOIN LATERAL unnest(cv.tags) tag
JOIN public.company_tags catalog ON catalog."companyId" = co."companyId" AND lower(catalog.name) = lower(btrim(tag))
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS public.contact_tag_assignments;
DROP TABLE IF EXISTS public.company_tags;
