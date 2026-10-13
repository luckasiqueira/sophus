-- +goose Up
CREATE TABLE subscription_addons (
    "subscriptionId" integer PRIMARY KEY REFERENCES subscriptions(id) ON DELETE CASCADE,
    "companyId" integer NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    "basePriceCents" bigint NOT NULL CHECK ("basePriceCents">=0),
    quantities jsonb NOT NULL DEFAULT '{}', prices jsonb NOT NULL DEFAULT '{}',
    revision integer NOT NULL DEFAULT 0,
    "updatedAt" timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE subscription_addon_changes (
    "paymentId" integer PRIMARY KEY REFERENCES payments(id) ON DELETE CASCADE,
    "companyId" integer NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    "subscriptionId" integer NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    "planId" integer NOT NULL REFERENCES plans(id),
    "basePriceCents" bigint NOT NULL, "sourceRevision" integer NOT NULL,
    "sourceQuantities" jsonb NOT NULL, "targetQuantities" jsonb NOT NULL,
    "sourcePrices" jsonb NOT NULL, "targetPrices" jsonb NOT NULL,
    "sourcePriceCents" bigint NOT NULL, "targetPriceCents" bigint NOT NULL,
    "serviceEndsAt" timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','applied','canceled','refunded','credited')),
    "createdAt" timestamptz NOT NULL DEFAULT now(), "updatedAt" timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX subscription_addon_one_pending_idx ON subscription_addon_changes ("companyId") WHERE status='pending';
CREATE VIEW subscription_effective_limits AS
SELECT s.id AS "subscriptionId", s."companyId",
    p."agentLimit" + CASE WHEN p."allowAddons" THEN COALESCE((a.quantities->>'extraAgents')::integer,0) ELSE 0 END AS "agentLimit",
    p."connectionLimit" + CASE WHEN p."allowAddons" THEN COALESCE((a.quantities->>'extraConnections')::integer,0) ELSE 0 END AS "connectionLimit",
    p."storageLimitGB" + CASE WHEN p."allowAddons" THEN COALESCE((a.quantities->>'extraStorageGB')::integer,0) ELSE 0 END AS "storageLimitGB",
    p."allowAddons" AND COALESCE((a.quantities->>'audioTranscription')::boolean,false) AS "audioTranscription"
FROM subscriptions s JOIN plans p ON p.id=s."planId" LEFT JOIN subscription_addons a ON a."subscriptionId"=s.id;
-- +goose StatementBegin
CREATE FUNCTION cancel_pending_addon_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('canceled','failed') THEN
        UPDATE subscription_addon_changes SET status='canceled',"updatedAt"=now() WHERE "paymentId"=NEW.id AND status='pending';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER payments_cancel_addon_change AFTER UPDATE OF status ON payments FOR EACH ROW EXECUTE FUNCTION cancel_pending_addon_change();

-- +goose Down
DROP TRIGGER IF EXISTS payments_cancel_addon_change ON payments;
DROP FUNCTION IF EXISTS cancel_pending_addon_change();
DROP VIEW IF EXISTS subscription_effective_limits;
DROP TABLE subscription_addon_changes;
DROP TABLE subscription_addons;
