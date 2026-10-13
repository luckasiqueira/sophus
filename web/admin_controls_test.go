package web

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestOperationalPagesUseEnhancedFormControls(t *testing.T) {
	tests := []struct {
		name       string
		component  templ.Component
		expected   []string
		unexpected string
	}{
		{name: "AI agents", component: AIAgentSettings(), expected: []string{"enhanceAdminSelect", `id="ai-active" type="checkbox" checked class="peer sr-only"`, `id="ai-safe-mode" type="checkbox" checked class="peer sr-only"`}},
		{name: "agents", component: AgentSettings(), expected: []string{"enhanceAdminSelect", `id="agent-role"`, `h-[46px]`, "refreshAdminSelects(form)"}, unexpected: "appearance-none"},
		{name: "conversation picker", component: ConversationPicker(), expected: []string{"enhanceAdminSelect", `id="conversation-picker-connection"`}, unexpected: `id="conversation-picker-connection" data-native`},
		{name: "contacts", component: Contacts(), expected: []string{`label for="contacts-connection"`, "refreshAdminSelects(document.getElementById('contacts-panel'))", "refreshAdminSelects(document.getElementById('contact-import-modal'))"}, unexpected: `id="contacts-connection" data-native`},
		{name: "departments", component: DepartmentSettings(), expected: []string{"peer sr-only", "peer-checked:bg-purple-600"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			if err := test.component.Render(context.Background(), &output); err != nil {
				t.Fatalf("render component: %v", err)
			}
			html := output.String()
			for _, expected := range test.expected {
				if !strings.Contains(html, expected) {
					t.Fatalf("rendered component does not contain %q", expected)
				}
			}
			if test.unexpected != "" && strings.Contains(html, test.unexpected) {
				t.Fatalf("rendered component unexpectedly contains %q", test.unexpected)
			}
		})
	}
}
