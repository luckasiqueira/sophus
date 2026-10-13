package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sophus/pkg/http/requests"

	"github.com/google/uuid"
)

const (
	MessageEditingDisabled    = "disabled"
	MessageEditingAdmins      = "admins"
	MessageEditingEveryone    = "all"
	WhatsAppMessageEditWindow = 15 * time.Minute
)

var (
	ErrMessageEditForbidden      = errors.New("você não tem permissão para editar mensagens nesta conversa")
	ErrMessageNotEditable        = errors.New("somente mensagens de texto enviadas nos últimos 15 minutos podem ser editadas")
	ErrMessageEditConflict       = errors.New("a mensagem foi alterada enquanto você editava; feche e abra o editor novamente")
	ErrWhatsAppEditRejected      = errors.New("o WhatsApp não confirmou a edição; a mensagem original foi mantida")
	ErrEditConnectionUnavailable = errors.New("a conexão do WhatsApp está indisponível")
)

func ValidMessageEditPolicy(policy string) bool {
	return policy == "" || policy == MessageEditingDisabled || policy == MessageEditingAdmins || policy == MessageEditingEveryone
}

func AgentCanEditMessages(policy string, agent Agent, conversation Conversation) bool {
	if conversation.ConnectionID <= 0 || conversation.Status != ConversationStatusOpen {
		return false
	}
	if !agent.IsAdmin() && (conversation.AgentID == nil || *conversation.AgentID != agent.Id) {
		return false
	}
	return policy == MessageEditingEveryone || policy == MessageEditingAdmins && agent.IsAdmin()
}

func MessageCanBeEdited(message MessageData, now time.Time) bool {
	return message.IsFromMe && !message.IsDeleted && message.MessageId != "" &&
		!strings.HasPrefix(message.MessageId, "flow-") && !strings.HasPrefix(message.MessageId, "media-") &&
		(message.MediaType == "text" || message.MediaType == "flow") &&
		!message.CreatedAt.IsZero() && !now.Before(message.CreatedAt) && now.Before(message.CreatedAt.Add(WhatsAppMessageEditWindow))
}

func sendWhatsAppMessageEdit(connection ConnectionEVO, chat, messageID, text string) error {
	request := requests.Request{
		URL: ApiBaseURL + "/message/edit", Method: "POST",
		Payload: map[string]string{"chat": chat, "messageId": messageID, "message": text},
		Headers: map[string]string{"Content-Type": "application/json", "apikey": connection.EvolutionAPIKey()},
		Timeout: 15 * time.Second, MaxResponseBytes: 1 << 20,
	}
	if err := request.Do(); err != nil {
		return ErrWhatsAppEditRejected
	}
	var response struct {
		Message string `json:"message"`
		Data    struct {
			MessageID string `json:"messageId"`
		} `json:"data"`
	}
	if json.Unmarshal(request.Response.Body, &response) != nil || response.Message != "success" || response.Data.MessageID == "" {
		return ErrWhatsAppEditRejected
	}
	return nil
}

// Serialize edits of the same record, check current company policy/assignment,
// and commit local content only after WhatsApp accepts the edit. No signature
// is added again: the editor operates on the exact text already sent.
func EditPlatformMessage(agent Agent, conversationURL uuid.UUID, messageID, text, expectedText string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var message MessageData
	var conversation Conversation
	var owner sql.NullInt64
	var policy, chat, number, apiKey string
	var connection ConnectionEVO
	err = tx.QueryRow(`SELECT m.id,m."conversationId",m.text,m."mediaType",m."isFromMe",m."isDeleted",m."createdAt",
		cv.status,cv."agentId",co.id,co.status,co.number,COALESCE(NULLIF(co."apiToken",''),co."connectionKey"::text,''),
		COALESCE(ct.jid,''),ct.number,COALESCE(ct."isGroup",false),COALESCE(company."operationSettings"->>'messageEditPolicy','disabled')
		FROM messages m JOIN conversations cv ON cv.id=m."conversationId"
		JOIN connections co ON co.id=cv."connectionId" AND co."companyId"=$3
		JOIN contacts ct ON ct.id=cv."contactId" AND ct."companyId"=$3
		JOIN companies company ON company.id=$3
		WHERE cv.url=$1 AND m."messageId"=$2 FOR UPDATE OF m,cv`, conversationURL, messageID, agent.CompanyId).Scan(
		&message.Id, &message.ConversationId, &message.Text, &message.MediaType, &message.IsFromMe, &message.IsDeleted, &message.CreatedAt,
		&conversation.Status, &owner, &connection.Id, &connection.Status, &connection.Number, &apiKey,
		&chat, &number, &message.IsGroup, &policy)
	if err != nil {
		return 0, err
	}
	conversation.ConnectionID = connection.Id
	if owner.Valid {
		value := int(owner.Int64)
		conversation.AgentID = &value
	}
	if !AgentCanEditMessages(policy, agent, conversation) {
		return 0, ErrMessageEditForbidden
	}
	message.MessageId = messageID
	if !MessageCanBeEdited(message, time.Now()) {
		return 0, ErrMessageNotEditable
	}
	if connection.Status != ConnectionStatusConnected {
		return 0, ErrEditConnectionUnavailable
	}
	if message.Text != expectedText {
		return 0, ErrMessageEditConflict
	}
	if text == message.Text {
		return message.ConversationId, nil
	}
	if chat == "" {
		chat = WhatsAppParticipant(number)
	}
	if chat == "" {
		return 0, ErrEditConnectionUnavailable
	}
	connection.CompanyID = agent.CompanyId
	connection.APIToken = apiKey
	timestamp := time.Now().UnixMilli()
	if err := sendWhatsAppMessageEdit(connection, chat, messageID, text); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE messages SET text=$1,"displayData"="displayData"||jsonb_build_object('edited',true,'editedAt',$2::bigint,'body',$1::text)
		WHERE id=$3`, text, timestamp, message.Id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	invalidateConversationHistory(agent.CompanyId, message.ConversationId)
	return message.ConversationId, nil
}
