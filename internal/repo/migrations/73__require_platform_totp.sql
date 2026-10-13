-- +goose Up
ALTER TABLE platform_admins
    ADD COLUMN "totpSecretCiphertext" text NOT NULL DEFAULT '',
    ADD COLUMN "totpLastUsedStep" bigint NOT NULL DEFAULT -1;

CREATE TABLE platform_login_challenges (
    "tokenHash" text PRIMARY KEY,
    "adminId" integer NOT NULL REFERENCES platform_admins(id) ON DELETE CASCADE,
    "sessionVersion" integer NOT NULL,
    "secretCiphertext" text NOT NULL,
    enrollment boolean NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    "expiresAt" timestamptz NOT NULL
);
CREATE INDEX platform_login_challenges_expiry_idx ON platform_login_challenges ("expiresAt");

-- +goose Down
DROP TABLE platform_login_challenges;
ALTER TABLE platform_admins DROP COLUMN "totpSecretCiphertext", DROP COLUMN "totpLastUsedStep";
