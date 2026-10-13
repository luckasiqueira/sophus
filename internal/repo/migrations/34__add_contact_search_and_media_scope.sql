-- +goose Up
ALTER TABLE public.messages
    ADD COLUMN "mediaScope" character varying(10) NOT NULL DEFAULT 'messages',
    ADD CONSTRAINT messages_media_scope_check CHECK ("mediaScope" IN ('messages', 'flows'));

CREATE INDEX contacts_connection_idx ON public.contacts ("connectionId");
CREATE INDEX contacts_name_ci_idx ON public.contacts (lower(name));
CREATE INDEX contacts_email_ci_idx ON public.contacts (lower(email));
CREATE INDEX contacts_number_digits_idx ON public.contacts ((regexp_replace(number, '[^0-9]', '', 'g')));
CREATE INDEX conversations_tags_gin_idx ON public.conversations USING gin (tags);

-- +goose Down
DROP INDEX IF EXISTS public.conversations_tags_gin_idx;
DROP INDEX IF EXISTS public.contacts_number_digits_idx;
DROP INDEX IF EXISTS public.contacts_email_ci_idx;
DROP INDEX IF EXISTS public.contacts_name_ci_idx;
DROP INDEX IF EXISTS public.contacts_connection_idx;
ALTER TABLE public.messages
    DROP CONSTRAINT IF EXISTS messages_media_scope_check,
    DROP COLUMN IF EXISTS "mediaScope";
