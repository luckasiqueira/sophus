-- +goose Up
ALTER TABLE public.contacts ADD COLUMN "companyId" integer;

UPDATE public.contacts contact
SET "companyId" = connection."companyId"
FROM public.connections connection
WHERE contact."connectionId" = connection.id::text;

UPDATE public.contacts contact
SET "companyId" = connection."companyId"
FROM public.conversations conversation
JOIN public.connections connection ON connection.id = conversation."connectionId"
WHERE contact."companyId" IS NULL AND conversation."contactId" = contact.id;

-- Contacts without an owning connection or conversation cannot be attributed safely.
DELETE FROM public.contacts contact
WHERE contact."companyId" IS NULL
  AND NOT EXISTS (SELECT 1 FROM public.conversations conversation WHERE conversation."contactId" = contact.id);

CREATE INDEX contacts_company_merge_number_idx ON public.contacts ("companyId", number)
WHERE COALESCE("isGroup", false) = false AND number ~ '^[0-9]{8,20}$';
CREATE INDEX contacts_company_merge_jid_idx ON public.contacts ("companyId", jid)
WHERE COALESCE(jid, '') <> '';
CREATE INDEX contacts_company_merge_lid_idx ON public.contacts ("companyId", lid)
WHERE COALESCE("isGroup", false) = false AND COALESCE(lid, '') <> '';

-- Link contacts that represent the same person or group within one company.
CREATE TEMP TABLE contact_company_merge ON COMMIT DROP AS
WITH RECURSIVE identities (company_id, contact_id, kind, value) AS (
    SELECT "companyId", id, 'number', number FROM public.contacts
    WHERE COALESCE("isGroup", false) = false AND number ~ '^[0-9]{8,20}$'
    UNION
    SELECT "companyId", id, 'jid', jid FROM public.contacts
    WHERE COALESCE("isGroup", false) = false AND COALESCE(jid, '') <> ''
    UNION
    SELECT "companyId", id, 'lid', lid FROM public.contacts
    WHERE COALESCE("isGroup", false) = false AND COALESCE(lid, '') <> ''
    UNION
    SELECT "companyId", id, 'group_jid', jid FROM public.contacts
    WHERE COALESCE("isGroup", false) = true AND COALESCE(jid, '') <> ''
), identity_links (left_id, right_id) AS (
    SELECT MIN(contact_id) OVER (PARTITION BY company_id, kind, value), contact_id
    FROM identities
), reachable (root_id, contact_id) AS (
    SELECT id, id FROM public.contacts WHERE "companyId" IS NOT NULL
    UNION
    SELECT reachable.root_id,
        CASE WHEN identity_links.left_id = reachable.contact_id THEN identity_links.right_id ELSE identity_links.left_id END
    FROM reachable
    JOIN identity_links ON identity_links.left_id = reachable.contact_id OR identity_links.right_id = reachable.contact_id
), components AS (
    SELECT contact_id, MIN(root_id) AS component_id
    FROM reachable
    GROUP BY contact_id
), ranked AS (
    SELECT contact_id, component_id,
        FIRST_VALUE(contact_id) OVER (PARTITION BY component_id ORDER BY
            CASE WHEN BTRIM(contact.name) <> '' AND contact.name <> 'Contato salvo automaticamente' THEN 0 ELSE 1 END,
            contact.id) AS survivor_id,
        ROW_NUMBER() OVER (PARTITION BY component_id ORDER BY contact.id) AS position
    FROM components
    JOIN public.contacts contact ON contact.id = components.contact_id
)
SELECT contact_id AS duplicate_id, survivor_id
FROM ranked
WHERE contact_id <> survivor_id;

