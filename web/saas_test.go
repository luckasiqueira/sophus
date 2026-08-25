package web

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestSaaSPagesRender(t *testing.T) {
	tests := []struct {
		name    string
		content string
		page    templ.Component
	}{
		{name: "setup", content: "Token de configuração", page: SaaSSetup()},
		{name: "dashboard", content: "Carteira de empresas", page: SaaSDashboard()},
		{name: "platform settings", content: "Cobrança global", page: SaaSSettings()},
		{name: "company settings", content: "Configurações da empresa", page: CompanySettings()},
		{name: "company overview", content: "Mensagens por período", page: CompanyOverview()},
		{name: "agents", content: "Agentes da empresa", page: AgentSettings()},
		{name: "billing", content: "Planos disponíveis", page: Billing()},
		{name: "flow builder curl import", content: "Importar de cURL", page: FlowBuilder()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			if err := test.page.Render(context.Background(), &output); err != nil {
				t.Fatalf("render page: %v", err)
			}
			if !strings.Contains(output.String(), test.content) {
				t.Fatalf("rendered page does not contain %q", test.content)
			}
		})
	}
}

func TestBillingUsesStandaloneLandingLayout(t *testing.T) {
	var output strings.Builder
	if err := Billing().Render(context.Background(), &output); err != nil {
		t.Fatalf("render billing page: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"Planos e contratação | Sophus", "Workspace de atendimento", "billing-mobile-menu"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("standalone billing page does not contain %q", expected)
		}
	}
	if strings.Contains(html, `href="/messages"`) {
		t.Fatal("billing page unexpectedly uses the application navigation")
	}
}
