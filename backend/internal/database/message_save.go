package database

import (
	"encoding/json"
	"time"

	"strings"
	"zubly/backend/pkg/wpp"
)

func MessageSaveAPI(apiToken string, msg wpp.TextMessage) error {
	connection, err := GetConnectionByToken(apiToken)
	if err != nil {
		return err
	}
	contact := Contact{
		Name:         "Contato salvo automaticamente",
		Number:       msg.Number,
		ConnectionId: connection.Id,
		JID:          msg.Number + "@s.whatsapp.net",
	}
	conversationId, err := setConversation(connection.Id, contact)
	if err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`)
	if err != nil {
		return err
	}
	fullData, err := json.Marshal(contact)
	if err != nil {
		return err
	}
	defer stmt.Close()
	return stmt.QueryRow(
		msg.MessageID,
		msg.Text,
		conversationId,
		msg.Quoted.MessageID,
		"mediatype_fix",
		fullData,
		time.Now(),
		time.Now(),
		true,
		false,
		false,
		false,
	).Err()
}

func MessageSave(connection Connection, msg wpp.EventMessage) error {
	contact := Contact{
		Name:         msg[0].Body.Data.Info.PushName,
		Number:       strings.Split(msg[0].Body.Data.Info.Sender, "@")[0],
		JID:          msg[0].Body.Data.Info.Sender,
		LID:          msg[0].Body.Data.Info.SenderAlt,
		ConnectionId: connection.Id,
		IsGroup:      msg[0].Body.Data.Info.IsGroup,
	}
	conversationId, err := setConversation(connection.Id, contact)
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
