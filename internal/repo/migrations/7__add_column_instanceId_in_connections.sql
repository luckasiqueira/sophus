-- +goose Up
ALTER TABLE IF EXISTS public.connections
    ADD COLUMN "instanceId" uuid;