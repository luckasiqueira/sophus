package flowengine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sophus/internal/llm"
	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
	"sophus/utils/env"
	"strconv"
	"strings"
	"time"
)

const (
	maxDepth                 = 50
	maxExecutionTime         = 5 * time.Minute
	maxExecutionContextBytes = 4 << 20
	noResponseWaitNodeKey    = "_noResponseWaitNodeId"
)

func logLLMFailure(operation string, companyID, credentialID int, provider, model string, err error) {
	failure, known := llm.FailureForError(err)
	if !known {
		log.Printf("llm request failed operation=%s company=%d credential=%d provider=%s model=%s category=internal", operation, companyID, credentialID, provider, model)
		return
	}
	log.Printf("llm request failed operation=%s company=%d credential=%d provider=%s model=%s category=%s upstream_status=%d retryable=%t", operation, companyID, credentialID, failure.Provider, model, failure.Category, failure.StatusCode, failure.Retryable)
}

type Engine struct {
	messenger    *Messenger
	claimVersion int
	startedAt    time.Time
}

func NewEngine(connection repo.ConnectionEVO) *Engine {
	return &Engine{messenger: NewMessenger(connection)}
}

func (e *Engine) ExecuteFlow(flowID, conversationID, companyID int, initialContext ExecutionContext) error {
	startTime := time.Now()
	e.startedAt = startTime
	var execution repo.FlowExecution
	resumeExecutionID, _ := initialContext["_resumeExecutionId"].(float64)
	allowed, accessErr := repo.CompanyHasAccess(companyID)
	if accessErr != nil || !allowed {
		message := "empresa sem assinatura ativa para executar o fluxo"
		if accessErr != nil {
			message = fmt.Sprintf("não foi possível validar a assinatura da empresa: %v", accessErr)
		}
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), message)
		}
		return errors.New(message)
	}
	if int(resumeExecutionID) > 0 {
		var err error
		execution, err = repo.GetFlowExecutionById(int(resumeExecutionID))
		if err != nil {
			return fmt.Errorf("execução %d não encontrada: %w", int(resumeExecutionID), err)
		}
		if execution.FlowId != flowID || execution.ConversationId != conversationID || execution.CompanyId != companyID {
			return fmt.Errorf("execução %d não pertence ao flow, conversa e empresa informados", execution.Id)
		}
		e.claimVersion = execution.ClaimVersion
	}

	var flowSnapshot json.RawMessage
	flowRevision := 0
	if int(resumeExecutionID) > 0 {
		flowSnapshot = execution.FlowDataSnapshot
		if execution.SnapshotIsLegacy {
			return e.failExecution(execution.Id, "execução antiga não pode ser retomada com segurança")
		}
	} else {
		flow, err := repo.GetChatbotFlowById(flowID, companyID)
		if err != nil {
			return fmt.Errorf("flow %d não encontrado: %w", flowID, err)
		}
		if !flow.IsActive {
			return fmt.Errorf("flow %d está inativo", flowID)
		}
		flowSnapshot = flow.FlowData
		flowRevision = flow.Revision
	}
	flowData, err := ParseFlowData(flowSnapshot)
	if err != nil {
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), err.Error())
		}
		return err
	}
	if int(resumeExecutionID) == 0 {
		if err := resolveSubflowSnapshots(&flowData, companyID, map[int]bool{flowID: true}); err != nil {
			return err
		}
		flowSnapshot, err = json.Marshal(flowData)
		if err != nil {
			return fmt.Errorf("serializar snapshot do fluxo: %w", err)
		}
		if len(flowSnapshot) > maxFlowDataBytes {
			return fmt.Errorf("snapshot expandido do fluxo excede o limite de %d bytes", maxFlowDataBytes)
		}
	}

	conversation, err := repo.GetConversationById(conversationID)
	if err != nil {
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), err.Error())
		}
		return err
	}

	context := cloneContext(initialContext)
	context["_flowId"] = flowID
	context["flowId"] = flowID
	context["conversationId"] = conversationID

	var currentNodeID string
	resumeWithoutRoute := false

	if int(resumeExecutionID) > 0 {
		if execution.Status != "waiting" {
			log.Printf("execução %d já retomada (status: %s)", execution.Id, execution.Status)
			return nil
		}
		claimVersion, claimed, err := repo.ClaimFlowExecution(execution.Id)
		if err != nil {
			return err
		}
		if !claimed {
			log.Printf("execução %d já foi retomada por outro evento", execution.Id)
			return nil
		}
		e.claimVersion = claimVersion
		savedCtx, parseErr := ParseContext(execution.Context)
		if parseErr != nil {
			return e.failExecution(execution.Id, fmt.Sprintf("contexto persistido inválido: %v", parseErr))
		}
		context = mergeContext(savedCtx, initialContext)
		if execution.CurrentNodeId != nil {
			savedNodeID := *execution.CurrentNodeId
			savedNode := findNode(flowData.Nodes, savedNodeID)
			if boolVal(initialContext, "_timedOut") && stringVal(savedCtx, noResponseWaitNodeKey) == savedNodeID && flowData.Settings.NoResponse.Enabled {
				delete(context, noResponseWaitNodeKey)
				if savedNode != nil && savedNode.Type == NodeNPSEvaluation {
					if err := repo.TimeoutFlowNPSEvaluation(execution.Id, savedNodeID); err != nil {
						return e.failExecution(execution.Id, fmt.Sprintf("falha ao encerrar avaliação NPS sem resposta: %v", err))
					}
				}
				if err := e.handleNoResponse(flowData.Settings.NoResponse, conversation, companyID, context); err != nil {
					return e.failExecution(execution.Id, fmt.Sprintf("falha ao tratar cliente sem resposta: %v", err))
				}
				return e.completeExecution(execution.Id, savedNodeID, context)
			}
			delete(context, noResponseWaitNodeKey)
			if savedNode != nil && savedNode.Type == NodeWaitForResponse {
				output := "replied"
				if boolVal(initialContext, "_timedOut") {
					output = "timeout"
				}
				next, _ := e.getNextNode(savedNodeID, string(NodeWaitForResponse), flowData.Edges, nil, nil, output, "")
				if next != "" {
					currentNodeID = next
				} else {
					resumeWithoutRoute = true
				}
			} else {
				currentNodeID = savedNodeID
			}
		}
	} else {
		contextJSON, contextErr := executionContextJSON(context)
		if contextErr != nil {
			return contextErr
		}
		executionID, started, err := repo.StartFlowExecution(repo.FlowExecution{
			FlowId:           flowID,
			ConversationId:   conversationID,
			CompanyId:        companyID,
			Status:           "running",
			Context:          contextJSON,
			FlowDataSnapshot: flowSnapshot,
			FlowRevision:     flowRevision,
		})
		if err != nil {
			return err
		}
		if !started {
			return nil
		}
		sse.NotifyConversations(companyID)
		execution, err = repo.GetFlowExecutionById(executionID)
		if err != nil {
			return e.failExecution(executionID, err.Error())
		}
		e.claimVersion = execution.ClaimVersion
	}
	context["executionId"] = execution.Id

	metadata, err := repo.GetFlowConditionMetadata(conversationID, companyID)
	if err != nil {
		return e.failExecution(execution.Id, fmt.Sprintf("não foi possível carregar dados para condições: %v", err))
	}
	applyConditionMetadata(context, metadata)
	if resumeWithoutRoute {
		return e.completeExecution(execution.Id, "", context)
	}

	if currentNodeID == "" {
		startNode := findNodeByType(flowData.Nodes, NodeStart)
		if startNode == nil {
			return e.failExecution(execution.Id, "flow não possui node start")
		}
		currentNodeID = startNode.ID
		if int(resumeExecutionID) == 0 {
			bhResult, err := e.checkBusinessHours(startNode, conversation, context)
			if err != nil {
				return e.failExecution(execution.Id, err.Error())
			}
			if bhResult == "blocked" || bhResult == "message_sent" {
				return e.completeExecution(execution.Id, currentNodeID, context)
			}
		}
	}

	depth := 0
	for currentNodeID != "" && depth < maxDepth {
		if time.Since(startTime) > maxExecutionTime {
			return e.failExecution(execution.Id, "timeout: execução excedeu 5 minutos")
		}

		currentNode := findNode(flowData.Nodes, currentNodeID)
		if currentNode == nil {
			return e.failExecution(execution.Id, fmt.Sprintf("node %s não encontrado", currentNodeID))
		}

		log.Printf("processando node %s (%s)", currentNode.ID, currentNode.Type)
		result, err := e.processNode(execution.Id, *currentNode, context, conversation, companyID, flowData, true)
		if err != nil {
			if errors.Is(err, repo.ErrFlowExecutionInactive) {
				log.Printf("flow execution stopped because it is no longer active: execution=%d node=%s", execution.Id, currentNodeID)
				return nil
			}
			return e.failExecution(execution.Id, err.Error())
		}

		if len(result.Context) > 0 {
			context = mergeContext(context, result.Context)
		}

		if result.WaitForResponse {
			timeoutSeconds := applyNoResponseWait(flowData, currentNodeID, context, intVal(currentNode.Data, "timeout"))
			if !result.WaitPersisted {
				if err := e.saveWaitingState(execution.Id, currentNodeID, context, timeoutSeconds, string(currentNode.Type)); err != nil {
					return e.failExecution(execution.Id, fmt.Sprintf("não foi possível colocar a execução em espera: %v", err))
				}
			}
			if timeoutSeconds > 0 {
				ScheduleTimeout(execution.Id, flowID, conversationID, companyID, timeoutSeconds)
			}
			return nil
		}

		if currentNode.Type == NodeEnd || currentNode.Type == NodeAIAgent {
			return e.completeExecution(execution.Id, currentNodeID, context)
		}
		if result.DirectNodeID != "" {
			currentNodeID = result.DirectNodeID
			depth++
			continue
		}

		nextNodeID, _ := e.getNextNode(currentNodeID, string(currentNode.Type), flowData.Edges,
			result.ConditionResult, result.SwitchCaseIndex, result.OutputHandle, result.ScheduleAppointmentHandle)
		if nextNodeID == "" {
			if result.OutputHandle != "" {
				log.Printf("flow route missing: execution=%d node=%s type=%s output=%s", execution.Id, currentNodeID, currentNode.Type, result.OutputHandle)
			}
			return e.completeExecution(execution.Id, currentNodeID, context)
		}
		currentNodeID = nextNodeID
		depth++
	}

	if depth >= maxDepth {
		return e.failExecution(execution.Id, fmt.Sprintf("flow excedeu profundidade máxima de %d nodes", maxDepth))
	}
	return nil
}

