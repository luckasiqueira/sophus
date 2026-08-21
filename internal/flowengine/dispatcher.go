package flowengine

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
)

var timeoutMu sync.Mutex
var timeoutTimers = map[int]*time.Timer{}
var schedulerSlots = make(chan struct{}, 8)
var scheduledMessageSlots = make(chan struct{}, 4)

func ScheduleTimeout(executionID, _, _, _, timeoutSeconds int) {
	timeoutMu.Lock()
	defer timeoutMu.Unlock()
	if existing, ok := timeoutTimers[executionID]; ok {
		existing.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
		timeoutMu.Lock()
		if timeoutTimers[executionID] == timer {
			delete(timeoutTimers, executionID)
		}
		timeoutMu.Unlock()

		resumeTimedOutExecution(executionID, false)
	})
	timeoutTimers[executionID] = timer
}

func StartScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := repo.RecoverExpiredFlowExecutionClaims(); err != nil {
					log.Printf("erro ao recuperar claims expirados de flows: %v", err)
					continue
				}
				availableFlowSlots := cap(schedulerSlots) - len(schedulerSlots)
				ids := []int{}
				var err error
				if availableFlowSlots > 0 {
					ids, err = repo.ReserveDueFlowExecutionIDs(availableFlowSlots)
				}
				if err != nil {
					log.Printf("erro ao buscar timeouts de flows: %v", err)
					continue
				}
				for _, id := range ids {
					select {
					case schedulerSlots <- struct{}{}:
						go func(executionID int) {
							defer func() { <-schedulerSlots }()
							resumeTimedOutExecution(executionID, true)
						}(id)
					default:
					}
				}
				availableMessageSlots := cap(scheduledMessageSlots) - len(scheduledMessageSlots)
				messages := []repo.ScheduledFlowMessage{}
				if availableMessageSlots > 0 {
					messages, err = repo.ClaimDueScheduledFlowMessages(availableMessageSlots)
				}
				if err != nil {
					log.Printf("erro ao buscar mensagens agendadas: %v", err)
					continue
				}
				for _, message := range messages {
					select {
					case scheduledMessageSlots <- struct{}{}:
						go func(scheduled repo.ScheduledFlowMessage) {
							defer func() { <-scheduledMessageSlots }()
							processScheduledFlowMessage(scheduled)
						}(message)
					default:
					}
				}
			}
		}
	}()
}

func processScheduledFlowMessage(scheduled repo.ScheduledFlowMessage) {
	allowed, err := repo.CompanyHasAccess(scheduled.CompanyID)
	if err == nil && !allowed {
		err = errors.New("empresa sem assinatura ativa")
	}
	var conversation repo.Conversation
	if err == nil {
		conversation, err = repo.GetConversationById(scheduled.ConversationID)
	}
	var connection repo.ConnectionEVO
	if err == nil {
		connection, err = repo.GetConnectionById(scheduled.ConnectionID)
	}
	if err == nil && (connection.CompanyID != scheduled.CompanyID || conversation.ConnectionID != scheduled.ConnectionID) {
		err = errors.New("mensagem agendada possui vínculos inconsistentes")
	}
	if err == nil {
		err = NewMessenger(connection).SendText(conversation, scheduled.Message)
	}
	if finishErr := repo.FinishScheduledFlowMessage(scheduled.ID, scheduled.ClaimVersion, err); finishErr != nil {
		log.Printf("erro ao finalizar mensagem agendada %d: %v", scheduled.ID, finishErr)
	}
}

func resumeTimedOutExecution(executionID int, reserved bool) {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil || execution.Status != "waiting" || execution.ResumeAt == nil || (!reserved && execution.ResumeAt.After(time.Now())) {
		return
	}
	conversation, err := repo.GetConversationById(execution.ConversationId)
	if err != nil {
		return
	}
	connection, err := repo.GetConnectionById(conversation.ConnectionID)
	if err != nil {
		return
	}
	engine := NewEngine(connection)
	if err := engine.ExecuteFlow(execution.FlowId, execution.ConversationId, execution.CompanyId, ExecutionContext{
		"_resumeExecutionId": float64(executionID),
		"_timedOut":          true,
	}); err != nil {
		log.Printf("erro ao processar timeout da execução %d: %v", executionID, err)
	}
}

func CancelTimeout(executionID int) {
	timeoutMu.Lock()
	defer timeoutMu.Unlock()
	if timer, ok := timeoutTimers[executionID]; ok {
		timer.Stop()
		delete(timeoutTimers, executionID)
	}
}

func HandleIncomingMessage(conversation repo.Conversation, connection repo.ConnectionEVO, messageText, senderName, messageType string) {
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
				"messageType":        messageType,
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
			message := "execução running recuperada após exceder o tempo máximo"
			recovered, err := repo.RecoverStaleFlowExecution(active, message)
			if err != nil {
				log.Printf("failed to recover stale flow execution: execution=%d conversation=%d error=%v", active.Id, conversation.Id, err)
				return
			}
			if !recovered {
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
				"message":     messageText,
				"response":    messageText,
				"senderName":  senderName,
				"messageType": messageType,
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
