-- +goose Up
ALTER TABLE public.platform_settings
    DROP CONSTRAINT IF EXISTS platform_settings_provider_check,
    ADD CONSTRAINT platform_settings_provider_check
        CHECK ("paymentProvider" IN ('mercado_pago', 'abacate_pay', 'asaas'));

ALTER TABLE public.companies
    DROP CONSTRAINT IF EXISTS companies_payment_provider_override_check,
    ADD CONSTRAINT companies_payment_provider_override_check
        CHECK ("paymentProviderOverride" IS NULL OR "paymentProviderOverride" IN ('mercado_pago', 'abacate_pay', 'asaas'));

ALTER TABLE public.plan_provider_products
    DROP CONSTRAINT IF EXISTS plan_provider_products_provider_check,
    ADD CONSTRAINT plan_provider_products_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay', 'asaas'));

ALTER TABLE public.provider_webhook_events
    DROP CONSTRAINT IF EXISTS provider_webhook_events_provider_check,
    ADD CONSTRAINT provider_webhook_events_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay', 'asaas'));

ALTER TABLE public.payments
    DROP CONSTRAINT IF EXISTS payments_provider_check,
    ADD CONSTRAINT payments_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay', 'asaas'));

-- +goose Down
-- New charges fall back to Mercado Pago; Asaas invoices are kept as history,
-- so the payments constraint is restored without validating existing rows.
UPDATE public.platform_settings SET "paymentProvider" = 'mercado_pago', "updatedAt" = now()
WHERE "paymentProvider" = 'asaas';
UPDATE public.companies SET "paymentProviderOverride" = NULL
WHERE "paymentProviderOverride" = 'asaas';
DELETE FROM public.plan_provider_products WHERE provider = 'asaas';
DELETE FROM public.provider_webhook_events WHERE provider = 'asaas';

ALTER TABLE public.platform_settings
    DROP CONSTRAINT IF EXISTS platform_settings_provider_check,
    ADD CONSTRAINT platform_settings_provider_check
        CHECK ("paymentProvider" IN ('mercado_pago', 'abacate_pay'));

ALTER TABLE public.companies
    DROP CONSTRAINT IF EXISTS companies_payment_provider_override_check,
    ADD CONSTRAINT companies_payment_provider_override_check
        CHECK ("paymentProviderOverride" IS NULL OR "paymentProviderOverride" IN ('mercado_pago', 'abacate_pay'));

ALTER TABLE public.plan_provider_products
    DROP CONSTRAINT IF EXISTS plan_provider_products_provider_check,
    ADD CONSTRAINT plan_provider_products_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay'));

ALTER TABLE public.provider_webhook_events
    DROP CONSTRAINT IF EXISTS provider_webhook_events_provider_check,
    ADD CONSTRAINT provider_webhook_events_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay'));

ALTER TABLE public.payments
    DROP CONSTRAINT IF EXISTS payments_provider_check,
    ADD CONSTRAINT payments_provider_check
        CHECK (provider IN ('mercado_pago', 'abacate_pay')) NOT VALID;