func (e *Engine) processNode(executionID int, node FlowNode, ctx ExecutionContext, conversation repo.Conversation, companyID int, flowData FlowData, persist bool) (ProcessResult, error) {
	allowed, err := repo.CompanyHasAccess(companyID)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("não foi possível validar a assinatura da empresa: %w", err)
	}
	if !allowed {
		return ProcessResult{}, errors.New("empresa sem assinatura ativa")
	}
	if persist {
		if err := e.saveExecutionState(executionID, node.ID, ctx, "running"); err != nil {
			return ProcessResult{}, err
		}
	}

	switch node.Type {
	case NodeStart:
		return ProcessResult{}, nil
	case NodeSendText:
		return ProcessResult{}, e.executeSendText(node, ctx, conversation)
	case NodeAIAgent:
		agentID := intVal(node.Data, "aiAgentId")
		agent, err := repo.GetAIAgent(agentID, companyID)
		if err != nil || !agent.IsActive {
			return ProcessResult{}, errors.New("agente de IA não está disponível")
		}
		session, err := repo.ActivateAIAgentSession(conversation.Id, agentID, companyID, safeModeEnabled(node.Data))
		if err != nil {
			return ProcessResult{}, fmt.Errorf("não foi possível transferir para o agente de IA: %w", err)
		}
		if message := strings.TrimSpace(agent.WelcomeMessage); message != "" {
			if err := e.messenger.SendText(conversation, ReplaceVariables(message, ctx)); err != nil {
				return ProcessResult{}, err
			}
		} else if message := strings.TrimSpace(stringVal(ctx, "response")); message != "" {
			session.Agent = agent
			incomingKey := fmt.Sprintf("flow:%d:%s", executionID, node.ID)
			go processAIAgentMessage(session, conversation, e.messenger.Connection, message, incomingKey)
		}
		return ProcessResult{}, nil
	case NodeSendTemplate:
		content := strings.TrimSpace(stringVal(node.Data, "_templateContent"))
		if content == "" {
			return ProcessResult{}, errors.New("snapshot do modelo de mensagem não está disponível")
		}
		return ProcessResult{}, e.messenger.SendText(conversation, ReplaceVariables(content, ctx))
	case NodeSendImage:
		return ProcessResult{}, e.executeSendImage(node, ctx, conversation)
	case NodeSendVideo:
		return ProcessResult{}, e.executeSendVideo(node, ctx, conversation)
	case NodeSendAudio:
		return ProcessResult{}, e.executeSendAudio(node, ctx, conversation)
	case NodeSendFile:
		return ProcessResult{}, e.executeSendFile(node, ctx, conversation)
	case NodeSendMenu:
		waitingID, _ := ctx["_waitingForMenuNodeId"].(string)
		if waitingID != node.ID {
			history := menuHistoryWithCurrent(ctx, node.ID)
			waitContext := mergeContext(ctx, ExecutionContext{
				"_waitingForMenuNodeId": node.ID,
				"_menuHistory":          history,
				"response":              nil,
				"userReplied":           nil,
				"_timedOut":             nil,
			})
			timeoutSeconds := applyNoResponseWait(flowData, node.ID, waitContext, intVal(node.Data, "timeout"))
			if err := e.saveWaitingState(executionID, node.ID, waitContext, timeoutSeconds, string(node.Type)); err != nil {
				return ProcessResult{}, err
			}
			if err := e.messenger.SendMenu(conversation, node.Data, waitContext); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{
				WaitForResponse: true,
				WaitPersisted:   true,
				Context:         waitContext,
			}, nil
		}
		handle, action := menuResponseResult(node.Data, fmt.Sprint(ctx["response"]), ctx)
		resultContext := ExecutionContext{"_waitingForMenuNodeId": nil, "_timedOut": nil}
		if action != "" {
			target, history := menuNavigationTarget(action, ctx)
			if target != "" {
				resultContext["_menuHistory"] = history
				return ProcessResult{Context: resultContext, DirectNodeID: target}, nil
			}
		}
		return ProcessResult{Context: resultContext, OutputHandle: handle}, nil
	case NodeSendButtons:
		waitingID, _ := ctx["_waitingForButtonsNodeId"].(string)
		if waitingID != node.ID {
			waitContext := mergeContext(ctx, ExecutionContext{
				"_waitingForButtonsNodeId": node.ID, "response": nil, "userReplied": nil, "_timedOut": nil,
			})
			timeoutSeconds := applyNoResponseWait(flowData, node.ID, waitContext, intVal(node.Data, "timeout"))
			if err := e.saveWaitingState(executionID, node.ID, waitContext, timeoutSeconds, string(node.Type)); err != nil {
				return ProcessResult{}, err
			}
			if err := e.messenger.SendButtons(conversation, node.Data, waitContext); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{WaitForResponse: true, WaitPersisted: true, Context: waitContext}, nil
		}
		return ProcessResult{Context: ExecutionContext{"_waitingForButtonsNodeId": nil, "_timedOut": nil},
			OutputHandle: buttonResponseHandle(node.Data, stringVal(ctx, "response"), ctx)}, nil
	case NodeCondition:
		metadata, err := repo.GetFlowConditionMetadata(conversation.Id, companyID)
		if err != nil {
			return ProcessResult{}, fmt.Errorf("não foi possível atualizar dados da condição: %w", err)
		}
		applyConditionMetadata(ctx, metadata)
		ctx["currentTime"] = conditionCurrentTime(flowData)
		result := e.EvaluateNodeCondition(node.Data, ctx)
		return ProcessResult{ConditionResult: &result}, nil
	case NodeSwitch:
		waitingID, _ := ctx["_waitingForSwitchNodeId"].(string)
		if waitingID != node.ID {
			waitContext := mergeContext(ctx, ExecutionContext{
				"_waitingForSwitchNodeId": node.ID, "response": nil, "userReplied": nil, "_timedOut": nil,
			})
			timeoutSeconds := applyNoResponseWait(flowData, node.ID, waitContext, intVal(node.Data, "timeout"))
			if err := e.saveWaitingState(executionID, node.ID, waitContext, timeoutSeconds, string(node.Type)); err != nil {
				return ProcessResult{}, err
			}
			if strings.TrimSpace(stringVal(node.Data, "message")) != "" {
				if err := e.executeSendText(node, ctx, conversation); err != nil {
					return ProcessResult{}, err
				}
			}
			return ProcessResult{WaitForResponse: true, WaitPersisted: true, Context: waitContext}, nil
		}
		if boolVal(ctx, "_timedOut") {
			fallback := -1
			return ProcessResult{SwitchCaseIndex: &fallback, Context: ExecutionContext{
				"_waitingForSwitchNodeId": nil, "_timedOut": nil,
			}}, nil
		}
		cases := switchCases(node.Data)
		idx := e.EvaluateSwitch(cases, ctx)
		return ProcessResult{SwitchCaseIndex: &idx, Context: ExecutionContext{
			"_waitingForSwitchNodeId": nil, "_timedOut": nil,
		}}, nil
	case NodeDelay:
		return ProcessResult{}, e.executeDelay(node.Data)
	case NodeChangeStatus:
		return ProcessResult{}, e.executeChangeStatus(node.Data, conversation.Id, executionID, companyID)
	case NodeAssign:
		if err := e.executeAssign(node.Data, conversation.Id, companyID); err != nil {
			return ProcessResult{}, err
		}
		if strings.TrimSpace(stringVal(node.Data, "message")) != "" {
			return ProcessResult{}, e.executeSendText(node, ctx, conversation)
		}
		return ProcessResult{}, nil
	case NodeHTTPRequest:
		requestStartedAt := time.Now()
		resp := e.messenger.ExecuteHTTPRequest(node.Data, ctx)
		outcome := httpResponseHandle(resp)
		outputHandle := httpRequestOutputHandle(node, flowData.Edges, outcome)
		routing := outputHandle
		if routing == "" {
			routing = "continue"
		}
		log.Printf("flow HTTP request finished: execution=%d node=%s status=%d outcome=%s routing=%s duration=%s", executionID, node.ID, intVal(resp, "status"), outcome, routing, time.Since(requestStartedAt).Round(time.Millisecond))
		resultContext := ExecutionContext{}
		if saveTo := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); saveTo != "" {
			resultContext[saveTo] = resp
		}
		mapped, mappingErrors := applyHTTPResponseMappings(resp, node.Data)
		for key, value := range mapped {
			resultContext[key] = value
		}
		if len(mappingErrors) > 0 {
			resultContext["_httpMappingErrors"] = mappingErrors
			log.Printf("HTTP response mapping warnings: execution=%d node=%s errors=%s", executionID, node.ID, strings.Join(mappingErrors, "; "))
		} else {
			resultContext["_httpMappingErrors"] = nil
		}
		return ProcessResult{Context: resultContext, OutputHandle: outputHandle}, nil
	case NodeSetContactField:
		value, err := flowCustomFieldValue(node, ctx)
		if err != nil {
			return ProcessResult{}, err
		}
		if err := repo.SetFlowContactCustomField(conversation.Id, executionID, companyID, strings.TrimSpace(stringVal(node.Data, "fieldKey")), value); err != nil {
			return ProcessResult{}, err
		}
		return ProcessResult{}, nil
	case NodeConversationSummary:
		summary, err := executeConversationSummary(node, companyID, conversation.Id)
		if err != nil {
			log.Printf("conversation summary failed: execution=%d node=%s error=%v", executionID, node.ID, err)
			return ProcessResult{OutputHandle: "failure"}, nil
		}
		if boolVal(node.Data, "persist") {
			if err := repo.AddFlowConversationEvent(conversation.Id, executionID, companyID, node.ID, "history", "Resumo da conversa: "+summary); err != nil {
				return ProcessResult{}, err
			}
		}
		return ProcessResult{OutputHandle: "success", Context: ExecutionContext{strings.TrimSpace(stringVal(node.Data, "saveResponseTo")): summary}}, nil
	case NodeNPSEvaluation:
		waitingID, _ := ctx["_waitingForNPSNodeId"].(string)
		if waitingID != node.ID {
			waitContext := mergeContext(ctx, ExecutionContext{"_waitingForNPSNodeId": node.ID, "response": nil, "userReplied": nil, "_timedOut": nil})
			if err := repo.StartFlowNPSEvaluation(conversation.Id, executionID, companyID, node.ID); err != nil {
				return ProcessResult{}, err
			}
			timeoutSeconds := applyNoResponseWait(flowData, node.ID, waitContext, intVal(node.Data, "timeout"))
			if err := e.saveWaitingState(executionID, node.ID, waitContext, timeoutSeconds, string(node.Type)); err != nil {
				return ProcessResult{}, err
			}
			if err := e.executeSendText(FlowNode{ID: node.ID, Type: NodeSendText, Data: map[string]interface{}{"message": stringVal(node.Data, "message")}}, ctx, conversation); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{WaitForResponse: true, WaitPersisted: true, Context: waitContext}, nil
		}
		if boolVal(ctx, "_timedOut") {
			if err := repo.TimeoutFlowNPSEvaluation(executionID, node.ID); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{OutputHandle: "timeout", Context: ExecutionContext{"_waitingForNPSNodeId": nil, "_timedOut": nil, "response": nil, "userReplied": false}}, nil
		}
		raw := strings.TrimSpace(fmt.Sprint(ctx["response"]))
		score, err := strconv.Atoi(raw)
		if err != nil || score < 0 || score > 10 {
			if err := repo.InvalidateFlowNPSEvaluation(executionID, node.ID, raw, time.Now()); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{OutputHandle: "invalid", Context: ExecutionContext{"_waitingForNPSNodeId": nil, "_timedOut": nil}}, nil
		}
		category := npsCategory(score)
		if err := repo.CompleteFlowNPSEvaluation(executionID, node.ID, score, category, raw, time.Now()); err != nil {
			return ProcessResult{}, err
		}
		return ProcessResult{OutputHandle: category, Context: ExecutionContext{
			"_waitingForNPSNodeId": nil, "_timedOut": nil,
			strings.TrimSpace(stringVal(node.Data, "saveScoreTo")): score,
		}}, nil
	case NodeWaitForResponse:
		return ProcessResult{WaitForResponse: true, Context: ExecutionContext{
			"response": nil, "userReplied": nil, "_timedOut": nil,
		}}, nil
	case NodeInput:
		waitingID, _ := ctx["_waitingForInputNodeId"].(string)
		if waitingID != node.ID {
			waitContext := mergeContext(ctx, ExecutionContext{
				"_waitingForInputNodeId": node.ID, "response": nil, "userReplied": nil, "_timedOut": nil,
			})
			timeoutSeconds := applyNoResponseWait(flowData, node.ID, waitContext, intVal(node.Data, "timeout"))
			if err := e.saveWaitingState(executionID, node.ID, waitContext, timeoutSeconds, string(node.Type)); err != nil {
				return ProcessResult{}, err
			}
			if msg := stringVal(node.Data, "message"); msg != "" {
				if err := e.executeSendText(node, ctx, conversation); err != nil {
					return ProcessResult{}, err
				}
			}
			return ProcessResult{
				WaitForResponse: true,
				WaitPersisted:   true,
				Context:         waitContext,
			}, nil
		}
		if boolVal(ctx, "_timedOut") {
			return ProcessResult{OutputHandle: "timeout", Context: ExecutionContext{
				"_waitingForInputNodeId": nil, "_timedOut": nil, "response": nil, "userReplied": false,
			}}, nil
		}
		response := strings.TrimSpace(fmt.Sprint(ctx["response"]))
		messageType, _ := ctx["messageType"].(string)
		valid := validInputResponse(response, messageType, ctx["userReplied"] == true, stringVal(node.Data, "validationType"), stringVal(node.Data, "validationPattern"))
		log.Printf("input response evaluated: execution=%d node=%s validation=%s message_type=%q text_length=%d valid=%t", executionID, node.ID, stringVal(node.Data, "validationType"), messageType, len(response), valid)
		if !valid {
			return ProcessResult{Context: ExecutionContext{
				"_waitingForInputNodeId": nil, "_timedOut": nil,
			}, OutputHandle: "invalid"}, nil
		}
		varName := strings.TrimSpace(stringVal(node.Data, "variableName"))
		if varName == "" {
			varName = "userInput"
		}
		return ProcessResult{Context: ExecutionContext{
			varName:                  ctx["response"],
			"_waitingForInputNodeId": nil,
			"_timedOut":              nil,
		}, OutputHandle: "valid"}, nil
	case NodeCheckResponse:
		hasResponse := ctx["userReplied"] == true
		if saveTo := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); saveTo != "" {
			ctx[saveTo] = hasResponse
		}
		return ProcessResult{ConditionResult: &hasResponse}, nil
	case NodeSetVariable:
		name := strings.TrimSpace(stringVal(node.Data, "variableName"))
		return ProcessResult{Context: ExecutionContext{name: ReplaceVariables(stringVal(node.Data, "value"), ctx)}}, nil
	case NodeTag:
		operation := strings.TrimSpace(stringVal(node.Data, "operation"))
		tag := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "tag"), ctx))
		if tag == "" || len(tag) > 80 {
			return ProcessResult{}, errors.New("tag expandida deve possuir entre 1 e 80 caracteres")
		}
		tags, err := repo.UpdateConversationTag(conversation.Id, executionID, companyID, operation, tag)
		if err != nil {
			return ProcessResult{}, err
		}
		sse.NotifyConversations(companyID)
		return ProcessResult{Context: ExecutionContext{"conversationTags": tags}}, nil
	case NodeUpdateContact:
		field := strings.TrimSpace(stringVal(node.Data, "field"))
		value := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "value"), ctx))
		if value == "" || len(value) > 320 {
			return ProcessResult{}, errors.New("valor expandido do contato é inválido")
		}
		if field == "email" {
			address, err := mail.ParseAddress(value)
			if err != nil || address.Address != value {
				return ProcessResult{}, errors.New("email expandido é inválido")
			}
		}
		if err := repo.UpdateFlowContact(conversation.Id, executionID, companyID, field, value); err != nil {
			return ProcessResult{}, err
		}
		contextKey := "contactName"
		if field == "email" {
			contextKey = "contactEmail"
		}
		sse.NotifyConversations(companyID)
		return ProcessResult{Context: ExecutionContext{contextKey: value}}, nil
	case NodeBusinessHours:
		companySettings, err := repo.GetCompanySettings(companyID)
		if err != nil {
			return ProcessResult{}, err
		}
		policy := companySettings.Operations
		if !policy.BusinessHoursEnabled {
			open := true
			return ProcessResult{ConditionResult: &open}, nil
		}
		hours := BusinessHours{Enabled: true, Timezone: companySettings.Timezone, Days: policy.BusinessHoursDays,
			StartTime: policy.BusinessHoursStart, EndTime: policy.BusinessHoursEnd, LunchEnabled: policy.BusinessHoursLunchEnabled,
			LunchStart: policy.BusinessHoursLunchStart, LunchEnd: policy.BusinessHoursLunchEnd}
		open, err := businessHoursOpenAt(hours, time.Now())
		return ProcessResult{ConditionResult: &open}, err
	case NodeRandomSplit:
		return ProcessResult{OutputHandle: randomSplitHandle(intVal(node.Data, "percentage"), rand.IntN(100))}, nil
	case NodeMessageType:
		return ProcessResult{OutputHandle: messageTypeHandle(ctx["messageType"])}, nil
	case NodeSmartAssign:
		var departmentID *int
		if id := intVal(node.Data, "departmentId"); id > 0 {
			departmentID = &id
		}
		assignment, err := repo.AssignConversationSmart(conversation.Id, executionID, companyID, departmentID, stringVal(node.Data, "strategy"))
		if errors.Is(err, sql.ErrNoRows) {
			return ProcessResult{OutputHandle: "unavailable"}, nil
		}
		if err != nil {
			return ProcessResult{}, err
		}
		sse.NotifyConversations(companyID)
		return ProcessResult{OutputHandle: "assigned", Context: ExecutionContext{
			"assignedAgentId": assignment.AgentID, "assignedAgentName": assignment.AgentName,
		}}, nil
	case NodeAddNote:
		content := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "content"), ctx))
		if content == "" || len(content) > 10000 {
			return ProcessResult{}, errors.New("conteúdo expandido da nota é inválido")
		}
		if err := repo.AddFlowConversationEvent(conversation.Id, executionID, companyID, node.ID, stringVal(node.Data, "eventType"), content); err != nil {
			return ProcessResult{}, err
		}
		return ProcessResult{}, nil
	case NodeCallback:
		payload, err := json.Marshal(map[string]interface{}{
			"event":          strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "eventName"), ctx)),
			"flowId":         ctx["_flowId"],
			"executionId":    executionID,
			"conversationId": conversation.Id,
			"variables":      publicFlowContext(ctx),
		})
		if err != nil {
			return ProcessResult{}, err
		}
		callbackData := map[string]interface{}{
			"method": "POST", "url": stringVal(node.Data, "url"), "bodyMode": "rawJSON", "body": string(payload),
			"timeout": intVal(node.Data, "timeout"), "followRedirects": boolVal(node.Data, "followRedirects"),
		}
		if token := strings.TrimSpace(stringVal(node.Data, "token")); token != "" {
			callbackData["headerMode"] = "fields"
			callbackData["headerFields"] = []interface{}{map[string]interface{}{"key": "Authorization", "value": "Bearer " + token}}
		}
		response := e.messenger.ExecuteHTTPRequest(callbackData, ctx)
		status := intVal(response, "status")
		handle := "success"
		if boolVal(response, "error") || status < 200 || status >= 300 {
			handle = "failure"
		}
		return ProcessResult{OutputHandle: handle, Context: ExecutionContext{"callbackResponse": response}}, nil
	case NodeSubflow:
		subflowData, err := embeddedSubflowData(node.Data)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"subflowError": err.Error()}}, nil
		}
		subflowContext, err := e.executeInlineFlow(executionID, subflowData, ctx, conversation, companyID)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"subflowError": err.Error()}}, nil
		}
		subflowContext["subflowError"] = nil
		return ProcessResult{OutputHandle: "success", Context: subflowContext}, nil
	case NodeAI:
		credentialID := intVal(node.Data, "credentialId")
		if credentialID <= 0 {
			return e.processLegacyAINode(node, ctx)
		}
		credential, err := repo.GetAICredential(credentialID, companyID)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"aiError": "credencial de IA não encontrada"}}, nil
		}
		apiKey, err := llm.DecryptAPIKey(credential.APIKeyCiphertext)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"aiError": err.Error()}}, nil
		}
		systemPrompt := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "systemPrompt"), ctx))
		prompt := ReplaceVariables(stringVal(node.Data, "prompt"), ctx)
		knowledgeBase := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "knowledgeBase"), ctx))
		if knowledgeBase != "" {
			systemPrompt = strings.TrimSpace(systemPrompt + "\n\nBASE DE CONHECIMENTO (fonte prioritária):\n" + relevantKnowledge(knowledgeBase, prompt, 30000))
		}
		messages := []llm.Message{{Role: "user", Content: prompt}}
		requestContext, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		response, err := llm.Complete(requestContext, llm.Request{
			Provider: credential.Provider, Model: credential.Model, APIKey: apiKey, System: systemPrompt, Messages: messages,
			MaxOutputTokens: 1024, Temperature: 0.4, SafeMode: safeModeEnabled(node.Data),
		})
		if err != nil {
			logLLMFailure("flow_ai", companyID, credential.Id, credential.Provider, credential.Model, err)
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"aiError": llm.SafeErrorMessage(err)}}, nil
		}
		variableName := strings.TrimSpace(stringVal(node.Data, "saveResponseTo"))
		return ProcessResult{OutputHandle: "success", Context: ExecutionContext{
			variableName: response.Text, "aiError": nil,
		}}, nil
	case NodeMediaTranscription:
		credential, err := resolveFlowAICredential(node, companyID)
		if err != nil || (credential.Provider != "openai" && credential.Provider != "groq") {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "credencial incompatível com transcrição de mídia"}}, nil
		}
		messageID, _ := ctx["messageId"].(string)
		var attachment repo.MessageData
		if strings.TrimSpace(messageID) != "" {
			attachment, err = repo.GetIncomingMediaByMessage(messageID, companyID)
		} else {
			attachment, err = repo.GetLatestIncomingMedia(conversation.Id, companyID)
		}
		if err != nil || strings.TrimSpace(attachment.MediaPath) == "" {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "nenhuma mídia recebida foi encontrada"}}, nil
		}
		filePath, err := media.FilePath(companyID, attachment.MediaPath)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "caminho de mídia inválido"}}, nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "não foi possível ler a mídia"}}, nil
		}
		kind := attachment.MediaType
		if kind == "audio" {
			enabled, permissionErr := repo.CompanyAudioTranscriptionEnabled(companyID)
			if permissionErr != nil || !enabled {
				return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "a ferramenta de transcrição de áudio não está contratada na assinatura"}}, nil
			}
		}
		if !llm.SupportsMediaAnalysis(credential.Provider, kind) {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "a credencial Groq suporta somente transcrição de áudio"}}, nil
		}
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		prompt := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "prompt"), ctx))
		if len(prompt) > 10000 {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": "prompt expandido excede o limite de 10000 caracteres"}}, nil
		}
		response, err := llm.AnalyzeMedia(context.Background(), credential.Provider, credential.Model, credential.APIKey, kind, contentType, data, prompt, safeModeEnabled(node.Data))
		if err != nil {
			logLLMFailure("flow_media_transcription", companyID, credential.ID, credential.Provider, credential.Model, err)
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"transcriptionError": llm.SafeErrorMessage(err)}}, nil
		}
		variableName := strings.TrimSpace(stringVal(node.Data, "saveResponseTo"))
		return ProcessResult{OutputHandle: "success", Context: ExecutionContext{variableName: response.Text, "transcriptionError": nil}}, nil
	case NodeScheduleMessage:
		message := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "message"), ctx))
		if message == "" || len(message) > 10000 {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"scheduleError": "mensagem expandida inválida"}}, nil
		}
		dueAt, err := repo.ScheduleFlowMessage(conversation.Id, executionID, companyID, node.ID, message, intVal(node.Data, "delayMinutes"))
		if err != nil {
			return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"scheduleError": err.Error()}}, nil
		}
		return ProcessResult{OutputHandle: "scheduled", Context: ExecutionContext{
			"scheduledMessageAt": dueAt.Format(time.RFC3339), "scheduleError": nil,
		}}, nil
	case NodeEnd:
		return ProcessResult{}, nil
	default:
		return ProcessResult{}, fmt.Errorf("tipo de node desconhecido: %s", node.Type)
	}
}

