-- +goose Up
-- Retries no longer move the time the user chose: they wait on "retryAt".
-- Recurring schedules create their next occurrence after each delivery; every
-- occurrence points at the first one ("seriesId") so the calendar stays
-- anchored to the original day and time.
ALTER TABLE public.scheduled_messages
    ADD COLUMN "retryAt" timestamp with time zone,
    ADD COLUMN recurrence character varying(10) NOT NULL DEFAULT 'none',
    ADD COLUMN "recurrenceUntil" date,
    ADD COLUMN "seriesId" bigint,
    ADD CONSTRAINT scheduled_messages_recurrence_check CHECK (recurrence IN ('none', 'daily', 'weekly', 'monthly')),
    ADD CONSTRAINT scheduled_messages_series_fk FOREIGN KEY ("seriesId")
        REFERENCES public.scheduled_messages (id) ON UPDATE CASCADE ON DELETE SET NULL;

DROP INDEX IF EXISTS public.scheduled_messages_due_idx;
CREATE INDEX scheduled_messages_due_idx
    ON public.scheduled_messages ((COALESCE("retryAt", "scheduledFor")), id)
    WHERE status = 'pending';

CREATE UNIQUE INDEX scheduled_messages_series_occurrence_unique
    ON public.scheduled_messages ("seriesId", "scheduledFor")
    WHERE "seriesId" IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS public.scheduled_messages_series_occurrence_unique;
DROP INDEX IF EXISTS public.scheduled_messages_due_idx;
ALTER TABLE public.scheduled_messages
    DROP CONSTRAINT IF EXISTS scheduled_messages_series_fk,
    DROP CONSTRAINT IF EXISTS scheduled_messages_recurrence_check,
    DROP COLUMN IF EXISTS "seriesId",
    DROP COLUMN IF EXISTS "recurrenceUntil",
    DROP COLUMN IF EXISTS recurrence,
    DROP COLUMN IF EXISTS "retryAt";
CREATE INDEX scheduled_messages_due_idx
    ON public.scheduled_messages ("scheduledFor", id)
    WHERE status = 'pending';
