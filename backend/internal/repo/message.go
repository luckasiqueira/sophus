package repo

import (
	"encoding/json"
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
	m := EventMessageEVO{}
	err := json.Unmarshal(msg.JSON, &m.Data)
	if err != nil {
		return err
	}
	return saveMessageEvo(m, msg.JSON, contact, connection.Id)
}

func (msg EventMessageEVO) Save(connection ConnectionEVO) error {
	contact := Contact{
		Name:         msg.Data.Info.PushName,
		Number:       strings.Split(msg.Data.Info.RecipientAlt, "@")[0],
		JID:          msg.Data.Info.RecipientAlt,
		LID:          msg.Data.Info.Sender,
		ConnectionId: connection.Id,
		IsGroup:      msg.Data.Info.IsGroup,
	}
	fullJson, err := json.Marshal(msg.Data)
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
	query := `INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "fullData", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`
	text := msg.Data.Message.Text
	if text == "" {
		text = msg.Data.Message.ExtendedTextMessage.Text // text = m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	return insert(query,
		msg.Data.Info.ID,
		text,
		conversationId,
		msg.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessageID,
		msg.Data.Info.Mediatype,
		fullJson,
		msg.Data.Info.Timestamp,
		msg.Data.Info.Timestamp,
		msg.Data.Info.IsFromMe,
		msg.Data.Info.IsGroup,
		false,
		false)
}