// processLegacyAINode keeps existing persisted flows working until they are saved with a credentialId.
func (e *Engine) processLegacyAINode(node FlowNode, ctx ExecutionContext) (ProcessResult, error) {
	apiURL := strings.TrimSpace(env.Backend["AI_API_URL"])
	apiKey := strings.TrimSpace(env.Backend["AI_API_KEY"])
	model := strings.TrimSpace(env.Backend["AI_MODEL"])
	if apiURL == "" || apiKey == "" || model == "" {
		return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{"aiError": "integração de IA não configurada"}}, nil
	}
	messages := []map[string]string{}
	systemPrompt := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "systemPrompt"), ctx))
	if safeModeEnabled(node.Data) {
		systemPrompt = llm.ApplySafeMode(systemPrompt)
	}
	if systemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": ReplaceVariables(stringVal(node.Data, "prompt"), ctx)})
	body, err := json.Marshal(map[string]interface{}{"model": model, "messages": messages})
	if err != nil {
		return ProcessResult{}, err
	}
	response := e.messenger.ExecuteHTTPRequest(map[string]interface{}{
		"method": "POST", "url": apiURL, "bodyMode": "rawJSON", "body": string(body), "timeout": 60000,
		"headerMode": "fields", "headerFields": []interface{}{
			map[string]interface{}{"key": "Authorization", "value": "Bearer " + apiKey},
		},
	}, ExecutionContext{})
	text, ok := aiResponseText(response)
	if !ok {
		return ProcessResult{OutputHandle: "failure", Context: ExecutionContext{
			"aiError": aiResponseError(response), "aiResponse": response,
		}}, nil
	}
	variableName := strings.TrimSpace(stringVal(node.Data, "saveResponseTo"))
	return ProcessResult{OutputHandle: "success", Context: ExecutionContext{variableName: text, "aiError": nil}}, nil
}

