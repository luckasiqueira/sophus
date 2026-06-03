-- +goose Up
CREATE TABLE public.agents
(
    id serial NOT NULL,
    name character varying(40) NOT NULL,
    email character varying NOT NULL,
    password character varying NOT NULL,
    role character varying NOT NULL,
    "isActive" boolean NOT NULL,
    "companyId" integer NOT NULL,
    "createdAt" timestamp with time zone NOT NULL,
    "updatedAt" timestamp with time zone NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT company FOREIGN KEY ("companyId")
        REFERENCES public.companies (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID
);