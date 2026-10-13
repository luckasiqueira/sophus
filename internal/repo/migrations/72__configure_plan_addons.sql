-- +goose Up
ALTER TABLE plans ADD COLUMN "allowAddons" boolean NOT NULL DEFAULT false;
ALTER TABLE platform_settings
    ADD COLUMN "extraConnectionPriceCents" bigint NOT NULL DEFAULT 0 CHECK ("extraConnectionPriceCents" >= 0),
    ADD COLUMN "extraAgentPriceCents" bigint NOT NULL DEFAULT 0 CHECK ("extraAgentPriceCents" >= 0),
    ADD COLUMN "extraStorageGBPriceCents" bigint NOT NULL DEFAULT 0 CHECK ("extraStorageGBPriceCents" >= 0),
    ADD COLUMN "audioTranscriptionPriceCents" bigint NOT NULL DEFAULT 1490 CHECK ("audioTranscriptionPriceCents" >= 0);

-- +goose Down
ALTER TABLE platform_settings
    DROP COLUMN "extraConnectionPriceCents",
    DROP COLUMN "extraAgentPriceCents",
    DROP COLUMN "extraStorageGBPriceCents",
    DROP COLUMN "audioTranscriptionPriceCents";
ALTER TABLE plans DROP COLUMN "allowAddons";
