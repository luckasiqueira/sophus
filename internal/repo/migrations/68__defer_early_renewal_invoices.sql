-- +goose Up
CREATE INDEX subscriptions_renewal_due_idx ON public.subscriptions ("currentPeriodEnd", "companyId")
    WHERE status IN ('trialing', 'active', 'past_due') AND "priceCents" > 0;

-- Retire only automatic renewal invoices created too early by older versions.
-- Invoices with checkout identifiers, manual adjustments or reserved credit
-- must remain untouched so an externally payable link is never orphaned.
DELETE FROM public.payments payment
WHERE payment.status = 'pending' AND payment."dueDate" > now() + interval '10 days'
  AND payment."providerPaymentId" IS NULL AND payment."providerPreferenceId" IS NULL
  AND payment."checkoutURL" IS NULL AND payment."checkoutKey" IS NULL
  AND payment."checkoutCreating" = false AND payment."manualAmount" = false
  AND payment."creditAppliedCents" = 0
  AND NOT EXISTS (SELECT 1 FROM public.payment_fixed_entitlements fixed WHERE fixed."paymentId" = payment.id)
  AND NOT EXISTS (SELECT 1 FROM public.subscription_plan_changes change WHERE change."paymentId" = payment.id)
  AND EXISTS (SELECT 1 FROM public.payments paid WHERE paid."subscriptionId" = payment."subscriptionId" AND paid.status = 'paid');

-- An untouched trial invoice can be deferred when its trial lasts more than
-- ten days. Its original prorated terms are derived from the subscription.
DELETE FROM public.payments payment
USING public.subscriptions subscription
WHERE payment."subscriptionId" = subscription.id AND subscription.status = 'trialing'
  AND payment.status = 'pending' AND payment."dueDate" > now() + interval '10 days'
  AND payment."providerPaymentId" IS NULL AND payment."providerPreferenceId" IS NULL
  AND payment."checkoutURL" IS NULL AND payment."checkoutKey" IS NULL
  AND payment."checkoutCreating" = false AND payment."manualAmount" = false
  AND payment."creditAppliedCents" = 0
  AND EXISTS (SELECT 1 FROM public.payment_fixed_entitlements fixed WHERE fixed."paymentId" = payment.id)
  AND NOT EXISTS (SELECT 1 FROM public.payments other WHERE other."subscriptionId" = subscription.id AND other.id <> payment.id);

-- +goose Down
DROP INDEX IF EXISTS public.subscriptions_renewal_due_idx;