UPDATE public.contacts survivor
SET name = COALESCE((SELECT duplicate.name FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id AND BTRIM(duplicate.name) <> '' AND duplicate.name <> 'Contato salvo automaticamente'
                     ORDER BY duplicate.id LIMIT 1), survivor.name),
    number = COALESCE(NULLIF(survivor.number, ''), (SELECT NULLIF(duplicate.number, '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id AND duplicate.number ~ '^[0-9]{8,20}$' ORDER BY duplicate.id LIMIT 1), survivor.number),
    email = COALESCE(NULLIF(survivor.email, ''), (SELECT NULLIF(duplicate.email, '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id ORDER BY duplicate.id LIMIT 1), survivor.email),
    company = COALESCE(NULLIF(survivor.company, ''), (SELECT NULLIF(duplicate.company, '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id ORDER BY duplicate.id LIMIT 1), survivor.company),
    jid = COALESCE(NULLIF(survivor.jid, ''), (SELECT NULLIF(duplicate.jid, '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id ORDER BY duplicate.id LIMIT 1), survivor.jid),
    lid = COALESCE(NULLIF(survivor.lid, ''), (SELECT NULLIF(duplicate.lid, '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id ORDER BY duplicate.id LIMIT 1), survivor.lid),
    "avatarUrl" = COALESCE(NULLIF(survivor."avatarUrl", ''), (SELECT NULLIF(duplicate."avatarUrl", '') FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id ORDER BY duplicate.id LIMIT 1), survivor."avatarUrl"),
    "avatarUpdatedAt" = GREATEST(survivor."avatarUpdatedAt", (SELECT MAX(duplicate."avatarUpdatedAt") FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id)),
    "customFields" = COALESCE((SELECT jsonb_object_agg(field.key, field.value)
                     FROM public.contacts duplicate JOIN contact_company_merge map ON map.duplicate_id = duplicate.id
                     CROSS JOIN LATERAL jsonb_each(duplicate."customFields") field
                     WHERE map.survivor_id = survivor.id), '{}'::jsonb) || survivor."customFields"
WHERE EXISTS (SELECT 1 FROM contact_company_merge map WHERE map.survivor_id = survivor.id);

INSERT INTO public.contact_tag_assignments ("contactId", "tagId")
SELECT map.survivor_id, assignment."tagId"
FROM contact_company_merge map
JOIN public.contact_tag_assignments assignment ON assignment."contactId" = map.duplicate_id
ON CONFLICT DO NOTHING;

UPDATE public.conversations conversation
SET "contactId" = map.survivor_id
FROM contact_company_merge map
WHERE conversation."contactId" = map.duplicate_id;

DELETE FROM public.contact_tag_assignments assignment
USING contact_company_merge map
WHERE assignment."contactId" = map.duplicate_id;

DELETE FROM public.contacts contact
USING contact_company_merge map
WHERE contact.id = map.duplicate_id;

UPDATE public.conversations conversation
SET tags = ARRAY(SELECT tag.name FROM public.contact_tag_assignments assignment
                 JOIN public.company_tags tag ON tag.id = assignment."tagId"
                 WHERE assignment."contactId" = conversation."contactId" ORDER BY lower(tag.name)),
    "updatedAt" = now()
WHERE EXISTS (SELECT 1 FROM contact_company_merge map WHERE map.survivor_id = conversation."contactId");

ALTER TABLE public.contacts
    ADD CONSTRAINT contacts_company_fk FOREIGN KEY ("companyId") REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE public.contacts ALTER COLUMN "companyId" SET NOT NULL;

DROP INDEX IF EXISTS public.contacts_direct_number_unique;
DROP INDEX IF EXISTS public.contacts_company_merge_number_idx;
DROP INDEX IF EXISTS public.contacts_company_merge_jid_idx;
DROP INDEX IF EXISTS public.contacts_company_merge_lid_idx;
DROP INDEX IF EXISTS public.contacts_connection_idx;
DROP INDEX IF EXISTS public.contacts_connection_jid_lookup_idx;
DROP INDEX IF EXISTS public.contacts_connection_lid_lookup_idx;
ALTER TABLE public.contacts DROP COLUMN "connectionId";

CREATE INDEX contacts_company_idx ON public.contacts ("companyId");
CREATE UNIQUE INDEX contacts_company_direct_number_unique ON public.contacts ("companyId", number)
WHERE COALESCE("isGroup", false) = false AND number ~ '^[0-9]{8,20}$';
CREATE UNIQUE INDEX contacts_company_direct_jid_unique ON public.contacts ("companyId", jid)
WHERE COALESCE("isGroup", false) = false AND COALESCE(jid, '') <> '';
CREATE UNIQUE INDEX contacts_company_direct_lid_unique ON public.contacts ("companyId", lid)
WHERE COALESCE("isGroup", false) = false AND COALESCE(lid, '') <> '';
CREATE UNIQUE INDEX contacts_company_group_jid_unique ON public.contacts ("companyId", jid)
WHERE COALESCE("isGroup", false) = true AND COALESCE(jid, '') <> '';

-- +goose Down
DROP INDEX IF EXISTS public.contacts_company_group_jid_unique;
DROP INDEX IF EXISTS public.contacts_company_direct_lid_unique;
DROP INDEX IF EXISTS public.contacts_company_direct_jid_unique;
DROP INDEX IF EXISTS public.contacts_company_direct_number_unique;
DROP INDEX IF EXISTS public.contacts_company_idx;
ALTER TABLE public.contacts ADD COLUMN "connectionId" character varying;
ALTER TABLE public.contacts ALTER COLUMN "companyId" DROP NOT NULL;
ALTER TABLE public.contacts DROP CONSTRAINT IF EXISTS contacts_company_fk;
ALTER TABLE public.contacts DROP COLUMN "companyId";
