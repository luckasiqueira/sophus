-- +goose Up
-- Keeps a readable label for targets that are deleted after the event.
ALTER TABLE public.audit_events ADD COLUMN "targetName" text NOT NULL DEFAULT '';

-- Serves the failed-login notice shown after each agent login.
CREATE INDEX audit_events_agent_login_idx ON public.audit_events ("actorId", action, id DESC)
    WHERE "actorType" = 'agent' AND action IN ('login', 'login_failed');

-- +goose Down
DROP INDEX IF EXISTS public.audit_events_agent_login_idx;
ALTER TABLE public.audit_events DROP COLUMN IF EXISTS "targetName";
