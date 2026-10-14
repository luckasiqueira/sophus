-- +goose Up
-- Every Asaas payment link issued for an invoice, one per payment method the
-- payer chose. Links stay mapped after the invoice switches to another method
-- so charges already generated through them are still recognized.
CREATE TABLE public.asaas_payment_links
(
    "linkId"      character varying        NOT NULL,
    "paymentId"   integer                  NOT NULL,
    "billingType" character varying(20)    NOT NULL,
    "checkoutKey" character varying(64)    NOT NULL,
    "checkoutURL" text                     NOT NULL,
    "createdAt"   timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT asaas_payment_links_pk PRIMARY KEY ("linkId"),
    CONSTRAINT asaas_payment_links_payment_fk FOREIGN KEY ("paymentId")
        REFERENCES public.payments (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT asaas_payment_links_billing_type_check
        CHECK ("billingType" IN ('UNDEFINED', 'PIX', 'BOLETO', 'CREDIT_CARD'))
);

CREATE INDEX asaas_payment_links_payment_idx
    ON public.asaas_payment_links ("paymentId", "billingType", "createdAt" DESC);

-- +goose Down
DROP TABLE IF EXISTS public.asaas_payment_links;
