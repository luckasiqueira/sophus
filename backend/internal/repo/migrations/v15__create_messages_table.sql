CREATE TABLE public.messages
(
    id serial NOT NULL,
    "messageId" character varying NOT NULL,
    text text NOT NULL,
    "conversationId" integer NOT NULL,
    "quotedId" character varying NOT NULL,
    "mediaType" character varying NOT NULL,
    "fullData" json NOT NULL,
    "createdAt" timestamp with time zone NOT NULL,
    "updatedAt" timestamp with time zone NOT NULL,
    "isFromMe" boolean NOT NULL,
    "isGroup" boolean NOT NULL,
    "isRead" boolean NOT NULL,
    "isDeleted" boolean NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT connection FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) MATCH SIMPLE
        ON UPDATE NO ACTION
        ON DELETE NO ACTION
        NOT VALID
);
