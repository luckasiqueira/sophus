-- +goose Up
ALTER TABLE public.payments
    ADD CONSTRAINT payments_id_company_subscription_unique UNIQUE (id, "companyId", "subscriptionId");

CREATE TABLE public.subscription_plan_changes (
    "paymentId" integer PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "sourceSubscriptionId" integer NOT NULL REFERENCES public.subscriptions(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    "targetPlanId" integer NOT NULL REFERENCES public.plans(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    "billingCycle" character varying(20) NOT NULL,
    "priceCents" bigint NOT NULL,
    "durationDays" integer NOT NULL,
    status character varying(20) NOT NULL DEFAULT 'pending',
    "targetSubscriptionId" integer,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT subscription_plan_changes_cycle_check CHECK ("billingCycle" IN ('monthly', 'annual')),
    CONSTRAINT subscription_plan_changes_price_check CHECK ("priceCents" >= 0),
    CONSTRAINT subscription_plan_changes_duration_check CHECK ("durationDays" > 0),
    CONSTRAINT subscription_plan_changes_status_check CHECK (status IN ('pending', 'applied', 'canceled')),
    CONSTRAINT subscription_plan_changes_payment_source_fk
        FOREIGN KEY ("paymentId", "companyId", "sourceSubscriptionId")
        REFERENCES public.payments(id, "companyId", "subscriptionId") ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT subscription_plan_changes_target_subscription_fk
        FOREIGN KEY ("targetSubscriptionId", "companyId")
        REFERENCES public.subscriptions(id, "companyId") ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE UNIQUE INDEX subscription_plan_changes_company_pending_unique
    ON public.subscription_plan_changes ("companyId") WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS public.subscription_plan_changes;
ALTER TABLE public.payments DROP CONSTRAINT IF EXISTS payments_id_company_subscription_unique;
