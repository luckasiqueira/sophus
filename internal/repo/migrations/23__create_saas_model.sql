-- +goose Up
ALTER TABLE public.companies
    RENAME COLUMN isactive TO "isActive";

ALTER TABLE public.companies
    ADD COLUMN "updatedAt" timestamp with time zone NOT NULL DEFAULT now();

UPDATE public.companies
SET timezone = 'America/Sao_Paulo'
WHERE timezone IS NULL OR btrim(timezone) = '';

ALTER TABLE public.companies
    ALTER COLUMN timezone SET DEFAULT 'America/Sao_Paulo',
    ALTER COLUMN timezone SET NOT NULL;

CREATE TABLE public.platform_admins
(
    id          serial                   NOT NULL PRIMARY KEY,
    name        character varying(80)    NOT NULL,
    email       character varying        NOT NULL,
    password    character varying        NOT NULL,
    "isActive" boolean                  NOT NULL DEFAULT true,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX platform_admins_email_ci_unique
    ON public.platform_admins (lower(email));

CREATE UNIQUE INDEX platform_admins_singleton_unique
	ON public.platform_admins ((true));

CREATE TABLE public.plans
(
    id                serial                   NOT NULL PRIMARY KEY,
    name              character varying(80)    NOT NULL,
    description       text                     NOT NULL DEFAULT '',
    "priceCents"      bigint                   NOT NULL,
    "durationDays"    integer                  NOT NULL,
    "agentLimit"      integer                  NOT NULL,
    "connectionLimit" integer                  NOT NULL,
    "legacyPlanId"    integer,
    "isActive"        boolean                  NOT NULL DEFAULT true,
    "createdAt"       timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt"       timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT plans_price_check CHECK ("priceCents" >= 0),
    CONSTRAINT plans_duration_check CHECK ("durationDays" > 0),
    CONSTRAINT plans_agent_limit_check CHECK ("agentLimit" > 0),
    CONSTRAINT plans_connection_limit_check CHECK ("connectionLimit" > 0)
);

CREATE UNIQUE INDEX plans_name_ci_unique
    ON public.plans (lower(name));

CREATE UNIQUE INDEX plans_legacy_id_unique
    ON public.plans ("legacyPlanId")
    WHERE "legacyPlanId" IS NOT NULL;

CREATE TABLE public.subscriptions
(
    id                   serial                   NOT NULL PRIMARY KEY,
    "companyId"          integer                  NOT NULL,
    "planId"             integer                  NOT NULL,
    status                character varying(20)    NOT NULL,
    "priceCents"          bigint                   NOT NULL,
    "durationDays"        integer                  NOT NULL,
    "startsAt"            timestamp with time zone NOT NULL,
    "currentPeriodStart"  timestamp with time zone NOT NULL,
    "currentPeriodEnd"    timestamp with time zone NOT NULL,
    "basePeriodEnd"       timestamp with time zone NOT NULL,
    "canceledAt"          timestamp with time zone,
    "createdAt"           timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt"           timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT subscriptions_plan_fk FOREIGN KEY ("planId")
        REFERENCES public.plans (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT subscriptions_id_company_unique UNIQUE (id, "companyId"),
    CONSTRAINT subscriptions_status_check
        CHECK (status IN ('trialing', 'active', 'past_due', 'canceled', 'expired')),
    CONSTRAINT subscriptions_price_check CHECK ("priceCents" >= 0),
    CONSTRAINT subscriptions_duration_check CHECK ("durationDays" > 0),
    CONSTRAINT subscriptions_period_check CHECK ("currentPeriodEnd" > "currentPeriodStart")
);

CREATE UNIQUE INDEX subscriptions_company_current_unique
    ON public.subscriptions ("companyId")
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE INDEX subscriptions_company_history_idx
    ON public.subscriptions ("companyId", "createdAt" DESC);

CREATE INDEX subscriptions_plan_idx
    ON public.subscriptions ("planId");

CREATE TABLE public.payments
(
    id                     serial                   NOT NULL PRIMARY KEY,
    "companyId"            integer                  NOT NULL,
    "subscriptionId"       integer                  NOT NULL,
    status                  character varying(20)    NOT NULL,
    "amountCents"           bigint                   NOT NULL,
    "dueDate"               timestamp with time zone NOT NULL,
    provider                character varying(40)    NOT NULL DEFAULT 'mercado_pago',
    "providerPaymentId"     character varying,
    "providerPreferenceId"  character varying,
    "checkoutURL"           text,
    "checkoutKey"           character varying(64),
    "paidAt"                timestamp with time zone,
    "checkoutCreating"      boolean                  NOT NULL DEFAULT false,
    "createdAt"             timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt"             timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT payments_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT payments_subscription_company_fk FOREIGN KEY ("subscriptionId", "companyId")
        REFERENCES public.subscriptions (id, "companyId") ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT payments_status_check
        CHECK (status IN ('pending', 'processing', 'paid', 'failed', 'canceled', 'refunded')),
    CONSTRAINT payments_amount_check CHECK ("amountCents" >= 0)
);

CREATE INDEX payments_company_created_idx
    ON public.payments ("companyId", "createdAt" DESC);

CREATE INDEX payments_company_status_due_idx
    ON public.payments ("companyId", status, "dueDate");

CREATE INDEX payments_subscription_idx
    ON public.payments ("subscriptionId");

CREATE INDEX payments_open_due_idx
    ON public.payments ("dueDate")
    WHERE status IN ('pending', 'processing');

CREATE UNIQUE INDEX payments_provider_payment_unique
    ON public.payments (provider, "providerPaymentId")
    WHERE "providerPaymentId" IS NOT NULL;

CREATE INDEX agents_email_ci_idx ON public.agents (lower(email));

INSERT INTO public.plans
    (name, description, "priceCents", "durationDays", "agentLimit", "connectionLimit", "legacyPlanId", "isActive")
SELECT 'Plano legado ' || legacy."planId",
    'Plano criado automaticamente para preservar empresas existentes.',
    0,
    30,
    2147483647,
    2147483647,
    legacy."planId",
    true
FROM (SELECT DISTINCT "planId" FROM public.companies) legacy;

INSERT INTO public.subscriptions
    ("companyId", "planId", status, "priceCents", "durationDays", "startsAt", "currentPeriodStart", "currentPeriodEnd", "basePeriodEnd", "canceledAt")
SELECT c.id,
       legacy_plan.id,
       CASE
           WHEN legacy_dates.period_end < now() THEN 'past_due'
           ELSE 'active'
       END,
       0,
       legacy_plan."durationDays",
       legacy_dates.starts_at,
       legacy_dates.starts_at,
       legacy_dates.period_end,
       legacy_dates.period_end,
       NULL
FROM public.companies c
JOIN public.plans legacy_plan ON legacy_plan."legacyPlanId" = c."planId"
CROSS JOIN LATERAL (
    SELECT c."createdAt"::timestamp with time zone AS starts_at,
           CASE
               WHEN c."dueDate" IS NOT NULL
                    AND c."dueDate"::timestamp with time zone > c."createdAt"::timestamp with time zone
                   THEN c."dueDate"::timestamp with time zone
               ELSE GREATEST(now(), c."createdAt"::timestamp with time zone) + interval '30 days'
           END AS period_end
) legacy_dates;

ALTER TABLE public.companies
    DROP COLUMN "planId",
    DROP COLUMN "dueDate";

-- +goose Down
ALTER TABLE public.companies
    ADD COLUMN "planId" integer,
    ADD COLUMN "dueDate" timestamp without time zone;

UPDATE public.companies c
SET "planId" = COALESCE((
        SELECT COALESCE(p."legacyPlanId", p.id)
        FROM public.subscriptions s
        JOIN public.plans p ON p.id = s."planId"
        WHERE s."companyId" = c.id
        ORDER BY (s.status IN ('trialing', 'active', 'past_due')) DESC, s."createdAt" DESC
        LIMIT 1
    ), 0),
    "dueDate" = COALESCE((
        SELECT s."currentPeriodEnd"::timestamp without time zone
        FROM public.subscriptions s
        WHERE s."companyId" = c.id
        ORDER BY (s.status IN ('trialing', 'active', 'past_due')) DESC, s."createdAt" DESC
        LIMIT 1
    ), now() + interval '30 days');

ALTER TABLE public.companies
    ALTER COLUMN "planId" SET NOT NULL,
    ALTER COLUMN "dueDate" SET NOT NULL;

DROP INDEX IF EXISTS public.agents_email_ci_idx;
DROP TABLE IF EXISTS public.payments;
DROP TABLE IF EXISTS public.subscriptions;
DROP TABLE IF EXISTS public.plans;
DROP TABLE IF EXISTS public.platform_admins;

ALTER TABLE public.companies
    ALTER COLUMN timezone DROP NOT NULL,
    ALTER COLUMN timezone DROP DEFAULT;

ALTER TABLE public.companies
    DROP COLUMN "updatedAt";

ALTER TABLE public.companies
    RENAME COLUMN "isActive" TO isactive;
