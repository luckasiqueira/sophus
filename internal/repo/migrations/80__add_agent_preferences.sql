-- +goose Up
ALTER TABLE public.agents ADD COLUMN preferences jsonb NOT NULL DEFAULT '{
    "scrollAfterSend": true,
    "scrollAfterSendMode": "always",
    "messageSignatureEnabled": true,
    "messageSignatureTemplate": "{agentName}",
    "notifications": {"toast": true, "sound": true, "browser": true, "preview": true, "groups": true}
}'::jsonb;

-- Preserve the signature previously configured for each existing agent's company.
UPDATE public.agents agent SET preferences = agent.preferences || jsonb_build_object(
    'messageSignatureEnabled', COALESCE(company."operationSettings"->'messageSignatureEnabled', 'true'::jsonb),
    'messageSignatureTemplate', COALESCE(NULLIF(company."operationSettings"->>'messageSignatureTemplate', ''), '{agentName}')
) FROM public.companies company WHERE company.id = agent."companyId";

-- +goose Down
ALTER TABLE public.agents DROP COLUMN preferences;
