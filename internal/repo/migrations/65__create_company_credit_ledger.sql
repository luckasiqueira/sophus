-- +goose Up
CREATE TABLE public.company_credit_balances (
    "companyId" integer PRIMARY KEY REFERENCES public.companies(id) ON DELETE CASCADE,
    "balanceCents" bigint NOT NULL DEFAULT 0,
    "updatedAt" timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE public.company_credit_entries (
    id bigserial PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON DELETE CASCADE,
    "sourcePaymentId" integer REFERENCES public.payments(id) ON DELETE SET NULL,
    "paymentId" integer REFERENCES public.payments(id) ON DELETE SET NULL,
    kind varchar(20) NOT NULL CHECK (kind IN ('credit', 'reversal', 'apply', 'release')),
    "amountCents" bigint NOT NULL CHECK ((kind = 'reversal' AND "amountCents" <= 0) OR (kind IN ('credit', 'release') AND "amountCents" > 0) OR (kind = 'apply' AND "amountCents" < 0)),
    "createdAt" timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX company_credit_source_once_idx ON public.company_credit_entries ("sourcePaymentId", kind)
    WHERE kind IN ('credit', 'reversal');
CREATE UNIQUE INDEX company_credit_payment_once_idx ON public.company_credit_entries ("paymentId", kind)
    WHERE kind IN ('apply', 'release');
CREATE INDEX company_credit_history_idx ON public.company_credit_entries ("companyId", id DESC);
ALTER TABLE public.payments ADD COLUMN "creditAppliedCents" bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE public.payments DROP COLUMN "creditAppliedCents";
DROP TABLE IF EXISTS public.company_credit_entries;
DROP TABLE IF EXISTS public.company_credit_balances;
