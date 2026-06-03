-- +goose Up
CREATE SEQUENCE IF NOT EXISTS contacts_id_seq;

CREATE TABLE IF NOT EXISTS public.contacts
(
    id integer NOT NULL DEFAULT nextval('contacts_id_seq'::regclass),
    name character varying COLLATE pg_catalog."default" NOT NULL,
    "number" character varying COLLATE pg_catalog."default" NOT NULL,
    "connectionId" character varying COLLATE pg_catalog."default",
    jid character varying COLLATE pg_catalog."default",
    lid character varying COLLATE pg_catalog."default",
    "isGroup" boolean,
    "isBlocked" boolean,
    CONSTRAINT contacts_pkey PRIMARY KEY (id)
);

-- +goose Down
DROP TABLE IF EXISTS public.contacts;
DROP SEQUENCE IF EXISTS contacts_id_seq;