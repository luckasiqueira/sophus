-- +goose Up
-- Repair rows created when Evolution returned PN JID and LID in reversed fields.
UPDATE public.contacts
SET number = split_part(split_part(lid, '@', 1), ':', 1),
    jid = split_part(split_part(lid, '@', 1), ':', 1) || '@s.whatsapp.net',
    lid = split_part(split_part(jid, '@', 1), ':', 1) || '@lid'
WHERE COALESCE("isGroup", false) = false
  AND lower(COALESCE(jid, '')) LIKE '%@lid'
  AND lower(COALESCE(lid, '')) LIKE '%@s.whatsapp.net';

-- Keep LID-only identities out of the telephone-number field until a PN JID is observed.
UPDATE public.contacts
SET lid = split_part(split_part(jid, '@', 1), ':', 1) || '@lid', jid = '', number = ''
WHERE COALESCE("isGroup", false) = false
  AND lower(COALESCE(jid, '')) LIKE '%@lid'
  AND lower(COALESCE(lid, '')) NOT LIKE '%@s.whatsapp.net';

UPDATE public.contacts
SET jid = split_part(split_part(jid, '@', 1), ':', 1) || '@s.whatsapp.net',
    number = split_part(split_part(jid, '@', 1), ':', 1)
WHERE COALESCE("isGroup", false) = false
  AND lower(COALESCE(jid, '')) LIKE '%@s.whatsapp.net';

UPDATE public.contacts
SET lid = split_part(split_part(lid, '@', 1), ':', 1) || '@lid'
WHERE COALESCE("isGroup", false) = false
  AND lower(COALESCE(lid, '')) LIKE '%@lid';

CREATE TEMP TABLE contact_identity_merge ON COMMIT DROP AS
SELECT id AS duplicate_id,
       first_value(id) OVER (
           PARTITION BY "connectionId", number
           ORDER BY CASE WHEN BTRIM(name) <> '' AND name <> 'Contato salvo automaticamente' THEN 0 ELSE 1 END, id
       ) AS survivor_id,
       row_number() OVER (
           PARTITION BY "connectionId", number
           ORDER BY CASE WHEN BTRIM(name) <> '' AND name <> 'Contato salvo automaticamente' THEN 0 ELSE 1 END, id
       ) AS position
FROM public.contacts
WHERE COALESCE("isGroup", false) = false AND number ~ '^[0-9]{8,20}$';

DELETE FROM contact_identity_merge WHERE position = 1;

UPDATE public.contacts survivor
SET name = COALESCE((SELECT duplicate.name FROM public.contacts duplicate
                     JOIN contact_identity_merge map ON map.duplicate_id = duplicate.id
                     WHERE map.survivor_id = survivor.id AND BTRIM(duplicate.name) <> ''
                       AND duplicate.name <> 'Contato salvo automaticamente' ORDER BY duplicate.id LIMIT 1), survivor.name),
    jid = COALESCE(NULLIF(survivor.jid, ''), (SELECT NULLIF(duplicate.jid, '') FROM public.contacts duplicate
          JOIN contact_identity_merge map ON map.duplicate_id = duplicate.id
          WHERE map.survivor_id = survivor.id AND NULLIF(duplicate.jid, '') IS NOT NULL ORDER BY duplicate.id LIMIT 1)),
    lid = COALESCE(NULLIF(survivor.lid, ''), (SELECT NULLIF(duplicate.lid, '') FROM public.contacts duplicate
          JOIN contact_identity_merge map ON map.duplicate_id = duplicate.id
          WHERE map.survivor_id = survivor.id AND NULLIF(duplicate.lid, '') IS NOT NULL ORDER BY duplicate.id LIMIT 1))
WHERE EXISTS (SELECT 1 FROM contact_identity_merge map WHERE map.survivor_id = survivor.id);

INSERT INTO public.contact_tag_assignments ("contactId", "tagId")
SELECT map.survivor_id, assignment."tagId"
FROM contact_identity_merge map
JOIN public.contact_tag_assignments assignment ON assignment."contactId" = map.duplicate_id
ON CONFLICT DO NOTHING;

DELETE FROM public.contact_tag_assignments assignment
USING contact_identity_merge map
WHERE assignment."contactId" = map.duplicate_id;

UPDATE public.conversations conversation
SET "contactId" = map.survivor_id
FROM contact_identity_merge map
WHERE conversation."contactId" = map.duplicate_id;

DELETE FROM public.contacts contact
USING contact_identity_merge map
WHERE contact.id = map.duplicate_id;

CREATE UNIQUE INDEX contacts_direct_number_unique
ON public.contacts ("connectionId", number)
WHERE COALESCE("isGroup", false) = false AND number ~ '^[0-9]{8,20}$';

-- +goose Down
DROP INDEX IF EXISTS public.contacts_direct_number_unique;
