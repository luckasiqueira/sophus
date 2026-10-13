-- +goose Up
CREATE TABLE public.audit_events (
    id bigserial PRIMARY KEY,
    "companyId" integer,
    "actorId" integer,
    "actorEmail" text NOT NULL DEFAULT '',
    "actorType" text NOT NULL,
    action text NOT NULL,
    "targetType" text NOT NULL DEFAULT '',
    "targetId" text NOT NULL DEFAULT '',
    "ipAddress" text NOT NULL DEFAULT '',
    "createdAt" timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_company_time_idx ON public.audit_events ("companyId", id DESC);
CREATE INDEX audit_events_actor_time_idx ON public.audit_events ("actorEmail", id DESC);

-- +goose Down
DROP TABLE IF EXISTS public.audit_events;
