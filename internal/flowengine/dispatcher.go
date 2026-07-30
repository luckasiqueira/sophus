package flowengine

import (
	"log"
	"strings"
	"sync"
	"time"

	"sophus/internal/repo"
)

var timeoutMu sync.Mutex
var timeoutTimers = map[int]*time.Timer{}

func ScheduleTimeout(executionID, flowID, conversationID, companyID, timeoutSeconds int) {
	timeoutMu.Lock()
	defer timeoutMu.Unlock()
	if existing, ok := timeoutTimers[executionID]; ok {
		existing.Stop()
	}
	timeoutTimers[executionID] = time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
		timeoutMu.Lock()
		delete(timeoutTimers, executionID)
		timeoutMu.Unlock()

		execution, err := repo.GetFlowExecutionById(executionID)
		if err != nil || execution.Status != "waiting" {
			return
		}
		log.Printf("timeout do waitForResponse para execução %d", executionID)
		go func() {
			conversation, err := repo.GetConversationById(conversationID)
			if err != nil {
				return
			}
			connection, err := repo.GetConnectionById(conversation.ConnectionID)
			if err != nil {
				return
			}
			engine := NewEngine(connection)
			if err := engine.ExecuteFlow(flowID, conversationID, companyID, ExecutionContext{
				"_resumeExecutionId": float64(executionID),
				"_timedOut":          true,
			}); err != nil {
				log.Printf("erro ao processar timeout da execução %d: %v", executionID, err)
			}
		}()
	})
}

func CancelTimeout(executionID int) {
	timeoutMu.Lock()
	defer timeoutMu.Unlock()
	if timer, ok := timeoutTimers[executionID]; ok {
		timer.Stop()
		delete(timeoutTimers, executionID)
	}
}

func HandleIncomingMessage(conversation repo.Conversation, connection repo.ConnectionEVO, messageText, senderName string) {
	if strings.TrimSpace(messageText) == "" {
		return
	}

	waiting, err := repo.GetWaitingExecutionByConversation(conversation.Id)
	if err == nil && waiting.Id > 0 {
		if conversation.Status == repo.ConversationStatusClosed {
			return
		}
		if conversation.Status == repo.ConversationStatusOpen {
			claimed, claimErr := repo.ClaimConversationForFlow(conversation.Id)
			if claimErr != nil || !claimed {
				return
			}
		}
		CancelTimeout(waiting.Id)
		go func() {
			engine := NewEngine(connection)
			ctx := ExecutionContext{
				"_resumeExecutionId": float64(waiting.Id),
				"response":           messageText,
				"message":            messageText,
				"senderName":         senderName,
				"userReplied":        true,
			}
			if err := engine.ExecuteFlow(waiting.FlowId, conversation.Id, connection.CompanyID, ctx); err != nil {
				log.Printf("erro ao retomar flow: %v", err)
			}
		}()
		return
	}
	if active, activeErr := repo.GetActiveFlowExecutionByConversation(conversation.Id); activeErr == nil && active.Id > 0 {
		return
	}
	if conversation.Status != repo.ConversationStatusOpen {
		return
	}

	flows, err := repo.GetActiveFlowsForConnection(connection.CompanyID, connection.Id)
	if err != nil {
		log.Printf("erro ao buscar flows: %v", err)
		return
	}

	for _, flow := range flows {
		if !matchesTrigger(flow, messageText) {
			continue
		}
		flowID := flow.Id
		go func() {
			engine := NewEngine(connection)
			ctx := ExecutionContext{
				"message":    messageText,
				"response":   messageText,
				"senderName": senderName,
			}
			if err := engine.ExecuteFlow(flowID, conversation.Id, connection.CompanyID, ctx); err != nil {
				log.Printf("erro ao executar flow %d: %v", flowID, err)
			}
		}()
		break
	}
}

func matchesTrigger(flow repo.ChatbotFlow, messageText string) bool {
	text := strings.ToLower(strings.TrimSpace(messageText))
	switch flow.TriggerType {
	case "keyword":
		trigger := strings.ToLower(strings.TrimSpace(flow.TriggerValue))
		if trigger == "" {
			return false
		}
		return strings.Contains(text, trigger)
	case "exact":
		return text == strings.ToLower(strings.TrimSpace(flow.TriggerValue))
	case "always":
		return true
	default:
		return false
	}
}
