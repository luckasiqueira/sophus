-- +goose Up
-- Only signatures still on the previous default change; custom templates stay.
UPDATE public.agents
SET preferences = jsonb_set(preferences, '{messageSignatureTemplate}', '"*{agentName}*"')
WHERE preferences->>'messageSignatureTemplate' = '{agentName}';

UPDATE public.companies
SET "operationSettings" = jsonb_set("operationSettings", '{messageSignatureTemplate}', '"*{agentName}*"')
WHERE "operationSettings"->>'messageSignatureTemplate' = '{agentName}';

UPDATE public.platform_support_sessions
SET preferences = jsonb_set(preferences, '{messageSignatureTemplate}', '"*{agentName}*"')
WHERE preferences->>'messageSignatureTemplate' = '{agentName}';

ALTER TABLE public.agents ALTER COLUMN preferences SET DEFAULT '{
    "scrollAfterSend": true,
    "scrollAfterSendMode": "always",
    "messageSignatureEnabled": true,
    "messageSignatureTemplate": "*{agentName}*",
    "notifications": {"toast": true, "sound": true, "browser": true, "preview": true, "groups": true}
}'::jsonb;

ALTER TABLE public.companies ALTER COLUMN "operationSettings" SET DEFAULT jsonb_set(
    '{
        "messageSignatureEnabled": true,
        "messageSignatureTemplate": "{agentName}",
        "acceptCalls": false,
        "acceptGroupMessages": false,
        "flowsForAllContacts": true,
        "platformContactsOnly": false,
        "businessHoursEnabled": false,
        "businessHoursStart": "08:00",
        "businessHoursEnd": "18:00",
        "businessHoursDays": [1, 2, 3, 4, 5],
        "businessHoursLunchEnabled": false,
        "businessHoursLunchStart": "12:00",
        "businessHoursLunchEnd": "13:00",
        "outsideHoursMessage": "",
        "welcomeMessage": "",
        "autoAssignEnabled": false,
        "autoAssignStrategy": "least_loaded",
        "autoCloseMinutes": 0,
        "conversationRetentionDays": 0,
        "allowedAttachmentTypes": ["image", "video", "audio", "document"],
        "maxAttachmentMB": 50,
        "notifyNewConversation": true,
        "notifyMentions": true,
        "notifyUnanswered": true,
        "outboundMessagesEnabled": true
    }'::jsonb, '{messageSignatureTemplate}', '"*{agentName}*"');

-- +goose Down
UPDATE public.agents
SET preferences = jsonb_set(preferences, '{messageSignatureTemplate}', '"{agentName}"')
WHERE preferences->>'messageSignatureTemplate' = '*{agentName}*';

UPDATE public.companies
SET "operationSettings" = jsonb_set("operationSettings", '{messageSignatureTemplate}', '"{agentName}"')
WHERE "operationSettings"->>'messageSignatureTemplate' = '*{agentName}*';

UPDATE public.platform_support_sessions
SET preferences = jsonb_set(preferences, '{messageSignatureTemplate}', '"{agentName}"')
WHERE preferences->>'messageSignatureTemplate' = '*{agentName}*';

ALTER TABLE public.agents ALTER COLUMN preferences SET DEFAULT '{
    "scrollAfterSend": true,
    "scrollAfterSendMode": "always",
    "messageSignatureEnabled": true,
    "messageSignatureTemplate": "{agentName}",
    "notifications": {"toast": true, "sound": true, "browser": true, "preview": true, "groups": true}
}'::jsonb;

ALTER TABLE public.companies ALTER COLUMN "operationSettings" SET DEFAULT jsonb_set(
    '{
        "messageSignatureEnabled": true,
        "messageSignatureTemplate": "{agentName}",
        "acceptCalls": false,
        "acceptGroupMessages": false,
        "flowsForAllContacts": true,
        "platformContactsOnly": false,
        "businessHoursEnabled": false,
        "businessHoursStart": "08:00",
        "businessHoursEnd": "18:00",
        "businessHoursDays": [1, 2, 3, 4, 5],
        "businessHoursLunchEnabled": false,
        "businessHoursLunchStart": "12:00",
        "businessHoursLunchEnd": "13:00",
        "outsideHoursMessage": "",
        "welcomeMessage": "",
        "autoAssignEnabled": false,
        "autoAssignStrategy": "least_loaded",
        "autoCloseMinutes": 0,
        "conversationRetentionDays": 0,
        "allowedAttachmentTypes": ["image", "video", "audio", "document"],
        "maxAttachmentMB": 50,
        "notifyNewConversation": true,
        "notifyMentions": true,
        "notifyUnanswered": true,
        "outboundMessagesEnabled": true
    }'::jsonb, '{messageSignatureTemplate}', '"{agentName}"');
