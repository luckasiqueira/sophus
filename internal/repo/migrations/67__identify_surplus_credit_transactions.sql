-- +goose Up
ALTER TABLE public.company_credit_entries ADD COLUMN "providerTransactionId" text NOT NULL DEFAULT '';
UPDATE public.company_credit_entries entry
SET "providerTransactionId" = COALESCE(payment."providerPreferenceId", '')
FROM public.payments payment
WHERE entry."sourcePaymentId" = payment.id AND entry.kind IN ('credit', 'reversal');
DROP INDEX public.company_credit_source_once_idx;
CREATE UNIQUE INDEX company_credit_source_transaction_once_idx
    ON public.company_credit_entries ("sourcePaymentId", "providerTransactionId", kind)
    WHERE kind IN ('credit', 'reversal');

-- +goose Down
DROP INDEX public.company_credit_source_transaction_once_idx;
CREATE UNIQUE INDEX company_credit_source_once_idx ON public.company_credit_entries ("sourcePaymentId", kind)
    WHERE kind IN ('credit', 'reversal');
ALTER TABLE public.company_credit_entries DROP COLUMN "providerTransactionId";
