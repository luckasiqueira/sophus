-- +goose Up
ALTER TABLE public.messages DROP CONSTRAINT IF EXISTS connection;
ALTER TABLE public.messages
    ADD CONSTRAINT messages_conversation_fk FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id)
        ON UPDATE NO ACTION
        ON DELETE CASCADE;

-- +goose Down
ALTER TABLE public.messages DROP CONSTRAINT IF EXISTS messages_conversation_fk;
ALTER TABLE public.messages
    ADD CONSTRAINT connection FOREIGN KEY ("conversationId")
        REFERENCES public.conversations (id) MATCH SIMPLE
        ON UPDATE NO ACTION
        ON DELETE NO ACTION
        NOT VALID;
