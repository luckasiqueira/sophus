-- +goose Up
ALTER TABLE public.departments
ADD COLUMN color character varying(7) NOT NULL DEFAULT '#6123CC',
ADD CONSTRAINT departments_color_check CHECK (color ~ '^#[0-9A-Fa-f]{6}$');

-- +goose Down
ALTER TABLE public.departments DROP CONSTRAINT IF EXISTS departments_color_check;
ALTER TABLE public.departments DROP COLUMN IF EXISTS color;
