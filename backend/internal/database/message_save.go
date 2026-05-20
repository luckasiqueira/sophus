package database

import (
	"encoding/json"
	"fmt"
	"time"

	"strings"
	"zubly/backend/pkg/wpp"

	"github.com/google/uuid"
)

func MessageSave(connection Connection, msg wpp.EventMessage) error {
	// Listar conversation mais recente e se ele está aberto. Se estiver fechado, criar um novo
	conversationId, err := checkExistentConversation(connection.Id, msg)
	if err != nil {
		return err
	}
	fmt.Println("Starting message creation")
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

func checkExistentConversation(connectionId int, msg wpp.EventMessage) (int, error) {
	stmt, err := db.Prepare(`SELECT "id" FROM "contacts" WHERE "number" = $1 OR "lid" = $2`)
	if err != nil {
		return 0, err
	}
	var contactId int
	err = stmt.QueryRow(msg[0].Body.Data.Info.Sender, msg[0].Body.Data.Info.SenderAlt).Scan(&contactId)
	if err != nil {
		if !strings.Contains(err.Error(), "no rows in result set") {
			return 0, err
		}
		contact := Contact{
			Name:         msg[0].Body.Data.Info.PushName,
			Number:       strings.Split(msg[0].Body.Data.Info.Sender, "@")[0],
			JID:          msg[0].Body.Data.Info.Sender,
			LID:          msg[0].Body.Data.Info.SenderAlt,
			ConnectionId: connectionId,
			IsGroup:      msg[0].Body.Data.Info.IsGroup,
		}
		contactId, err = CreateContact(contact)
		if err != nil {
			return 0, err
		}
	}
	defer stmt.Close()
	stmtt, err := db.Prepare(`SELECT "id" FROM "conversations" WHERE "contactId" = $1 AND "status" = 'open'`)
	if err != nil {
		return 0, err
	}
	var conversationId int
	err = stmtt.QueryRow(contactId).Scan(&conversationId)
	if err != nil {
		if !strings.Contains(err.Error(), "no rows in result set") {
			return 0, err
		}
		conversation := Conversation{
			Status:       "open",
			ContactID:    contactId,
			ConnectionID: connectionId,
			URL:          uuid.New(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		conversationId, err = CreateConversation(conversation)
		if err != nil {
			return 0, err
		}
	}

	return conversationId, nil
}