func applyConditionMetadata(ctx ExecutionContext, metadata repo.FlowConditionMetadata) {
	contactName := metadata.ContactName
	if contactName == "" {
		contactName, _ = ctx["senderName"].(string)
	}
	ctx["contactName"] = contactName
	ctx["contactNumber"] = metadata.ContactNumber
	if metadata.ContactEmail != "" || ctx["contactEmail"] == nil {
		ctx["contactEmail"] = metadata.ContactEmail
	}
	ctx["conversationTags"] = metadata.ConversationTags
	ctx["department"] = metadata.Department
	ctx["connectionType"] = metadata.ConnectionType
}

func conditionCurrentTime(flowData FlowData) string {
	timezone := "America/Sao_Paulo"
	if startNode := findNodeByType(flowData.Nodes, NodeStart); startNode != nil {
		if raw := startNode.Data["businessHours"]; raw != nil {
			encoded, _ := json.Marshal(raw)
			var hours BusinessHours
			if json.Unmarshal(encoded, &hours) == nil && strings.TrimSpace(hours.Timezone) != "" {
				timezone = hours.Timezone
			}
		}
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.Local
	}
	return time.Now().In(location).Format("15:04")
}

func (e *Engine) getNextNode(currentNodeID, nodeType string, edges []FlowEdge, conditionResult *bool, switchCaseIndex *int, outputHandle, scheduleHandle string) (string, error) {
	outgoing := filterEdges(edges, currentNodeID)
	if len(outgoing) == 0 {
		return "", nil
	}
	if nodeType == string(NodeSwitch) && switchCaseIndex != nil {
		if *switchCaseIndex == -1 {
			for _, edge := range outgoing {
				if edge.SourceHandle == "fallback" {
					return edge.Target, nil
				}
			}
			return "", nil
		}
		handleID := fmt.Sprintf("case-%d", *switchCaseIndex)
		for _, edge := range outgoing {
			if edge.SourceHandle == handleID {
				return edge.Target, nil
			}
		}
		return "", nil
	}
	if conditionResult != nil {
		for _, edge := range outgoing {
			if *conditionResult && (edge.SourceHandle == "true" || edge.SourceHandle == "") {
				return edge.Target, nil
			}
			if !*conditionResult && edge.SourceHandle == "false" {
				return edge.Target, nil
			}
		}
		return "", nil
	}
	if outputHandle != "" {
		for _, edge := range outgoing {
			if edge.SourceHandle == outputHandle || (nodeType == string(NodeSendMenu) && outputHandle == "fallback" && edge.SourceHandle == "") || (nodeType == string(NodeHTTPRequest) && outputHandle == "success" && edge.SourceHandle == "") {
				return edge.Target, nil
			}
		}
		if (outputHandle == "replied" || outputHandle == "valid") && (nodeType == string(NodeInput) || nodeType == string(NodeWaitForResponse)) {
			for _, edge := range outgoing {
				if edge.SourceHandle == "" || (nodeType == string(NodeInput) && outputHandle == "valid" && edge.SourceHandle == "replied") {
					return edge.Target, nil
				}
			}
		}
		return "", nil
	}
	if scheduleHandle != "" {
		for _, edge := range outgoing {
			if edge.SourceHandle == scheduleHandle {
				return edge.Target, nil
			}
		}
		if len(outgoing) > 0 {
			return outgoing[0].Target, nil
		}
	}
	return outgoing[0].Target, nil
}

