ALTER TABLE IF EXISTS public.tokens
    RENAME apitoken TO "apiToken";

ALTER TABLE IF EXISTS public.tokens
    ALTER COLUMN "apiToken" SET NOT NULL;

ALTER TABLE IF EXISTS public.tokens
    RENAME connectionkey TO "connectionKey";

ALTER TABLE IF EXISTS public.tokens
    ALTER COLUMN "connectionKey" SET NOT NULL;

ALTER TABLE IF EXISTS public.companies
    RENAME planid TO "planId";

ALTER TABLE IF EXISTS public.connections
    RENAME companyid TO "companyId";

ALTER TABLE IF EXISTS public.connections
    RENAME createdat TO "createdAt";

ALTER TABLE IF EXISTS public.logs_errors
    RENAME createdat TO "createdAt";

ALTER TABLE IF EXISTS public.webhooks
    RENAME connectionid TO "connectionId";