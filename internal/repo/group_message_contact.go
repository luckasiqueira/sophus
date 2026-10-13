package repo

import (
	"database/sql"
	"errors"
	"strings"
)

const groupContactPlaceholder = "Grupo do WhatsApp"

func groupContactFromWebhook(msg EventMessageEVO, companyID int) (Contact, error) {
	jid := strings.ToLower(strings.TrimSpace(msg.Data.Info.Chat))
	parts := strings.Split(strings.TrimSuffix(jid, "@g.us"), "-")
	valid := len(parts) > 0 && len(parts) <= 2
	for _, part := range parts {
		valid = valid && decimalDigits(part)
	}
	if !strings.HasSuffix(jid, "@g.us") || !valid {
		return Contact{}, errors.New("mensagem sem identidade de grupo válida")
	}
	contact := Contact{CompanyId: companyID, JID: jid, IsGroup: true, Name: groupContactPlaceholder}
	data := jsonObject(msg.Data.GroupData)
	if groupJID := firstMessageText(data, "jid", "groupJid"); groupJID != "" && strings.ToLower(groupJID) != jid {
		data = nil
	}
	name := firstMessageText(data, "name", "subject")
	if name == "" {
		if groupName, ok := rawJSONObject(data, "groupName"); ok {
			name = rawJSONString(groupName, "name")
		}
	}
	if name != "" {
		contact.Name = name
		return contact, nil
	}
	var id int
	var existingName string
	err := db.QueryRow(`SELECT id,name FROM contacts WHERE "companyId"=$1 AND COALESCE("isGroup",false)=true AND jid=$2 ORDER BY id LIMIT 1`, companyID, jid).Scan(&id, &existingName)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Contact{}, err
	}
	if err == nil {
		contact.Id = id
		if strings.TrimSpace(existingName) != "" {
			contact.Name = existingName
		}
	}
	// Group metadata is optional for message ingestion. Do not block the Redis
	// consumer on /group/info; a later webhook can supply the real group name.
	return contact, nil
}