func httpResponseHandle(response map[string]interface{}) string {
	if boolVal(response, "error") {
		return "failure"
	}
	status := intVal(response, "status")
	if status >= 200 && status < 300 {
		return "success"
	}
	return "failure"
}

func httpRequestOutputHandle(node FlowNode, edges []FlowEdge, outcome string) string {
	if configured, exists := node.Data["routeByStatus"]; exists {
		if enabled, ok := configured.(bool); ok && enabled {
			return outcome
		}
		return ""
	}
	for _, edge := range edges {
		if edge.Source == node.ID && edge.SourceHandle == "failure" {
			return outcome
		}
	}
	return ""
}

func flowCustomFieldValue(node FlowNode, ctx ExecutionContext) (interface{}, error) {
	value := strings.TrimSpace(ReplaceVariables(stringVal(node.Data, "value"), ctx))
	switch stringVal(node.Data, "fieldType") {
	case "text":
		return value, nil
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("valor do campo personalizado não é numérico")
		}
		return parsed, nil
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("valor do campo personalizado deve ser true ou false")
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("tipo de campo personalizado inválido")
	}
}

type flowAICredential struct {
	ID              int
	Provider        string
	Model           string
	APIKey          string
	MaxOutputTokens int
	Temperature     float64
}

func resolveFlowAICredential(node FlowNode, companyID int) (flowAICredential, error) {
	if credentialID := intVal(node.Data, "credentialId"); credentialID > 0 {
		credential, err := repo.GetAICredential(credentialID, companyID)
		if err != nil || !credential.HasAPIKey {
			return flowAICredential{}, fmt.Errorf("credencial de IA não encontrada")
		}
		apiKey, err := llm.DecryptAPIKey(credential.APIKeyCiphertext)
		if err != nil {
			return flowAICredential{}, err
		}
		return flowAICredential{
			ID: credential.Id, Provider: credential.Provider, Model: credential.Model, APIKey: apiKey,
			MaxOutputTokens: 1024, Temperature: 0.4,
		}, nil
	}

	// Flows persisted before credentials were selectable stored an AI agent ID.
	agent, err := repo.GetAIAgent(intVal(node.Data, "aiAgentId"), companyID)
	if err != nil || !agent.IsActive {
		return flowAICredential{}, fmt.Errorf("credencial de IA não encontrada")
	}
	apiKey, err := llm.DecryptAPIKey(agent.APIKeyCiphertext)
	if err != nil {
		return flowAICredential{}, err
	}
	return flowAICredential{
		ID: agent.CredentialId, Provider: agent.Provider, Model: agent.Model, APIKey: apiKey,
		MaxOutputTokens: agent.MaxOutputTokens, Temperature: agent.Temperature,
	}, nil
}

