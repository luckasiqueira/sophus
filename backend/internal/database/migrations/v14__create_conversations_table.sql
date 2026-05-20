CREATE TABLE public.conversations
(
    id serial NOT NULL,
    status character varying NOT NULL,
    "contactId" integer NOT NULL,
    "connectionId" integer NOT NULL,
    "agentId" integer NOT NULL,
    url uuid NOT NULL,
    "createdAt" timestamp with time zone NOT NULL,
    "updatedAt" timestamp with time zone NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT connection FOREIGN KEY ("connectionId")
        REFERENCES public.connections (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID,
    CONSTRAINT "contactId" FOREIGN KEY ("contactId")
        REFERENCES public.contacts (id) MATCH SIMPLE
        ON UPDATE CASCADE
        ON DELETE CASCADE
        NOT VALID
);
