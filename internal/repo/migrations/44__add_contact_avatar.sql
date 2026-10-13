-- +goose Up
ALTER TABLE public.contacts
    ADD COLUMN "avatarUrl" text NOT NULL DEFAULT '',
    ADD COLUMN "avatarRefreshRequestedAt" timestamp with time zone,
    ADD COLUMN "avatarUpdatedAt" timestamp with time zone;

CREATE INDEX contacts_avatar_refresh_idx ON public.contacts ("avatarRefreshRequestedAt", id)
    WHERE "avatarRefreshRequestedAt" IS NOT NULL
      AND ("avatarUpdatedAt" IS NULL OR "avatarUpdatedAt" < "avatarRefreshRequestedAt");

-- +goose Down
DROP INDEX IF EXISTS public.contacts_avatar_refresh_idx;
ALTER TABLE public.contacts
    DROP COLUMN IF EXISTS "avatarUpdatedAt",
    DROP COLUMN IF EXISTS "avatarRefreshRequestedAt",
    DROP COLUMN IF EXISTS "avatarUrl";
