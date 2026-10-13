package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"sophus/internal/repo"
)

func TestMessageDeliveryChecks(t *testing.T) {
	for _, tc := range []struct {
		status      int
		label, icon string
	}{
		{1, "Enviada", "fa-check"}, {2, "Entregue", "fa-check-double"}, {3, "Lida", "fa-check-double"},
	} {
		var html bytes.Buffer
		if err := MessageDeliveryStatus(repo.MessageData{MessageId: "ABC", DeliveryStatus: tc.status}, true).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{`id="message-status-ABC"`, `hx-swap-oob="outerHTML"`, `aria-label="` + tc.label + `"`, tc.icon} {
			if !strings.Contains(html.String(), expected) {
				t.Fatalf("missing %s in %s", expected, html.String())
			}
		}
		if strings.Contains(html.String(), "text-sky-300") != (tc.status == 3) {
			t.Fatalf("incorrect read color: %s", html.String())
		}
	}
}
