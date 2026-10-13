-- +goose Up
CREATE TABLE public.payment_fixed_entitlements (
    "paymentId" integer PRIMARY KEY REFERENCES public.payments(id) ON UPDATE CASCADE ON DELETE CASCADE,
    "periodEnd" timestamp with time zone NOT NULL,
    "renewalPaymentId" integer UNIQUE REFERENCES public.payments(id) ON UPDATE CASCADE ON DELETE SET NULL,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS public.payment_fixed_entitlements;
