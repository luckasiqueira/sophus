-- +goose Up
ALTER TABLE public.conversations ALTER COLUMN "connectionId" DROP NOT NULL;
ALTER TABLE public.conversations DROP CONSTRAINT IF EXISTS connection;
ALTER TABLE public.conversations
    ADD CONSTRAINT conversations_connection_fk FOREIGN KEY ("connectionId")
    REFERENCES public.connections(id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +goose Down
DELETE FROM public.conversations WHERE "connectionId" IS NULL;
ALTER TABLE public.conversations DROP CONSTRAINT IF EXISTS conversations_connection_fk;
ALTER TABLE public.conversations ALTER COLUMN "connectionId" SET NOT NULL;
ALTER TABLE public.conversations
    ADD CONSTRAINT connection FOREIGN KEY ("connectionId")
    REFERENCES public.connections(id) ON UPDATE CASCADE ON DELETE CASCADE;
