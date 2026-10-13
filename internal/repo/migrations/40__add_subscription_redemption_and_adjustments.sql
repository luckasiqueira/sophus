-- +goose Up
ALTER TABLE public.subscriptions
    ADD COLUMN "redemptionEndsAt" timestamp with time zone;

CREATE TABLE public.subscription_adjustments (
    id bigserial PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "subscriptionId" integer NOT NULL REFERENCES public.subscriptions(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "platformAdminId" integer REFERENCES public.platform_admins(id) ON UPDATE CASCADE ON DELETE SET NULL,
    action character varying(30) NOT NULL,
    days integer NOT NULL,
    reason character varying(500) NOT NULL,
    "previousStatus" character varying(20) NOT NULL,
    "newStatus" character varying(20) NOT NULL,
    "previousPeriodEnd" timestamp with time zone NOT NULL,
    "newPeriodEnd" timestamp with time zone NOT NULL,
    "redemptionEndsAt" timestamp with time zone,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT subscription_adjustments_action_check CHECK (action IN ('reactivate', 'redemption')),
    CONSTRAINT subscription_adjustments_days_check CHECK (days BETWEEN 1 AND 3650)
);

CREATE INDEX subscription_adjustments_company_created_idx
    ON public.subscription_adjustments ("companyId", "createdAt" DESC);

-- +goose Down
DROP TABLE IF EXISTS public.subscription_adjustments;
ALTER TABLE public.subscriptions DROP COLUMN IF EXISTS "redemptionEndsAt";
