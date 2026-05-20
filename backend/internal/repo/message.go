package repo

import (
	"encoding/json"
	"strings"
	"time"
	"zubly/backend/pkg/http/requests"
)

func SaveEvoMessage(msg Message, connection ConnectionEVO) error {
	return msg.Save(connection)
}

func (msg TextMessageEVO) Save(connection ConnectionEVO) error {
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
	fullJson, err := json.Marshal(contact)
	if err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	return stmt.QueryRow(
		msg.MessageID,
		msg.Text,
		conversationId,
		msg.QuotedEVO.MessageID,
		"mediatype_fix",
		fullJson,
		time.Now(),
		time.Now(),
		true,
		false,
		false,
		false,
	).Err()
}

func (msg EventMessageEVO) Save(connection ConnectionEVO) error {
	m := msg[0]
	contact := Contact{
		Name:         m.Body.Data.Info.PushName,
		Number:       strings.Split(m.Body.Data.Info.Sender, "@")[0],
		JID:          m.Body.Data.Info.Sender,
		LID:          m.Body.Data.Info.SenderAlt,
		ConnectionId: connection.Id,
		IsGroup:      m.Body.Data.Info.IsGroup,
	}
	conversationId, err := setConversation(connection.Id, contact)
	if err != nil {
		return err
	}
	fullJson, err := json.Marshal(m.Body)
	if err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`)
	if err != nil {
		return err
	}
	text := m.Body.Data.Message.Text
	if text == "" {
		text = m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	defer stmt.Close()
	_, err = stmt.Exec(
		m.Body.Data.Info.ID,
		text,
		conversationId,
		m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessageID,
		m.Body.Data.Info.Mediatype,
		fullJson,
		m.Body.Data.Info.Timestamp,
		m.Body.Data.Info.Timestamp,
		m.Body.Data.Info.IsFromMe,
		m.Body.Data.Info.IsGroup,
		false,
		false,
	)
	return err
}

func (msg TextMessageEVO) Send(connectionKey string) (int, error) {
	r := requests.Request{
		URL:     apiBaseURL + `/send/text`,
		Payload: msg,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Response: requests.Response{},
	}
	err := r.Do()
	return r.StatusCode, err
}
