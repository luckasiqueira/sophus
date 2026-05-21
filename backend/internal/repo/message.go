package repo

import (
	"encoding/json"
	"fmt"
	"sophus/backend/pkg/http/requests"
	"strings"
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
	m := make(EventMessageEVO, 1)
	err := json.Unmarshal(msg.JSON, &m[0].Body)
	if err != nil {
		return err
	}
	fmt.Println(m[0])
	return saveMessageEvo(m, msg.JSON, contact, connection.Id)
}

func (msg EventMessageEVO) Save(connection ConnectionEVO) error {
	m := msg[0]
	contact := Contact{
		Name:         m.Body.Data.Info.PushName,
		Number:       strings.Split(m.Body.Data.Info.RecipientAlt, "@")[0],
		JID:          m.Body.Data.Info.RecipientAlt,
		LID:          m.Body.Data.Info.Sender,
		ConnectionId: connection.Id,
		IsGroup:      m.Body.Data.Info.IsGroup,
	}
	fullJson, err := json.Marshal(m.Body)
	if err != nil {
		return err
	}
	return saveMessageEvo(msg, fullJson, contact, connection.Id)
}

func (msg TextMessageEVO) Send(connectionKey string) (int, []byte, error) {
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
	return r.StatusCode, r.Response.Body, err
}

func saveMessageEvo(msg EventMessageEVO, fullJson []byte, contact Contact, connectionId int) error {
	conversationId, err := setConversation(connectionId, contact)
	if err != nil {
		return err
	}
	m := msg[0]
	query := `INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`
	text := m.Body.Data.Message.Text
	if text == "" {
		text = m.Body.Data.Message.ExtendedTextMessage.Text // text = m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	return insert(query,
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
		false)
}
