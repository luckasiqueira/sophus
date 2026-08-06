package flowengine

import (
	"log"
	"strings"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
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
			sse.NotifyConversations(connection.CompanyID)
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
		if active.Status == "running" && time.Since(active.UpdatedAt) > maxExecutionTime {
			now := time.Now()
			message := "execução running recuperada após exceder o tempo máximo"
			active.Status = "failed"
			active.ErrorMessage = &message
			active.CompletedAt = &now
			if err := repo.FinalizeFlowExecution(active); err != nil {
				log.Printf("failed to recover stale flow execution: execution=%d conversation=%d error=%v", active.Id, conversation.Id, err)
				return
			}
			sse.NotifyConversations(connection.CompanyID)
			conversation.Status = repo.ConversationStatusOpen
			log.Printf("recovered stale flow execution: execution=%d conversation=%d", active.Id, conversation.Id)
		} else {
			log.Printf("flow dispatch skipped: conversation=%d active_execution=%d status=%s", conversation.Id, active.Id, active.Status)
			return
		}
	}
	if conversation.Status != repo.ConversationStatusOpen {
		log.Printf("flow dispatch skipped: conversation=%d status=%s", conversation.Id, conversation.Status)
		return
	}

	flows, err := repo.GetActiveFlowsForConnection(connection.CompanyID, connection.Id)
	if err != nil {
		log.Printf("erro ao buscar flows: %v", err)
		return
	}
	if len(flows) == 0 {
		log.Printf("flow dispatch skipped: conversation=%d connection=%d no active flows", conversation.Id, connection.Id)
		return
	}

	for _, flow := range flows {
		if !matchesTrigger(flow, messageText) {
			continue
		}
		flowID := flow.Id
		log.Printf("flow dispatch matched: flow=%d conversation=%d connection=%d trigger=%s", flowID, conversation.Id, connection.Id, flow.TriggerType)
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
	log.Printf("flow dispatch finished: conversation=%d candidates=%d", conversation.Id, len(flows))
}

func matchesTrigger(flow repo.ChatbotFlow, messageText string) bool {
	text := strings.ToLower(strings.TrimSpace(messageText))
	switch strings.ToLower(strings.TrimSpace(flow.TriggerType)) {
	case "keyword":
		trigger := strings.ToLower(strings.TrimSpace(flow.TriggerValue))
		if trigger == "" {
			return false
		}
		return strings.Contains(text, trigger)
	case "exact":
		trigger := strings.ToLower(strings.TrimSpace(flow.TriggerValue))
		return trigger != "" && text == trigger
	case "always":
		return true
	default:
		return false
	}
}
