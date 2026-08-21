-- +goose Up
CREATE TABLE public.flow_message_templates
(
    id          serial                      NOT NULL,
    "companyId" integer                     NOT NULL,
    name        character varying(100)      NOT NULL,
    content     text                        NOT NULL,
    revision    integer                     NOT NULL DEFAULT 1,
    "createdAt" timestamp with time zone    NOT NULL DEFAULT now(),
    "updatedAt" timestamp with time zone    NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT flow_message_templates_company_fk FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT flow_message_templates_content_check CHECK (char_length(content) BETWEEN 1 AND 10000)
);

CREATE UNIQUE INDEX flow_message_templates_company_name_unique
    ON public.flow_message_templates ("companyId", lower(name));

-- +goose Down
DROP TABLE IF EXISTS public.flow_message_templates;
