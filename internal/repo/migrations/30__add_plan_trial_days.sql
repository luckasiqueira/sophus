-- +goose Up
ALTER TABLE public.plans
    ADD COLUMN "trialDays" integer NOT NULL DEFAULT 10,
    ADD CONSTRAINT plans_trial_days_check CHECK ("trialDays" BETWEEN 0 AND 90);

-- +goose Down
ALTER TABLE public.plans
    DROP CONSTRAINT IF EXISTS plans_trial_days_check,
    DROP COLUMN IF EXISTS "trialDays";
