package web

import (
	"context"
	"strings"
	"testing"
)

func TestEntityRemovalUsesStyledConfirmation(t *testing.T) {
	for _, test := range []struct {
		name   string
		render func() string
	}{
		{"agents", func() string {
			var html strings.Builder
			if err := AgentSettings().Render(context.Background(), &html); err != nil {
				t.Fatal(err)
			}
			return html.String()
		}},
		{"contacts", func() string {
			var html strings.Builder
			if err := Contacts().Render(context.Background(), &html); err != nil {
				t.Fatal(err)
			}
			return html.String()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			html := test.render()
			for _, expected := range []string{`id="removal-confirmation"`, "dialog.showModal()", "await window.confirmRemoval", "window.confirmAction", "removal-confirmation-eyebrow"} {
				if !strings.Contains(html, expected) {
					t.Fatalf("missing %q", expected)
				}
			}
			if strings.Contains(html, "if (!confirm('Excluir o agente") || strings.Contains(html, "if (!confirm('Excluir \"") {
				t.Fatal("entity removal still uses a browser confirmation")
			}
			if test.name == "agents" && !strings.Contains(html, "if (agent.isOriginalAdmin)") {
				t.Fatal("founder deletion button is not protected")
			}
		})
	}
}
