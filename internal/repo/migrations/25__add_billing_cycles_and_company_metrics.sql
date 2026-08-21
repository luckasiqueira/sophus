-- +goose Up
LOCK TABLE public.plans, public.subscriptions, public.payments IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.plans WHERE "durationDays" NOT IN (30, 365)) THEN
        RAISE EXCEPTION 'Existem planos com duração diferente de 30 ou 365 dias; ajuste-os antes da migration 25';
    END IF;
    IF EXISTS (SELECT 1 FROM public.subscriptions WHERE "durationDays" NOT IN (30, 365)) THEN
        RAISE EXCEPTION 'Existem assinaturas com duração diferente de 30 ou 365 dias; ajuste-as antes da migration 25';
    END IF;
    IF EXISTS (SELECT 1 FROM public.plans WHERE "durationDays" = 30 AND "priceCents" > 768614336404564650) THEN
        RAISE EXCEPTION 'Existe preço mensal alto demais para conversão anual';
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE public.plans
    ADD COLUMN "monthlyPriceCents" bigint,
    ADD COLUMN "annualPriceCents" bigint;

UPDATE public.plans
SET "monthlyPriceCents" = CASE
        WHEN "durationDays" = 365 AND "priceCents" > 0 THEN GREATEST(1, ROUND("priceCents"::numeric / 12)::bigint)
        WHEN "durationDays" = 365 THEN 0
        ELSE "priceCents"
    END,
    "annualPriceCents" = CASE WHEN "durationDays" = 365 THEN "priceCents" ELSE "priceCents" * 12 END;

ALTER TABLE public.plans
    ALTER COLUMN "monthlyPriceCents" SET NOT NULL,
    ALTER COLUMN "annualPriceCents" SET NOT NULL,
    ADD CONSTRAINT plans_monthly_price_check CHECK ("monthlyPriceCents" >= 0),
    ADD CONSTRAINT plans_annual_price_check CHECK ("annualPriceCents" >= 0);

ALTER TABLE public.subscriptions
    ADD COLUMN "billingCycle" character varying(10) NOT NULL DEFAULT 'monthly',
    ADD COLUMN "previousSubscriptionId" integer,
    ADD COLUMN "periodRevision" integer NOT NULL DEFAULT 0;

ALTER TABLE public.payments
    ADD COLUMN "entitlementRevision" integer NOT NULL DEFAULT 0;

UPDATE public.subscriptions
SET "billingCycle" = CASE WHEN "durationDays" = 365 THEN 'annual' ELSE 'monthly' END;

ALTER TABLE public.subscriptions
	ADD CONSTRAINT subscriptions_billing_cycle_check CHECK (
		("billingCycle" = 'monthly' AND "durationDays" = 30)
		OR ("billingCycle" = 'annual' AND "durationDays" = 365)
	),
    ADD CONSTRAINT subscriptions_previous_fk FOREIGN KEY ("previousSubscriptionId", "companyId")
        REFERENCES public.subscriptions (id, "companyId") ON UPDATE CASCADE ON DELETE RESTRICT;

CREATE INDEX subscriptions_previous_idx
    ON public.subscriptions ("previousSubscriptionId")
    WHERE "previousSubscriptionId" IS NOT NULL;

CREATE INDEX conversations_connection_idx
    ON public.conversations ("connectionId", id);

CREATE INDEX messages_created_conversation_idx
    ON public.messages ("conversationId", "createdAt")
    INCLUDE ("isFromMe", "isDeleted");

CREATE INDEX chatbot_flows_company_created_idx
    ON public.chatbot_flows ("companyId", "createdAt" DESC);

CREATE INDEX flow_executions_company_flow_created_idx
    ON public.flow_executions ("companyId", "flowId", "createdAt" DESC);

-- +goose Down
LOCK TABLE public.plans, public.subscriptions, public.payments IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM public.plans WHERE NOT (
		("durationDays" = 30 AND "monthlyPriceCents" = "priceCents"
			AND "annualPriceCents"::numeric = "priceCents"::numeric * 12)
		OR ("durationDays" = 365 AND "annualPriceCents" = "priceCents"
			AND "monthlyPriceCents" = CASE WHEN "priceCents" > 0 THEN GREATEST(1, ROUND("priceCents"::numeric / 12)::bigint) ELSE 0 END)
	)) THEN
        RAISE EXCEPTION 'Não é possível reverter a migration 25 sem perder preços anuais independentes';
    END IF;
	IF EXISTS (SELECT 1 FROM public.subscriptions WHERE "previousSubscriptionId" IS NOT NULL OR "periodRevision" <> 0) THEN
        RAISE EXCEPTION 'Não é possível reverter a migration 25 sem perder o histórico de períodos das assinaturas';
    END IF;
	IF EXISTS (SELECT 1 FROM public.payments WHERE "entitlementRevision" <> 0) THEN
		RAISE EXCEPTION 'Não é possível reverter a migration 25 sem perder revisões de pagamentos';
	END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX IF EXISTS public.subscriptions_previous_idx;
DROP INDEX IF EXISTS public.flow_executions_company_flow_created_idx;
DROP INDEX IF EXISTS public.chatbot_flows_company_created_idx;
DROP INDEX IF EXISTS public.messages_created_conversation_idx;
DROP INDEX IF EXISTS public.conversations_connection_idx;

ALTER TABLE public.subscriptions
    DROP CONSTRAINT IF EXISTS subscriptions_billing_cycle_check,
    DROP CONSTRAINT IF EXISTS subscriptions_previous_fk,
    DROP COLUMN IF EXISTS "previousSubscriptionId",
	DROP COLUMN IF EXISTS "periodRevision",
    DROP COLUMN IF EXISTS "billingCycle";

ALTER TABLE public.payments
	DROP COLUMN IF EXISTS "entitlementRevision";

ALTER TABLE public.plans
    DROP CONSTRAINT plans_monthly_price_check,
    DROP CONSTRAINT plans_annual_price_check,
    DROP COLUMN "monthlyPriceCents",
    DROP COLUMN "annualPriceCents";
