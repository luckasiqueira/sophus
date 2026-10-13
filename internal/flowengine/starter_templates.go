package flowengine

// StarterTemplate is a complete, editable example. It contains no company IDs,
// credentials, private media or saved message-template references.
type StarterTemplate struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	Icon         string   `json:"icon"`
	TriggerType  string   `json:"triggerType"`
	TriggerValue string   `json:"triggerValue"`
	Steps        []string `json:"steps"`
	FlowData     FlowData `json:"flowData"`
}

// Return fresh graphs so editing an example never changes the catalog.
func StarterTemplates() []StarterTemplate {
	node := func(id string, kind FlowNodeType, x, y float64, data map[string]interface{}) FlowNode {
		if data == nil {
			data = map[string]interface{}{}
		}
		return FlowNode{ID: id, Type: kind, Position: map[string]float64{"x": x, "y": y}, Data: data}
	}
	text := func(id, message string, x, y float64) FlowNode {
		return node(id, NodeSendText, x, y, map[string]interface{}{"message": message})
	}
	edge := func(id, source, target, handle string) FlowEdge {
		return FlowEdge{ID: id, Source: source, Target: target, SourceHandle: handle}
	}
	return []StarterTemplate{
		{ID: "welcome", Name: "Boas-vindas simples", Description: "Receba o cliente com uma mensagem de boas-vindas.", Instructions: "Personalize a mensagem de boas-vindas. O modelo usa Sempre responde e não exige uma palavra específica do cliente.", Icon: "fa-hand-peace", TriggerType: "always", Steps: []string{"Início", "Boas-vindas", "Fim"}, FlowData: FlowData{
			Nodes: []FlowNode{node("start", NodeStart, 60, 80, nil), text("welcome", "Olá, {{contactName}}! Obrigado por entrar em contato. Como podemos ajudar?", 360, 80), node("end", NodeEnd, 660, 80, nil)},
			Edges: []FlowEdge{edge("e1", "start", "welcome", ""), edge("e2", "welcome", "end", "")},
		}},
		{ID: "capture-request", Name: "Capturar uma solicitação", Description: "Pergunte o assunto, capture a resposta e registre uma nota interna.", Instructions: "Edite a pergunta e a confirmação. A resposta fica na variável assunto e em uma nota interna. O prazo de resposta é de 10 minutos; resposta inválida ou ausente segue pelo aviso final.", Icon: "fa-clipboard-list", TriggerType: "always", Steps: []string{"Pergunta", "Capturar resposta", "Nota interna", "Confirmação"}, FlowData: FlowData{
			Nodes: []FlowNode{node("start", NodeStart, 60, 80, nil), text("question", "Olá! Conte em poucas palavras o que você precisa.", 360, 80), node("input", NodeInput, 660, 80, map[string]interface{}{"variableName": "assunto", "validationType": "text", "timeout": 600}), node("note", NodeAddNote, 960, 80, map[string]interface{}{"eventType": "note", "content": "Solicitação informada pelo cliente: {{assunto}}"}), text("confirmation", "Recebemos sua solicitação: {{assunto}}. Nossa equipe vai continuar o atendimento por aqui.", 960, 280), text("retry", "Não conseguimos registrar sua resposta. Envie uma nova mensagem para começar novamente.", 660, 480), node("end", NodeEnd, 1260, 280, nil)},
			Edges: []FlowEdge{edge("e1", "start", "question", ""), edge("e2", "question", "input", ""), edge("e3", "input", "note", "valid"), edge("e4", "note", "confirmation", ""), edge("e5", "confirmation", "end", ""), edge("e6", "input", "retry", "invalid"), edge("e7", "input", "retry", "timeout"), edge("e8", "retry", "end", "")},
		}},
		{ID: "simple-menu", Name: "Menu de informações", Description: "Ofereça duas opções e responda automaticamente à escolha do cliente.", Instructions: "Personalize as opções e os textos de horários e serviços. As opções já estão conectadas às respostas. O botão de fallback também permite adaptar o menu a respostas em texto.", Icon: "fa-list", TriggerType: "always", Steps: []string{"Menu", "Horários ou serviços", "Resposta", "Fim"}, FlowData: FlowData{
			Nodes: []FlowNode{node("start", NodeStart, 60, 80, nil), node("menu", NodeSendMenu, 360, 80, map[string]interface{}{"title": "Como podemos ajudar?", "description": "Escolha uma das opções abaixo.", "buttonText": "Ver opções", "footerText": "Atendimento", "useFallback": true, "timeout": 600, "sections": []interface{}{map[string]interface{}{"title": "Informações", "rows": []interface{}{map[string]interface{}{"title": "Horários", "description": "Quando estamos disponíveis", "rowId": "horarios", "_handleId": "hours"}, map[string]interface{}{"title": "Serviços", "description": "Conheça o que oferecemos", "rowId": "servicos", "_handleId": "services"}}}}}), text("hours", "Nosso horário de atendimento é de segunda a sexta, das 08h às 18h.", 660, 80), text("services", "Aqui você pode conhecer nossos serviços e tirar dúvidas com a equipe. Conte o que está procurando!", 660, 280), text("fallback", "Não entendemos sua escolha. Envie uma nova mensagem para consultar as opções novamente.", 660, 480), node("end", NodeEnd, 960, 280, nil)},
			Edges: []FlowEdge{edge("e1", "start", "menu", ""), edge("e2", "menu", "hours", "menu-hours"), edge("e3", "menu", "services", "menu-services"), edge("e4", "menu", "fallback", "fallback"), edge("e5", "hours", "end", ""), edge("e6", "services", "end", ""), edge("e7", "fallback", "end", "")},
		}},
		{ID: "business-hours", Name: "Resposta por horário", Description: "Envie respostas diferentes dentro e fora do horário comercial.", Instructions: "Ajuste os dias, os horários e o fuso do bloco Horário Comercial. O exemplo usa segunda a sexta, das 08h às 18h, no fuso de São Paulo.", Icon: "fa-clock", TriggerType: "always", Steps: []string{"Verificar horário", "Disponível ou ausente", "Fim"}, FlowData: FlowData{
			Nodes: []FlowNode{node("start", NodeStart, 60, 80, nil), node("hours", NodeBusinessHours, 360, 80, map[string]interface{}{"timezone": "America/Sao_Paulo", "days": []int{1, 2, 3, 4, 5}, "startTime": "08:00", "endTime": "18:00", "lunchEnabled": false}), text("open", "Estamos dentro do horário de atendimento. Conte como podemos ajudar!", 660, 80), text("closed", "Estamos fora do horário de atendimento. Funcionamos de segunda a sexta, das 08h às 18h. Deixe sua mensagem e retornaremos assim que possível.", 660, 280), node("end", NodeEnd, 960, 180, nil)},
			Edges: []FlowEdge{edge("e1", "start", "hours", ""), edge("e2", "hours", "open", "true"), edge("e3", "hours", "closed", "false"), edge("e4", "open", "end", ""), edge("e5", "closed", "end", "")},
		}},
		{ID: "human-handoff", Name: "Encaminhar para a equipe", Description: "Distribua a conversa para um operador e trate a ausência de operadores.", Instructions: "A distribuição usa os operadores ativos da empresa, priorizando quem tem menos atendimentos. Você pode selecionar um departamento no bloco Distribuição Inteligente.", Icon: "fa-user-check", TriggerType: "always", Steps: []string{"Aviso", "Distribuir atendimento", "Destino ou indisponibilidade"}, FlowData: FlowData{
			Nodes: []FlowNode{node("start", NodeStart, 60, 80, nil), text("welcome", "Vamos encaminhar sua conversa para uma pessoa da nossa equipe.", 360, 80), node("assign", NodeSmartAssign, 660, 80, map[string]interface{}{"strategy": "leastLoaded"}), text("unavailable", "Não há operador disponível para receber este atendimento no momento. Sua mensagem permanece registrada para nossa equipe.", 960, 280), node("end", NodeEnd, 1260, 80, nil)},
			Edges: []FlowEdge{edge("e1", "start", "welcome", ""), edge("e2", "welcome", "assign", ""), edge("e3", "assign", "end", "assigned"), edge("e4", "assign", "unavailable", "unavailable"), edge("e5", "unavailable", "end", "")},
		}},
	}
}
