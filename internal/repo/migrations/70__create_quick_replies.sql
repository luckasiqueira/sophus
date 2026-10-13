-- +goose Up
CREATE TABLE public.quick_replies (
    id serial PRIMARY KEY,
    "companyId" integer NOT NULL REFERENCES public.companies(id) ON UPDATE CASCADE ON DELETE CASCADE,
    shortcut varchar(40) NOT NULL CHECK (shortcut ~ '^[a-z0-9][a-z0-9_-]{0,39}$'),
    title varchar(100) NOT NULL CHECK (char_length(trim(title)) > 0),
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 10000),
    revision integer NOT NULL DEFAULT 1,
    "createdAt" timestamptz NOT NULL DEFAULT now(),
    "updatedAt" timestamptz NOT NULL DEFAULT now(),
    UNIQUE ("companyId", shortcut)
);

-- +goose Down
DROP TABLE public.quick_replies;
