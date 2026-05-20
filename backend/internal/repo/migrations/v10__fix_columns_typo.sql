ALTER TABLE IF EXISTS public.companies
    RENAME createdat TO "createdAt";

ALTER TABLE IF EXISTS public.companies
    RENAME duedate TO "dueDate";