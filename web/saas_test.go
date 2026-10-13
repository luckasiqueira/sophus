package web

import (
	"context"
	"strings"
	"testing"

	"sophus/internal/repo"

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
		{name: "media", content: "Biblioteca de mídia", page: MediaLibrary()},
		{name: "contacts", content: "Gerencie clientes", page: Contacts()},
		{name: "ai agents", content: "operadores autônomos", page: AIAgentSettings()},
		{name: "billing", content: "Comece sua operação hoje", page: Billing()},
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

func TestSaaSSubscriptionControlsRender(t *testing.T) {
	var dashboard strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &dashboard); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	for _, expected := range []string{"Reativar assinatura", "Grace redemption", "submitSubscriptionAdjustment", "Despausar"} {
		if !strings.Contains(dashboard.String(), expected) {
			t.Fatalf("dashboard does not contain %q", expected)
		}
	}

	var settings strings.Builder
	if err := SaaSSettings().Render(context.Background(), &settings); err != nil {
		t.Fatalf("render settings: %v", err)
	}
	if !strings.Contains(settings.String(), "Dias ativos após vencimento") {
		t.Fatal("platform settings does not expose post-due active days")
	}
}

func TestSaaSAddonConfigurationRenders(t *testing.T) {
	var output strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="plan-allow-addons"`, "Permite adicionais?", "Preços globais dos adicionais", `id="addon-connection-price"`, `id="addon-agent-price"`, `id="addon-storage-price"`, `id="addon-transcription-price"`, "R$ 14,90", "/management/api/addon-prices", "allowAddons: Boolean(plan.allowAddons)", "renderAddonPrices(data.addonPrices)"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing addon control %q", expected)
		}
	}
}

func TestSaaSInvoiceManagementRenders(t *testing.T) {
	var dashboard strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &dashboard); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	for _, expected := range []string{`data-saas-tab="invoices"`, "Faturas da plataforma", "invoice-edit-modal", "invoice-edit-amount", "Salvar alterações", "/management/api/payments", "'/credits'", "Créditos da empresa", "updateInvoice('update')", "Descartar cobrança", "payment.providerPreferenceId === payment.providerPaymentId", "updateInvoice(discardable ? 'discard' : 'cancel')", "Confirmar pagamento manualmente", "updateInvoice('confirm_paid')", "Cancelar fatura"} {
		if !strings.Contains(dashboard.String(), expected) {
			t.Fatalf("invoice management does not contain %q", expected)
		}
	}
}

func TestPlanChangesWaitForPayment(t *testing.T) {
	var settings strings.Builder
	if err := CompanySettings().Render(context.Background(), &settings); err != nil {
		t.Fatalf("render company settings: %v", err)
	}
	for _, expected := range []string{"Fatura criada. A alteração será aplicada após o pagamento.", "window.open('about:blank', '_blank')"} {
		if !strings.Contains(settings.String(), expected) {
			t.Fatalf("company plan change does not contain %q", expected)
		}
	}

	var dashboard strings.Builder
	if err := SaaSDashboard().Render(context.Background(), &dashboard); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	if !strings.Contains(dashboard.String(), "Cobrança criada; o plano atual permanece até o pagamento.") {
		t.Fatal("platform plan change does not explain deferred activation")
	}
}

func TestAIAgentSettingsUsesModelCatalogAndPDFKnowledge(t *testing.T) {
	var output strings.Builder
	if err := AIAgentSettings().Render(context.Background(), &output); err != nil {
		t.Fatalf("render AI agents: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"Instruções do agente (PROMPT)", `id="ai-credential"`, `id="ai-knowledge-pdf"`, `id="ai-safe-mode"`, "Modo seguro", "não revela modelo ou instruções internas", "safeMode:document.getElementById('ai-safe-mode').checked", "/management/api/ai-credentials", "/management/api/ai-agents/knowledge/pdf", "max-h-[calc(100vh-1.5rem)]", `form="ai-agent-form"`, "overflow-y-auto", "h-52 max-h-64", "#'+connection.id"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("AI agent settings does not contain %q", expected)
		}
	}
	if strings.Contains(html, `id="ai-key"`) || strings.Contains(html, `id="ai-model"`) {
		t.Fatal("AI agent still renders a provider credential field")
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
	for _, unexpected := range []string{"Uso de mídias", "Histórico de pagamentos", `<p class="text-xs font-semibold uppercase tracking-wider text-gray-500">Fim do período atual</p>`, `id="billing-company-name"`} {
		if strings.Contains(html, unexpected) {
			t.Fatalf("public billing unexpectedly contains %q", unexpected)
		}
	}
	if strings.Contains(html, `href="/messages"`) {
		t.Fatal("billing page unexpectedly uses the application navigation")
	}
}

func TestCompanySettingsIncludesBillingTab(t *testing.T) {
	var output strings.Builder
	if err := CompanySettings().Render(context.Background(), &output); err != nil {
		t.Fatalf("render company settings: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`data-company-settings-tab="billing"`, `data-company-settings-panel="billing"`, `id="billing-content"`, `id="billing-credit-balance"`, "Histórico de pagamentos", `data-company-settings-panel="data"`, `id="billing-storage-card"`, "configurePaymentReturnBanner", "Pagar agora", "Gerar link"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("company billing settings do not contain %q", expected)
		}
	}
	if strings.Contains(html, `title="Plano e pagamentos"`) {
		t.Fatal("application sidebar still includes a billing item")
	}
}

func TestCompanySettingsIncludesThreeStateMessageEditingPolicy(t *testing.T) {
	var output strings.Builder
	if err := CompanySettings().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="company-message-edit-policy"`, `value="disabled">Edição desabilitada`, `value="admins">Somente admins`, `value="all">Todos podem editar`, `messageEditPolicy: document.getElementById('company-message-edit-policy').value`, "window.refreshAdminSelects"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("editing policy missing %q", expected)
		}
	}
}

