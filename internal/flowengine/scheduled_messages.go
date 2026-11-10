package flowengine

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
)

var agentScheduledMessageSlots = make(chan struct{}, 4)

// scheduledSendError carries the message shown to the user on the schedule
// and whether trying again later can succeed.
type scheduledSendError struct {
	message   string
	retryable bool
}

func (err *scheduledSendError) Error() string { return err.message }

func permanentScheduleError(message string) error {
	return &scheduledSendError{message: message}
}

func dispatchAgentScheduledMessages() {
	available := cap(agentScheduledMessageSlots) - len(agentScheduledMessageSlots)
	if available <= 0 {
		return
	}
	messages, err := repo.ClaimDueScheduledMessages(available)
	if err != nil {
		log.Printf("erro ao buscar agendamentos de mensagens: %v", err)
	}
	for _, message := range messages {
		select {
		case agentScheduledMessageSlots <- struct{}{}:
			go func(scheduled repo.ScheduledMessage) {
				defer func() { <-agentScheduledMessageSlots }()
				processAgentScheduledMessage(scheduled)
			}(message)
		default:
			// No free slot: the claim expires and the row is requeued.
		}
	}
}

func processAgentScheduledMessage(scheduled repo.ScheduledMessage) {
	conversationID, err := sendAgentScheduledMessage(scheduled)
	retryable := true
	var sendErr *scheduledSendError
	if errors.As(err, &sendErr) {
		retryable = sendErr.retryable
	} else if err != nil {
		log.Printf("agendamento %d: envio falhou: %v", scheduled.Id, err)
		err = &scheduledSendError{message: "não foi possível enviar a mensagem; uma nova tentativa será feita"}
	}
	if finishErr := repo.FinishScheduledMessage(scheduled.Id, scheduled.ClaimVersion, conversationID, err, retryable); finishErr != nil {
		log.Printf("erro ao finalizar agendamento %d: %v", scheduled.Id, finishErr)
	} else if err == nil {
		scheduleNextOccurrence(scheduled)
	}
	sse.NotifyConversations(scheduled.CompanyId)
}

// scheduleNextOccurrence keeps a recurring series going after a delivery. A
// failed occurrence stops the series until the user reschedules it.
func scheduleNextOccurrence(scheduled repo.ScheduledMessage) {
	if scheduled.Recurrence == "" || scheduled.Recurrence == repo.ScheduledRecurrenceNone {
		return
	}
	settings, err := repo.GetCompanyOperationSettings(scheduled.CompanyId)
	if err != nil {
		log.Printf("agendamento %d: fuso da empresa indisponível para a próxima ocorrência: %v", scheduled.Id, err)
		return
	}
	next, ok := repo.NextScheduledOccurrence(scheduled, scheduleTimezone(settings.Timezone), time.Now())
	if !ok {
		return
	}
	if err := repo.CreateNextScheduledOccurrence(scheduled, next); err != nil {
		log.Printf("agendamento %d: não foi possível criar a próxima ocorrência: %v", scheduled.Id, err)
	}
}

func scheduleTimezone(name string) *time.Location {
	if location, err := time.LoadLocation(strings.TrimSpace(name)); err == nil && strings.TrimSpace(name) != "" {
		return location
	}
	if location, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return location
	}
	return time.UTC
}

func sendAgentScheduledMessage(scheduled repo.ScheduledMessage) (int, error) {
	allowed, err := repo.CompanyHasAccess(scheduled.CompanyId)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, permanentScheduleError("a empresa está sem acesso ativo ao sistema no horário do envio")
	}
	if scheduled.AgentId == nil {
		return 0, permanentScheduleError("o agente que criou o agendamento foi removido")
	}
	agent, err := repo.GetAgentByIdAndCompany(*scheduled.AgentId, scheduled.CompanyId)
	if err != nil || !agent.IsActive {
		return 0, permanentScheduleError("o agente que criou o agendamento foi removido ou desativado")
	}
	connection, err := repo.GetConnectionById(scheduled.ConnectionId)
	if err != nil || connection.CompanyID != scheduled.CompanyId {
		return 0, permanentScheduleError("a conexão escolhida não existe mais")
	}
	if connection.Status != repo.ConnectionStatusConnected {
		return 0, &scheduledSendError{message: "a conexão " + connection.Name + " estava desconectada no horário do envio", retryable: true}
	}
	started, err := repo.StartOrReuseConversation(agent, scheduled.ContactId, scheduled.ConnectionId)
	if errors.Is(err, repo.ErrConversationUnavailable) {
		return 0, permanentScheduleError("a conversa com o contato está com outro agente ou o contato está bloqueado")
	}
	if err != nil {
		return 0, err
	}
	conversation, err := repo.GetConversationForCompanyByURL(scheduled.CompanyId, started.URL)
	if err != nil {
		return 0, err
	}
	contact, err := repo.GetContactById(scheduled.ContactId)
	if err != nil {
		return conversation.Id, err
	}
	conversation.Contact = contact
	settings, err := repo.GetCompanyOperationSettings(scheduled.CompanyId)
	if err != nil {
		return conversation.Id, err
	}
	preferences, err := repo.GetAgentPreferences(agent.Id, agent.CompanyId)
	if err != nil {
		return conversation.Id, err
	}
	body := strings.TrimSpace(scheduled.Body)
	if body != "" {
		body = preferences.SignMessage(body, agent.Name)
	}
	if scheduled.MediaPath == "" {
		return conversation.Id, sendScheduledText(connection, conversation, body)
	}
	if !attachmentTypeAllowed(settings, scheduled.MediaKind) {
		return conversation.Id, permanentScheduleError("este tipo de anexo foi desativado nas configurações da empresa")
	}
	mediaURL, err := media.ScopedSignedURL(scheduled.CompanyId, media.ScopeMessages, scheduled.MediaPath, 15*time.Minute)
	if err != nil {
		return conversation.Id, permanentScheduleError("o arquivo anexado não foi encontrado na biblioteca de mídia")
	}
	// Audio has no caption on WhatsApp: its text goes in a separate message.
	caption := body
	if scheduled.MediaKind == "audio" {
		caption = ""
	}
	if _, err := NewChatMessenger(connection, settings).SendStoredMediaMessage(conversation, scheduled.MediaKind, mediaURL, caption,
		scheduled.MediaName, media.ScopeMessages, scheduled.MediaPath); err != nil {
		return conversation.Id, err
	}
	if scheduled.MediaKind == "audio" && body != "" {
		if err := sendScheduledText(connection, conversation, body); err != nil {
			// The audio is already delivered; retrying would send it twice.
			log.Printf("agendamento %d: texto após áudio falhou: %v", scheduled.Id, err)
			return conversation.Id, permanentScheduleError("o áudio foi enviado, mas o texto que o acompanhava falhou")
		}
	}
	return conversation.Id, nil
}

func sendScheduledText(connection repo.ConnectionEVO, conversation repo.Conversation, text string) error {
	msg := repo.TextMessageEVO{MessageBaseEVO: repo.MessageBaseEVO{Number: conversation.Contact.Number}, Text: text}
	status, body, err := msg.Send(connection.EvolutionAPIKey())
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("envio de texto recusado pelo Evolution GO (status %d)", status)
	}
	msg.JSON = body
	_, err = repo.SaveChatTextMessage(msg, connection, conversation)
	return err
}

func attachmentTypeAllowed(settings repo.CompanyOperationSettings, kind string) bool {
	for _, allowed := range settings.AllowedAttachmentTypes {
		if allowed == kind {
			return true
		}
	}
	return false
}
