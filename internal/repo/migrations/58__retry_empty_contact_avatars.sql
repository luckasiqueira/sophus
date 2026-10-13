-- +goose Up
-- Retry contacts previously marked complete while the profile-picture endpoint was misconfigured.
UPDATE public.contacts
SET "avatarRefreshRequestedAt" = now(), "avatarUpdatedAt" = NULL
WHERE COALESCE("isGroup", false) = false
  AND BTRIM(number) <> ''
  AND BTRIM(COALESCE("avatarUrl", '')) = '';

-- +goose Down