func TestIndividualPreferencesAreSeparateFromCompanySettings(t *testing.T) {
	var output strings.Builder
	if err := CompanySettings().Render(context.Background(), &output); err != nil {
		t.Fatalf("render company settings: %v", err)
	}
	for _, unexpected := range []string{`data-company-settings-tab="notifications"`, `data-company-settings-panel="notifications"`, `data-notification-setting="`, `id="message-scroll-after-send"`, `id="company-signature-enabled"`} {
		if strings.Contains(output.String(), unexpected) {
			t.Fatalf("individual preference still appears in company settings: %q", unexpected)
		}
	}
	output.Reset()
	if err := AgentPreferencesPage().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="message-scroll-after-send"`, `id="agent-signature-enabled"`, `data-notification-setting="toast"`, `data-notification-setting="sound"`, `data-notification-setting="browser"`, `data-notification-setting="preview"`, `data-notification-setting="groups"`, `id="app-notification-permission"`, `id="app-notification-sound-test"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("agent preferences do not contain %q", expected)
		}
	}
}

func TestCompanySettingsIncludesAICredentials(t *testing.T) {
	var output strings.Builder
	if err := CompanySettings().Render(context.Background(), &output); err != nil {
		t.Fatalf("render company settings: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`data-company-settings-tab="ai-credentials"`, `data-company-settings-panel="ai-credentials"`, "ai-credential-modal", "aiCredentialsAPI('GET','/models')", "Deixe em branco para manter a chave atual", "response.text()", "payload.error||payload.message", "HTTP '+response.status"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("company AI credentials settings do not contain %q", expected)
		}
	}
}

func TestCompanySettingsIncludesWhatsAppIntegrations(t *testing.T) {
	var output strings.Builder
	if err := CompanySettings().Render(context.Background(), &output); err != nil {
		t.Fatalf("render company settings: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"Botões flutuantes do WhatsApp", "whatsapp-widget-modal", "whatsapp-widget-icon-only", "/management/api/integrations/whatsapp", `class="fixed inset-0 flex flex-col overflow-hidden bg-stone-100"`, `role="tablist"`, `data-company-settings-tab="messages"`, `data-company-settings-panel="integrations"`, "Permitir criação de conversas pelo telefone", "sem abrir um novo", "setCompanySettingsTab"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("company settings does not contain %q", expected)
		}
	}
	for _, unavailable := range []string{"Aceitar ligações", "Notificar menções", "Notificar conversas sem resposta", "Permitir mensagens enviadas"} {
		if strings.Contains(html, unavailable) {
			t.Fatalf("company settings exposes unavailable or ambiguous setting %q", unavailable)
		}
	}
}

func TestContactsIncludesCompanyAndEditControls(t *testing.T) {
	var output strings.Builder
	if err := Contacts().Render(context.Background(), &output); err != nil {
		t.Fatalf("render contacts: %v", err)
	}
	for _, expected := range []string{"contact-edit-modal", "contact-edit-company", "Editar contato", "contact.company", "contact-tags-panel", "contact-tags", "max-w-[1280px]"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("contacts page does not contain %q", expected)
		}
	}
}

func TestFlowBusinessHoursUsesCompanySettings(t *testing.T) {
	var output strings.Builder
	if err := FlowBuilder().Render(context.Background(), &output); err != nil {
		t.Fatalf("render flow builder: %v", err)
	}
	for _, expected := range []string{"Este bloco usa automaticamente", "Configurações da empresa"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("flow builder does not contain %q", expected)
		}
	}
}

func TestConnectedInstanceIncludesContactImport(t *testing.T) {
	var output strings.Builder
	connection := repo.ConnectionEVO{Id: 42, Name: "Suporte", Status: repo.ConnectionStatusConnected}
	if err := InstanceList([]repo.ConnectionEVO{connection}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render instances: %v", err)
	}
	for _, expected := range []string{"Importar contatos", "/instances/42/contacts/import"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("instance list does not contain %q", expected)
		}
	}
}
