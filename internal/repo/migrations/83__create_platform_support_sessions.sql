-- +goose Up
CREATE TABLE platform_support_sessions (
    id uuid PRIMARY KEY,
    "tokenHash" text NOT NULL UNIQUE,
    "adminId" integer NOT NULL REFERENCES platform_admins(id) ON DELETE CASCADE,
    "companyId" integer NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    "agentId" integer NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    "sessionVersion" integer NOT NULL,
    preferences jsonb NOT NULL,
    "createdAt" timestamptz NOT NULL DEFAULT now(),
    "expiresAt" timestamptz NOT NULL,
    "revokedAt" timestamptz
);
CREATE INDEX platform_support_sessions_admin_idx ON platform_support_sessions ("adminId", "expiresAt") WHERE "revokedAt" IS NULL;

-- +goose Down
DROP TABLE platform_support_sessions;
