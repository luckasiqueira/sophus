-- +goose Up
CREATE TABLE public.departments
(
    id          serial                   NOT NULL,
    name        character varying(80)    NOT NULL,
    "companyId" integer                  NOT NULL,
    "isActive" boolean                  NOT NULL DEFAULT true,
    "createdAt" timestamp with time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT departments_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE UNIQUE INDEX departments_company_name_unique
    ON public.departments ("companyId", lower(name));

CREATE TABLE public.agent_departments
(
    "agentId"      integer NOT NULL,
    "departmentId" integer NOT NULL,
    PRIMARY KEY ("agentId", "departmentId"),
    CONSTRAINT agent_departments_agent_fk FOREIGN KEY ("agentId")
        REFERENCES public.agents (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT agent_departments_department_fk FOREIGN KEY ("departmentId")
        REFERENCES public.departments (id) ON UPDATE CASCADE ON DELETE CASCADE
);

ALTER TABLE public.conversations ALTER COLUMN "agentId" DROP NOT NULL;
UPDATE public.conversations SET "agentId" = NULL WHERE "agentId" = 0;
ALTER TABLE public.conversations ADD COLUMN "departmentId" integer;

UPDATE public.conversations cv
SET "agentId" = NULL
WHERE "agentId" IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM public.agents a WHERE a.id = cv."agentId");

ALTER TABLE public.conversations
    ADD CONSTRAINT conversations_agent_fk FOREIGN KEY ("agentId")
        REFERENCES public.agents (id) ON UPDATE CASCADE ON DELETE SET NULL,
    ADD CONSTRAINT conversations_department_fk FOREIGN KEY ("departmentId")
        REFERENCES public.departments (id) ON UPDATE CASCADE ON DELETE SET NULL;

UPDATE public.conversations cv
SET status = 'pending', "updatedAt" = now()
WHERE cv."agentId" IS NULL
  AND cv.status NOT IN ('closed', 'running')
  AND NOT EXISTS (
      SELECT 1 FROM public.flow_executions fe
      WHERE fe."conversationId" = cv.id AND fe.status IN ('running', 'waiting')
  );

CREATE INDEX conversations_assignment_idx
    ON public.conversations (status, "agentId", "departmentId", "updatedAt");

-- +goose Down
DROP INDEX IF EXISTS public.conversations_assignment_idx;
ALTER TABLE public.conversations DROP CONSTRAINT IF EXISTS conversations_department_fk;
ALTER TABLE public.conversations DROP CONSTRAINT IF EXISTS conversations_agent_fk;
ALTER TABLE public.conversations DROP COLUMN IF EXISTS "departmentId";
UPDATE public.conversations SET "agentId" = 0 WHERE "agentId" IS NULL;
ALTER TABLE public.conversations ALTER COLUMN "agentId" SET NOT NULL;
DROP TABLE IF EXISTS public.agent_departments;
DROP INDEX IF EXISTS public.departments_company_name_unique;
DROP TABLE IF EXISTS public.departments;