func executeConversationSummary(node FlowNode, companyID, conversationID int) (string, error) {
	credential, err := resolveFlowAICredential(node, companyID)
	if err != nil {
		return "", err
	}
	history, err := repo.GetAIConversationHistory(conversationID, companyID, intVal(node.Data, "maxMessages"))
	if err != nil {
		return "", err
	}
	if len(history) == 0 {
		return "", fmt.Errorf("conversa sem mensagens para resumir")
	}
	instruction := "Produza agora um resumo objetivo da conversa acima. Inclua solicitação do cliente, decisões, pendências e próximos passos. Não invente informações."
	if custom := strings.TrimSpace(stringVal(node.Data, "instructions")); custom != "" {
		instruction += " Instruções adicionais: " + custom
	}
	requestContext, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	response, err := llm.Complete(requestContext, llm.Request{
		Provider: credential.Provider, Model: credential.Model, APIKey: credential.APIKey,
		System:   "Você resume conversas de atendimento em português do Brasil, com clareza e concisão.",
		Messages: normalizeAIHistory(history, instruction), MaxOutputTokens: credential.MaxOutputTokens, Temperature: credential.Temperature,
		SafeMode: safeModeEnabled(node.Data),
	})
	if err != nil {
		logLLMFailure("flow_conversation_summary", companyID, credential.ID, credential.Provider, credential.Model, err)
		return "", fmt.Errorf("não foi possível gerar o resumo: %s", llm.SafeErrorMessage(err))
	}
	summary := strings.TrimSpace(response.Text)
	if summary == "" {
		return "", fmt.Errorf("provedor retornou resumo vazio")
	}
	return summary, nil
}

func npsCategory(score int) string {
	if score >= 9 {
		return "promoter"
	}
	if score >= 7 {
		return "passive"
	}
	return "detractor"
}

func (e *Engine) executeSendText(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	message := ReplaceVariables(stringVal(node.Data, "message"), ctx)
	if message == "" {
		return nil
	}
	return e.messenger.SendText(conversation, message)
}

func (e *Engine) executeSendImage(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "imagePath", "imageUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendMedia(conversation, "image", source, caption)
}

func (e *Engine) executeSendVideo(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "videoPath", "videoUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendMedia(conversation, "video", source, caption)
}

func (e *Engine) executeSendAudio(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "audioPath", "audioUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	return e.messenger.SendAudio(conversation, source)
}

func (e *Engine) executeSendFile(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "filePath", "fileUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendDocument(conversation, source, caption, stringVal(node.Data, "mediaName"))
}

func flowMediaSource(data map[string]interface{}, pathKey, urlKey string, companyID int, ctx ExecutionContext) (string, error) {
	if path := strings.TrimSpace(stringVal(data, pathKey)); path != "" {
		return media.SignedURL(companyID, path)
	}
	return ReplaceVariables(strings.TrimSpace(stringVal(data, urlKey)), ctx), nil
}

