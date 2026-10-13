-- +goose Up
-- Requeue avatars marked complete when the backend exceeded the previous five-second timeout.
UPDATE public.contacts
SET "avatarRefreshRequestedAt" = now(), "avatarUpdatedAt" = NULL
WHERE COALESCE("isGroup", false) = false
  AND BTRIM(number) <> ''
  AND BTRIM(COALESCE("avatarUrl", '')) = ''
  AND "avatarUpdatedAt" IS NOT NULL;

-- +goose Down
