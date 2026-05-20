package database

import (
	"encoding/json"
	"zubly/backend/pkg/wpp"
)

func MessageSave(connection Connection, msg wpp.EventMessage) error {
	// Listar conversation mais recente e se ele está aberto. Se estiver fechado, criar um novo
	conversationId, err := checkExistentConversation(msg)
	if err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`)
	if err != nil {
		return err
	}
	fullJson, err := json.Marshal(msg[0].Body)
	if err != nil {
		return err
	}
	text := msg[0].Body.Data.Message.Text
	if text == "" {
		text = msg[0].Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	defer stmt.Close()
	_, err = stmt.Exec(
		msg[0].Body.Data.Info.ID,
		text,
		conversationId,
		msg[0].Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessageID,
		msg[0].Body.Data.Info.Mediatype,
		fullJson,
		msg[0].Body.Data.Info.Timestamp,
		msg[0].Body.Data.Info.Timestamp,
		msg[0].Body.Data.Info.IsFromMe,
		msg[0].Body.Data.Info.IsGroup,
		false,
		false,
	)
	if err != nil {
		return err
	}
	return nil
}

func checkExistentConversation(msg wpp.EventMessage) (int, error) {
	// extrair o contactID a partir do number/lid
	stmt, err := db.Prepare(`SELECT "id" FROM "contacts" WHERE "number" = $1 OR "lid" = $2`)
	if err != nil {
		return 0, err
	}
	var contactId int
	err = stmt.QueryRow(msg[0].Body.Data.Info.Sender, msg[0].Body.Data.Info.SenderAlt).Scan(&contactId)
	if err != nil || contactId == 0 {
		// criar contato se não existir
		return 0, err
	}
	defer stmt.Close()
	stmtt, err := db.Prepare(`SELECT "id" FROM "conversations" WHERE "contactId" = $1 AND "status" = 'open'`)
	if err != nil {
		// criar conversation se não existir
		return 0, err
	}
	var conversationId int
	err = stmtt.QueryRow(contactId).Scan(&conversationId)
	if err != nil || conversationId == 0 {
		return 0, err
	}
	return conversationId, nil
}
