-- +goose Up
ALTER TABLE public.company_tags
    ADD COLUMN color character varying(20) NOT NULL DEFAULT 'purple',
    ADD CONSTRAINT company_tags_color_check
        CHECK (color IN ('purple', 'blue', 'teal', 'green', 'amber', 'orange', 'red', 'pink', 'gray'));

-- +goose Down
ALTER TABLE public.company_tags
    DROP CONSTRAINT IF EXISTS company_tags_color_check,
    DROP COLUMN IF EXISTS color;
