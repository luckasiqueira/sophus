package flowengine

import "testing"

func TestEvaluateStructuredConditionSources(t *testing.T) {
	ctx := ExecutionContext{
		"contactName":      "Ana Souza",
		"contactNumber":    "5511999999999",
		"conversationTags": []string{"VIP", "Renovação"},
		"contactEmail":     "ana@example.com",
		"message":          "Quero renovar meu plano",
		"department":       "Comercial",
		"connectionType":   "whatsapp_qrcode",
		"customer":         map[string]interface{}{"score": float64(12)},
	}
	tests := []struct {
		name     string
		source   string
		operator string
		value    string
		variable string
	}{
		{name: "contact name", source: "contactName", operator: "equals", value: "ana souza"},
		{name: "contact number", source: "contactNumber", operator: "notEquals", value: "5511888888888"},
		{name: "conversation tag", source: "conversationTag", operator: "contains", value: "vip"},
		{name: "contact email", source: "contactEmail", operator: "notContains", value: "invalid.test"},
		{name: "contact message", source: "contactMessage", operator: "contains", value: "RENOVAR"},
		{name: "department", source: "department", operator: "equals", value: "comercial"},
		{name: "variable", source: "variable", operator: "greaterThan", value: "10", variable: "customer.score"},
		{name: "connection type", source: "connectionType", operator: "equals", value: "whatsapp_qrcode"},
		{name: "time", source: "time", operator: "true"},
	}
	engine := &Engine{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := map[string]interface{}{
				"conditionSource":   test.source,
				"conditionOperator": test.operator,
				"conditionValue":    test.value,
				"conditionVariable": test.variable,
			}
			if !engine.EvaluateNodeCondition(data, ctx) {
				t.Fatalf("condition evaluated false: %#v", data)
			}
		})
	}
}

func TestStructuredConditionOperators(t *testing.T) {
	tests := []struct {
		name     string
		actual   interface{}
		operator string
		expected string
	}{
		{name: "equals", actual: "Ativo", operator: "equals", expected: "ativo"},
		{name: "not equals", actual: "Ativo", operator: "notEquals", expected: "inativo"},
		{name: "contains", actual: "Atendimento VIP", operator: "contains", expected: "vip"},
		{name: "not contains", actual: "Atendimento", operator: "notContains", expected: "financeiro"},
		{name: "greater number", actual: float64(11), operator: "greaterThan", expected: "10"},
		{name: "less time", actual: "08:30", operator: "lessThan", expected: "09:00"},
		{name: "true", actual: "sim", operator: "true"},
		{name: "false", actual: false, operator: "false"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !compareStructuredCondition(test.actual, test.operator, test.expected) {
				t.Fatalf("compareStructuredCondition(%#v, %q, %q) = false", test.actual, test.operator, test.expected)
			}
		})
	}
}

func TestEvaluateNodeConditionKeepsLegacyExpressions(t *testing.T) {
	result := (&Engine{}).EvaluateNodeCondition(map[string]interface{}{
		"condition": `{{message}}.includes("sim")`,
	}, ExecutionContext{"message": "Sim, por favor"})
	if !result {
		t.Fatal("legacy condition evaluated false")
	}
}
