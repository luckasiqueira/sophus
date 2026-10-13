-- +goose Up
ALTER TABLE public.messages
ADD COLUMN sender character varying NOT NULL DEFAULT '';

CREATE TABLE public.message_reactions
(
    id bigserial PRIMARY KEY,
    "messageId" integer NOT NULL REFERENCES public.messages (id) ON DELETE CASCADE,
    sender character varying NOT NULL,
    emoji character varying(32) NOT NULL,
    "isFromMe" boolean NOT NULL DEFAULT false,
    "eventId" character varying NOT NULL DEFAULT '',
    "updatedAt" timestamp with time zone NOT NULL DEFAULT now(),
    UNIQUE ("messageId", sender)
);

CREATE INDEX message_reactions_message_id_idx ON public.message_reactions ("messageId");

-- +goose Down
DROP TABLE IF EXISTS public.message_reactions;
ALTER TABLE public.messages DROP COLUMN IF EXISTS sender;
