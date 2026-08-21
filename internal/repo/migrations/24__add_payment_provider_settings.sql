-- +goose Up
CREATE TABLE public.platform_settings
(
    id                              smallint                 NOT NULL PRIMARY KEY,
    "paymentProvider"               character varying(40)    NOT NULL DEFAULT 'mercado_pago',
    "allowCompanyPaymentOverride"   boolean                  NOT NULL DEFAULT false,
    "defaultTimezone"               character varying        NOT NULL DEFAULT 'America/Sao_Paulo',
    "subscriptionGraceDays"         integer                  NOT NULL DEFAULT 0,
    "createdAt"                     timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt"                     timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT platform_settings_singleton_check CHECK (id = 1),
    CONSTRAINT platform_settings_provider_check
        CHECK ("paymentProvider" IN ('mercado_pago', 'abacate_pay')),
    CONSTRAINT platform_settings_grace_days_check
        CHECK ("subscriptionGraceDays" BETWEEN 0 AND 90)
);

INSERT INTO public.platform_settings (id) VALUES (1);

ALTER TABLE public.platform_admins
	ADD COLUMN "sessionVersion" integer NOT NULL DEFAULT 1,
	ADD CONSTRAINT platform_admins_session_version_check CHECK ("sessionVersion" > 0);

ALTER TABLE public.agents
	ADD COLUMN "sessionVersion" integer NOT NULL DEFAULT 1,
	ADD CONSTRAINT agents_session_version_check CHECK ("sessionVersion" > 0);

ALTER TABLE public.companies
    ADD COLUMN "paymentProviderOverride" character varying(40),
    ADD CONSTRAINT companies_payment_provider_override_check
        CHECK ("paymentProviderOverride" IS NULL OR "paymentProviderOverride" IN ('mercado_pago', 'abacate_pay'));

CREATE TABLE public.plan_provider_products
(
    "planId"             integer                  NOT NULL,
    provider             character varying(40)    NOT NULL,
    "amountCents"        bigint                   NOT NULL,
    "providerProductId"  character varying        NOT NULL,
    "createdAt"          timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt"          timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT plan_provider_products_pk PRIMARY KEY ("planId", provider, "amountCents"),
    CONSTRAINT plan_provider_products_plan_fk FOREIGN KEY ("planId")
        REFERENCES public.plans (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT plan_provider_products_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay')),
    CONSTRAINT plan_provider_products_amount_check CHECK ("amountCents" > 0)
);

CREATE TABLE public.provider_webhook_events
(
    provider    character varying(40)    NOT NULL,
    "eventId"   character varying         NOT NULL,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT provider_webhook_events_pk PRIMARY KEY (provider, "eventId"),
    CONSTRAINT provider_webhook_events_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay'))
);

ALTER TABLE public.payments
    ADD COLUMN "receiptURL" text,
	ADD COLUMN "checkoutLease" character varying(64),
    ADD CONSTRAINT payments_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay'));

CREATE UNIQUE INDEX payments_provider_preference_unique
    ON public.payments (provider, "providerPreferenceId")
    WHERE "providerPreferenceId" IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS public.payments_provider_preference_unique;

ALTER TABLE public.payments
    DROP CONSTRAINT IF EXISTS payments_provider_check,
	DROP COLUMN IF EXISTS "checkoutLease",
    DROP COLUMN IF EXISTS "receiptURL";

DROP TABLE IF EXISTS public.provider_webhook_events;
DROP TABLE IF EXISTS public.plan_provider_products;

ALTER TABLE public.platform_admins
	DROP CONSTRAINT IF EXISTS platform_admins_session_version_check,
	DROP COLUMN IF EXISTS "sessionVersion";

ALTER TABLE public.agents
	DROP CONSTRAINT IF EXISTS agents_session_version_check,
	DROP COLUMN IF EXISTS "sessionVersion";

ALTER TABLE public.companies
    DROP CONSTRAINT IF EXISTS companies_payment_provider_override_check,
    DROP COLUMN IF EXISTS "paymentProviderOverride";

DROP TABLE IF EXISTS public.platform_settings;
