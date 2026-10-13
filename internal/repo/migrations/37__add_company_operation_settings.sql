-- +goose Up
ALTER TABLE public.companies
    ADD COLUMN "operationSettings" jsonb NOT NULL DEFAULT '{
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
    }'::jsonb;

-- +goose Down
ALTER TABLE public.companies DROP COLUMN "operationSettings";
