-- +goose Up
ALTER TABLE IF EXISTS public.messages DROP COLUMN IF EXISTS "fullData";

ALTER TABLE IF EXISTS public.messages
    ADD COLUMN "mediaPath" character varying;