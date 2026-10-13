package repo

import (
	"encoding/json"
	"fmt"
)

type flowDependencyData struct {
	Settings struct {
		NoResponse struct {
			Enabled    bool   `json:"enabled"`
			TargetType string `json:"targetType"`
			TargetID   int    `json:"targetId"`
		} `json:"noResponse"`
	} `json:"settings"`
	Nodes []struct {
		Type string `json:"type"`
		Data struct {
			FlowID       int `json:"flowId"`
			TemplateID   int `json:"templateId"`
			AIAgentID    int `json:"aiAgentId"`
			CredentialID int `json:"credentialId"`
		} `json:"data"`
	} `json:"nodes"`
}

func ValidateFlowDependencies(raw json.RawMessage, companyID, rootFlowID int) error {
	ancestors := map[int]bool{}
	if rootFlowID > 0 {
		ancestors[rootFlowID] = true
	}
	return validateFlowDependencies(raw, companyID, ancestors)
}

func validateFlowDependencies(raw json.RawMessage, companyID int, ancestors map[int]bool) error {
	var data flowDependencyData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	if data.Settings.NoResponse.Enabled {
		switch data.Settings.NoResponse.TargetType {
		case "agent":
			agent, err := GetAgentByIdAndCompany(data.Settings.NoResponse.TargetID, companyID)
			if err != nil || !agent.IsActive {
				return fmt.Errorf("agente %d do tratamento sem resposta não existe ou está inativo nesta empresa", data.Settings.NoResponse.TargetID)
			}
		case "department":
			var exists bool
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM departments
				WHERE id = $1 AND "companyId" = $2 AND "isActive" = true)`, data.Settings.NoResponse.TargetID, companyID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("setor %d do tratamento sem resposta não existe ou está inativo nesta empresa", data.Settings.NoResponse.TargetID)
			}
		}
	}
	for _, node := range data.Nodes {
		switch node.Type {
		case "sendTemplate":
			if _, err := GetFlowMessageTemplate(node.Data.TemplateID, companyID); err != nil {
				return fmt.Errorf("modelo de mensagem %d não existe nesta empresa", node.Data.TemplateID)
			}
		case "subflow":
			if ancestors[node.Data.FlowID] {
				return fmt.Errorf("referência cíclica ao subfluxo %d", node.Data.FlowID)
			}
			flow, err := GetChatbotFlowById(node.Data.FlowID, companyID)
			if err != nil {
				return fmt.Errorf("subfluxo %d não existe nesta empresa", node.Data.FlowID)
			}
			next := make(map[int]bool, len(ancestors)+1)
			for id := range ancestors {
				next[id] = true
			}
			next[node.Data.FlowID] = true
			if err := validateFlowDependencies(flow.FlowData, companyID, next); err != nil {
				return err
			}
		case "aiAgent":
			agent, err := GetAIAgent(node.Data.AIAgentID, companyID)
			if err != nil || !agent.IsActive {
				return fmt.Errorf("agente de IA %d não existe ou está inativo nesta empresa", node.Data.AIAgentID)
			}
		case "ai", "conversationSummary", "mediaTranscription":
			if node.Data.CredentialID <= 0 {
				agent, err := GetAIAgent(node.Data.AIAgentID, companyID)
				if err != nil || !agent.IsActive || (node.Type == "mediaTranscription" && agent.Provider != "openai" && agent.Provider != "groq") {
					return fmt.Errorf("agente de IA legado %d não existe ou está inativo nesta empresa", node.Data.AIAgentID)
				}
				continue
			}
			credential, err := GetAICredential(node.Data.CredentialID, companyID)
			if err != nil || !credential.HasAPIKey || (node.Type == "mediaTranscription" && credential.Provider != "openai" && credential.Provider != "groq") {
				return fmt.Errorf("credencial de IA %d não existe, não pertence a esta empresa ou não possui chave ativa", node.Data.CredentialID)
			}
		}
	}
	return nil
}

func ActiveFlowReferencesTemplate(templateID, companyID int) (bool, error) {
	var referenced bool
	err := db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM chatbot_flows flow,
		LATERAL jsonb_array_elements(COALESCE(flow."flowData"->'nodes', '[]'::jsonb)) node
		WHERE flow."companyId" = $2 AND flow."isActive" = true
		AND node->>'type' = 'sendTemplate' AND node->'data'->>'templateId' = $1::text
	)`, templateID, companyID).Scan(&referenced)
	return referenced, err
}
