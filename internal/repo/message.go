package repo

import (
	"encoding/json"
	"fmt"
	"sophus/pkg/http/requests"
	"sophus/utils"
	"sophus/utils/env"
	"strings"

	"github.com/google/uuid"
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
	err := json.Unmarshal(msg.JSON, &m)
	if err != nil {
		return err
	}
	return saveMessageEvo(m, msg.JSON, contact, connection)
}

func (msg EventMessageEVO) Save(connection ConnectionEVO) error {
	var contact Contact
	var err error
	if msg.Data.Info.IsGroup {
		contact, err = getGroupInfo(msg.Data.Info.Chat, msg.InstanceToken.String())
		if err != nil {
			return err
		}
		contact.ConnectionId = connection.Id
	} else {
		contact = Contact{
			Name:         msg.Data.Info.PushName,
			Number:       strings.Split(msg.Data.Info.Sender, "@")[0],
			JID:          msg.Data.Info.Sender,
			LID:          msg.Data.Info.SenderAlt,
			ConnectionId: connection.Id,
			IsGroup:      msg.Data.Info.IsGroup,
		}
		if msg.Data.Info.IsFromMe {
			contact.LID = msg.Data.Info.Chat
			contact.Number = strings.Split(msg.Data.Info.RecipientAlt, "@")[0]
			contact.JID = msg.Data.Info.RecipientAlt
		}
	}
	return saveMessageEvo(msg, msg.FullJSON, contact, connection)
}

func (msg TextMessageEVO) Send(connectionKey string) (int, []byte, error) {
	r := requests.Request{
		URL:     ApiBaseURL + `/send/text`,
		Method:  "POST",
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

func saveMessageEvo(msg EventMessageEVO, fullJson []byte, contact Contact, connection ConnectionEVO) error {
	conversationId, err := setConversation(connection.Id, contact)
	if err != nil {
		return err
	}
	query := `INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "mediaPath", "createdAt", "updatedAt", 
     "isFromMe", "isGroup", "isRead", "isDeleted") VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12 );`
	msgType := CheckMessageType(msg)
	text := CheckMessageText(msg)
	if msg.Data.Message.Base64 != "" {
		msg.MediaPath, err = saveMessageMedia(msg, connection.CompanyID, msgType)
		if err != nil {
			return err
		}
	}
	return insert(query,
		msg.Data.Info.ID,
		text,
		conversationId,
		msg.Data.Message.TXT.ContextInfo.QuotedMessageID,
		msgType,
		msg.MediaPath,
		msg.Data.Info.Timestamp,
		msg.Data.Info.Timestamp,
		msg.Data.Info.IsFromMe,
		msg.Data.Info.IsGroup,
		false,
		false)
}

func CheckMessageText(msg EventMessageEVO) string {
	var text string
	if msg.Data.Message.TXT.Text != "" {
		text = msg.Data.Message.TXT.Text // text = m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	if msg.Data.Message.Conversation != "" {
		text = msg.Data.Message.Conversation
	}
	if msg.Data.Message.IMG.Caption != "" {
		text = msg.Data.Message.IMG.Caption
	}
	if msg.Data.Message.VID.Caption != "" {
		text = msg.Data.Message.IMG.Caption
	}
	return text
}

func CheckMessageType(msg EventMessageEVO) string {
	if msg.Data.Message.IMG.MimeType != "" {
		return "image"
	}
	if msg.Data.Message.VID.MimeType != "" {
		return "video"
	}
	if msg.Data.Message.AUD.MimeType != "" {
		return "audio"
	}
	return "text"
}

func saveMessageMedia(msg EventMessageEVO, companyId int, messageType string) (string, error) {
	var format string
	switch messageType {
	case "image":
		format = strings.Split(msg.Data.Message.IMG.MimeType, "/")[1]
	case "video":
		format = strings.Split(msg.Data.Message.VID.MimeType, "/")[1]
	case "audio":
		format = strings.Split(msg.Data.Message.AUD.MimeType, "/")[1]
		format = strings.Split(format, ";")[0]
	}

	path := fmt.Sprintf("%s/%d/", env.Backend["MEDIA_DIRECTORY"], companyId)
	fileName := fmt.Sprintf("%s.%s", uuid.NewString(), format)
	return fileName, utils.MediaDecoder(msg.Data.Message.Base64, path, fileName)
}

func GetMessagesByConversationURL(url uuid.UUID) ([]MessageData, error) {
	stmt, err := db.Prepare(`SELECT 
    m."messageId",
    m.text,
    m."quotedId",
    m."mediaType",
    m."updatedAt",
    m."isFromMe",
    m."isGroup",
    m."isRead",
    m."isDeleted",
    m."mediaPath"
		FROM messages m
		INNER JOIN public.conversations c ON c.id = m."conversationId"
		WHERE c.url = $1
		ORDER BY m.id ASC;`)
	if err != nil {
		return []MessageData{}, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(url)
	if err != nil {
		return []MessageData{}, err
	}
	messages := []MessageData{}
	for rows.Next() {
		msg := MessageData{}
		err = rows.Scan(
			&msg.MessageId,
			&msg.Text,
			&msg.QuotedId,
			&msg.MediaType,
			&msg.UpdatedAt,
			&msg.IsFromMe,
			&msg.IsGroup,
			&msg.IsRead,
			&msg.IsDeleted,
			&msg.MediaPath)
		if err != nil {
			if !strings.Contains(err.Error(), "converting NULL to string is unsupported") {
				return []MessageData{}, err
			}
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func GetMediaPathByMessage(message string) (string, error) {
	mediaPath := ""
	stmt, err := db.Prepare(`SELECT "mediaPath" FROM messages WHERE "messageId" = $1`)
	if err != nil {
		return mediaPath, err
	}
	defer stmt.Close()
	return mediaPath, stmt.QueryRow(message).Scan(&mediaPath)
}

//func (msg EventMesageImageEVO) Save(connection ConnectionEVO) error {
//	contact, err := contactHelper(msg.Data.Info.BaseEventMSGInfoEVO.IsGroup, connection.Id, msg.Data.Info.BaseEventMSGInfoEVO, msg.InstanceToken)
//	fullJson, err := json.Marshal(msg.Data)
//	if err != nil {
//		return err
//	}
//	return saveMessageEvo(msg, fullJson, contact, connection.Id)
//}

//func contactHelper(isGroup bool, connectionId int, info BaseEventMSGInfoEVO, connectionKey uuid.UUID) (Contact, error) {
//	var contact Contact
//	var err error
//	if isGroup {
//		contact, err = getGroupInfo(info.Chat, connectionKey.String())
//		if err != nil {
//			return contact, err
//		}
//		contact.ConnectionId = connectionId
//	} else {
//		contact = Contact{
//			Name:         info.PushName,
//			Number:       strings.Split(info.Sender, "@")[0],
//			JID:          info.Sender,
//			LID:          info.SenderAlt,
//			ConnectionId: connectionId,
//			IsGroup:      isGroup,
//		}
//	}
//	return contact, nil
//}