func (e *Engine) executeDelay(data map[string]interface{}) error {
	seconds := delaySeconds(data, time.Now().UnixNano())
	if seconds <= 0 {
		return nil
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

func delaySeconds(data map[string]interface{}, randomValue int64) int {
	seconds := 0
	if stringVal(data, "delayType") == "range" {
		minS := intVal(data, "minSeconds")
		maxS := intVal(data, "maxSeconds")
		if minS > 60 {
			minS = 60
		}
		if maxS > 60 {
			maxS = 60
		}
		if minS > 0 && maxS >= minS {
			if randomValue < 0 {
				randomValue = -randomValue
			}
			seconds = minS + int(randomValue%int64(maxS-minS+1))
		}
	} else {
		seconds = intVal(data, "seconds")
	}
	if seconds <= 0 {
		return 0
	}
	if seconds > 60 {
		seconds = 60
	}
	return seconds
}

func (e *Engine) executeChangeStatus(data map[string]interface{}, conversationID, executionID, companyID int) error {
	status := stringVal(data, "status")
	if status == "" {
		return nil
	}
	if err := repo.UpdateConversationStatus(conversationID, executionID, status); err != nil {
		return err
	}
	sse.NotifyConversations(companyID)
	return nil
}

func (e *Engine) executeAssign(data map[string]interface{}, conversationID, companyID int) error {
	assignType := stringVal(data, "assignType")
	if assignType == "" {
		assignType = "agent"
	}
	assignID := intVal(data, "assignId")
	if assignID <= 0 {
		return nil
	}
	if assignType == "department" {
		if err := repo.AssignConversationDepartment(conversationID, assignID, companyID); err != nil {
			return err
		}
		sse.NotifyConversations(companyID)
		return nil
	}
	if assignType != "agent" {
		return fmt.Errorf("tipo de atribuição inválido: %s", assignType)
	}
	if err := repo.AssignConversationAgent(conversationID, assignID, companyID); err != nil {
		return err
	}
	sse.NotifyConversations(companyID)
	return nil
}

func applyNoResponseWait(flowData FlowData, nodeID string, ctx ExecutionContext, configuredTimeout int) int {
	settings := flowData.Settings.NoResponse
	if !settings.Enabled {
		return configuredTimeout
	}
	ctx[noResponseWaitNodeKey] = nodeID
	return settings.ReminderMinutes * 60
}

func (e *Engine) handleNoResponse(settings NoResponseSettings, conversation repo.Conversation, companyID int, ctx ExecutionContext) error {
	message := ReplaceVariables(strings.TrimSpace(settings.ReminderMessage), ctx)
	reminderErr := e.messenger.SendText(conversation, message)
	transferErr := e.executeAssign(map[string]interface{}{
		"assignType": settings.TargetType,
		"assignId":   settings.TargetID,
	}, conversation.Id, companyID)
	if reminderErr != nil || transferErr != nil {
		return errors.Join(reminderErr, transferErr)
	}
	ctx["noResponseTriggered"] = true
	return nil
}

func (e *Engine) checkBusinessHours(startNode *FlowNode, conversation repo.Conversation, ctx ExecutionContext) (string, error) {
	raw, ok := startNode.Data["businessHours"]
	if !ok || raw == nil {
		return "ok", nil
	}
	b, _ := json.Marshal(raw)
	var bh BusinessHours
	if json.Unmarshal(b, &bh) != nil || !bh.Enabled {
		return "ok", nil
	}
	open, err := businessHoursOpenAt(bh, time.Now())
	if err != nil {
		return "", err
	}
	if open {
		return "ok", nil
	}
	if bh.OutsideAction == "message" && bh.OutsideMessage != "" {
		if err := e.messenger.SendText(conversation, ReplaceVariables(bh.OutsideMessage, ctx)); err != nil {
			return "", err
		}
		return "message_sent", nil
	}
	return "blocked", nil
}

func businessHoursOpenAt(bh BusinessHours, instant time.Time) (bool, error) {
	locName := strings.TrimSpace(bh.Timezone)
	if locName == "" {
		locName = "America/Sao_Paulo"
	}
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return false, fmt.Errorf("fuso horário inválido: %s", locName)
	}
	now := instant.In(loc)
	weekday := int(now.Weekday())
	allowedDays := bh.Days
	if len(allowedDays) == 0 {
		allowedDays = []int{1, 2, 3, 4, 5}
	}
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes, startOK := parseTimeMinutes(bh.StartTime)
	endMinutes, endOK := parseTimeMinutes(bh.EndTime)
	if !startOK || !endOK || startMinutes == endMinutes {
		return false, fmt.Errorf("horário de atendimento inválido")
	}
	scheduleWeekday := weekday
	withinHours := currentMinutes >= startMinutes && currentMinutes < endMinutes
	if startMinutes > endMinutes {
		withinHours = currentMinutes >= startMinutes || currentMinutes < endMinutes
		if currentMinutes < endMinutes {
			scheduleWeekday = (weekday + 6) % 7
		}
	}
	allowed := false
	for _, day := range allowedDays {
		if day == scheduleWeekday {
			allowed = true
			break
		}
	}
	outside := !allowed || !withinHours
	if !outside && bh.LunchEnabled {
		lunchStart, lunchStartOK := parseTimeMinutes(bh.LunchStart)
		lunchEnd, lunchEndOK := parseTimeMinutes(bh.LunchEnd)
		if !lunchStartOK || !lunchEndOK || lunchStart >= lunchEnd {
			return false, fmt.Errorf("horário de almoço inválido")
		}
		if currentMinutes >= lunchStart && currentMinutes < lunchEnd {
			outside = true
		}
	}
	return !outside, nil
}

func randomSplitHandle(percentage, roll int) string {
	if roll < percentage {
		return "a"
	}
	return "b"
}

func validInputResponse(response, messageType string, received bool, validationType, pattern string) bool {
	switch strings.TrimSpace(validationType) {
	case "", "any":
		return received || response != "" || strings.TrimSpace(messageType) != ""
	case "text":
		return response != ""
	case "email":
		address, err := mail.ParseAddress(response)
		return err == nil && address.Address == response
	case "number":
		_, err := strconv.ParseFloat(strings.ReplaceAll(response, ",", "."), 64)
		return err == nil
	case "regex":
		expression, err := regexp.Compile(pattern)
		return err == nil && expression.MatchString(response)
	default:
		return false
	}
}

func messageTypeHandle(value interface{}) string {
	messageType := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch messageType {
	case "text", "image", "video", "audio", "document":
		return messageType
	default:
		return "other"
	}
}

func publicFlowContext(ctx ExecutionContext) ExecutionContext {
	public := ExecutionContext{}
	for key, value := range ctx {
		if !strings.HasPrefix(key, "_") {
			public[key] = value
		}
	}
	return public
}

func aiResponseText(response map[string]interface{}) (string, bool) {
	status := intVal(response, "status")
	if boolVal(response, "error") || status < 200 || status >= 300 {
		return "", false
	}
	data, _ := response["data"].(map[string]interface{})
	choices, _ := data["choices"].([]interface{})
	if len(choices) == 0 {
		return "", false
	}
	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	content, _ := message["content"].(string)
	content = strings.TrimSpace(content)
	return content, content != ""
}

func aiResponseError(response map[string]interface{}) string {
	if message := strings.TrimSpace(fmt.Sprint(response["message"])); message != "" && message != "<nil>" {
		return message
	}
	if data, ok := response["data"].(map[string]interface{}); ok {
		if providerError, ok := data["error"].(map[string]interface{}); ok {
			if message := strings.TrimSpace(fmt.Sprint(providerError["message"])); message != "" && message != "<nil>" {
				return message
			}
		}
	}
	if status := intVal(response, "status"); status > 0 {
		return fmt.Sprintf("provedor de IA respondeu com status %d", status)
	}
	return "resposta inválida do provedor de IA"
}

func resolveSubflowSnapshots(flowData *FlowData, companyID int, ancestors map[int]bool) error {
	for index := range flowData.Nodes {
		node := &flowData.Nodes[index]
		if node.Type == NodeSendTemplate {
			template, err := repo.GetFlowMessageTemplate(intVal(node.Data, "templateId"), companyID)
			if err != nil {
				return fmt.Errorf("carregar modelo de mensagem: %w", err)
			}
			node.Data["_templateContent"] = template.Content
			node.Data["_templateRevision"] = template.Revision
			continue
		}
		if node.Type != NodeSubflow {
			continue
		}
		flowID := intVal(node.Data, "flowId")
		if ancestors[flowID] {
			return fmt.Errorf("subfluxo cíclico detectado no flow %d", flowID)
		}
		flow, err := repo.GetChatbotFlowById(flowID, companyID)
		if err != nil {
			return fmt.Errorf("carregar subfluxo %d: %w", flowID, err)
		}
		if err := ValidateActiveFlowData(flow.FlowData); err != nil {
			return fmt.Errorf("subfluxo %q inválido: %w", flow.Name, err)
		}
		subflowData, err := ParseFlowData(flow.FlowData)
		if err != nil {
			return err
		}
		nextAncestors := make(map[int]bool, len(ancestors)+1)
		for ancestor := range ancestors {
			nextAncestors[ancestor] = true
		}
		nextAncestors[flowID] = true
		if err := resolveSubflowSnapshots(&subflowData, companyID, nextAncestors); err != nil {
			return err
		}
		namespaceFlowData(&subflowData, fmt.Sprintf("%s:%d", node.ID, flowID))
		node.Data["_flowSnapshot"] = subflowData
		node.Data["_flowRevision"] = flow.Revision
	}
	return nil
}

func namespaceFlowData(flowData *FlowData, prefix string) {
	ids := make(map[string]string, len(flowData.Nodes))
	for index := range flowData.Nodes {
		oldID := flowData.Nodes[index].ID
		newID := stableSubflowID("sfn", prefix, oldID)
		ids[oldID] = newID
		flowData.Nodes[index].ID = newID
		if flowData.Nodes[index].Type == NodeSubflow {
			if nested, err := embeddedSubflowData(flowData.Nodes[index].Data); err == nil {
				namespaceFlowData(&nested, newID)
				flowData.Nodes[index].Data["_flowSnapshot"] = nested
			}
		}
	}
	for index := range flowData.Edges {
		flowData.Edges[index].ID = stableSubflowID("sfe", prefix, flowData.Edges[index].ID)
		flowData.Edges[index].Source = ids[flowData.Edges[index].Source]
		flowData.Edges[index].Target = ids[flowData.Edges[index].Target]
	}
}

func stableSubflowID(kind, prefix, id string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + id))
	return fmt.Sprintf("%s_%x", kind, sum[:8])
}

func embeddedSubflowData(data map[string]interface{}) (FlowData, error) {
	raw, ok := data["_flowSnapshot"]
	if !ok {
		return FlowData{}, errors.New("snapshot do subfluxo não está disponível")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return FlowData{}, err
	}
	return ParseFlowData(encoded)
}

func (e *Engine) executeInlineFlow(executionID int, flowData FlowData, ctx ExecutionContext, conversation repo.Conversation, companyID int) (ExecutionContext, error) {
	startNode := findNodeByType(flowData.Nodes, NodeStart)
	if startNode == nil {
		return nil, errors.New("subfluxo não possui node Início")
	}
	currentNodeID := startNode.ID
	context := cloneContext(ctx)
	for depth := 0; currentNodeID != "" && depth < maxDepth; depth++ {
		if time.Since(e.startedAt) > maxExecutionTime {
			return nil, errors.New("subfluxo excedeu o tempo máximo da execução")
		}
		if err := repo.TouchFlowExecution(executionID, e.claimVersion); err != nil {
			return nil, err
		}
		node := findNode(flowData.Nodes, currentNodeID)
		if node == nil {
			return nil, fmt.Errorf("node %s do subfluxo não encontrado", currentNodeID)
		}
		switch node.Type {
		case NodeSendMenu, NodeSendButtons, NodeSwitch, NodeWaitForResponse, NodeInput, NodeNPSEvaluation:
			return nil, fmt.Errorf("subfluxo não pode usar o node assíncrono %s", node.Type)
		}
		result, err := e.processNode(executionID, *node, context, conversation, companyID, flowData, false)
		if err != nil {
			return nil, err
		}
		context = mergeContext(context, result.Context)
		if node.Type == NodeEnd {
			return context, nil
		}
		nextNodeID, err := e.getNextNode(node.ID, string(node.Type), flowData.Edges,
			result.ConditionResult, result.SwitchCaseIndex, result.OutputHandle, result.ScheduleAppointmentHandle)
		if err != nil {
			return nil, err
		}
		if nextNodeID == "" {
			return context, nil
		}
		currentNodeID = nextNodeID
	}
	return nil, fmt.Errorf("subfluxo excedeu profundidade máxima de %d nodes", maxDepth)
}

func parseTimeMinutes(value string) (int, bool) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func (e *Engine) saveExecutionState(executionID int, currentNodeID string, ctx ExecutionContext, status string) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	execution.Status = status
	execution.ClaimVersion = e.claimVersion
	execution.CurrentNodeId = &currentNodeID
	execution.Context, err = executionContextJSON(ctx)
	if err != nil {
		return err
	}
	if status == "running" {
		execution.ResumeAt = nil
		execution.WaitReason = nil
	}
	return repo.UpdateFlowExecution(execution)
}

func (e *Engine) saveWaitingState(executionID int, currentNodeID string, ctx ExecutionContext, timeoutSeconds int, reason string) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	execution.Status = "waiting"
	execution.ClaimVersion = e.claimVersion
	execution.CurrentNodeId = &currentNodeID
	execution.Context, err = executionContextJSON(ctx)
	if err != nil {
		return err
	}
	execution.WaitReason = &reason
	return repo.SetFlowExecutionWaiting(execution, timeoutSeconds)
}

func (e *Engine) completeExecution(executionID int, currentNodeID string, ctx ExecutionContext) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	now := time.Now()
	execution.Status = "completed"
	execution.ClaimVersion = e.claimVersion
	execution.CurrentNodeId = &currentNodeID
	execution.Context, err = executionContextJSON(ctx)
	if err != nil {
		return err
	}
	execution.CompletedAt = &now
	if err := repo.FinalizeFlowExecution(execution); err != nil {
		if errors.Is(err, repo.ErrFlowExecutionInactive) {
			return nil
		}
		return err
	}
	sse.NotifyConversations(execution.CompanyId)
	return nil
}

func executionContextJSON(ctx ExecutionContext) (json.RawMessage, error) {
	encoded, err := json.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("serializar contexto da execução: %w", err)
	}
	if len(encoded) > maxExecutionContextBytes {
		return nil, fmt.Errorf("contexto da execução excede o limite de %d bytes", maxExecutionContextBytes)
	}
	return encoded, nil
}

func (e *Engine) failExecution(executionID int, message string) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	now := time.Now()
	execution.Status = "failed"
	if e.claimVersion > 0 {
		execution.ClaimVersion = e.claimVersion
	} else {
		e.claimVersion = execution.ClaimVersion
	}
	execution.ErrorMessage = &message
	execution.CompletedAt = &now
	if err := repo.FinalizeFlowExecution(execution); err != nil {
		if errors.Is(err, repo.ErrFlowExecutionInactive) {
			return nil
		}
		return fmt.Errorf("%s: não foi possível persistir a falha: %w", message, err)
	}
	sse.NotifyConversations(execution.CompanyId)
	return errors.New(message)
}

func findNode(nodes []FlowNode, id string) *FlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func findNodeByType(nodes []FlowNode, nodeType FlowNodeType) *FlowNode {
	for i := range nodes {
		if nodes[i].Type == nodeType {
			return &nodes[i]
		}
	}
	return nil
}

func filterEdges(edges []FlowEdge, source string) []FlowEdge {
	out := []FlowEdge{}
	for _, e := range edges {
		if e.Source == source {
			out = append(out, e)
		}
	}
	return out
}

func switchCases(data map[string]interface{}) []map[string]interface{} {
	raw, ok := data["cases"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var cases []map[string]interface{}
	_ = json.Unmarshal(b, &cases)
	return cases
}

func cloneContext(src ExecutionContext) ExecutionContext {
	dst := ExecutionContext{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeContext(base, overlay ExecutionContext) ExecutionContext {
	out := cloneContext(base)
	for k, v := range overlay {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}
