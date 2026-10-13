package web

import (
	"context"
	"strings"
	"testing"
)

func TestPlatformTOTPEnrollmentAndLogin(t *testing.T) {
	for _, enrollment := range []bool{true, false} {
		var html strings.Builder
		if err := PlatformSecondFactor(enrollment, "test-secret", "data:image/png;base64,test").Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"Google Authenticator", `hx-post="/login/2fa"`, `autocomplete="one-time-code"`, `pattern="[0-9]{6}"`} {
			if !strings.Contains(html.String(), expected) {
				t.Fatalf("missing %s", expected)
			}
		}
		if strings.Contains(html.String(), "test-secret") != enrollment {
			t.Fatal("secret exposed outside enrollment")
		}
	}
	var settings strings.Builder
	if err := SaaSSettings().Render(context.Background(), &settings); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"platform-current-password", "platform-new-password", "Alterar senha (opcional)"} {
		if strings.Contains(settings.String(), removed) {
			t.Fatalf("password control still present: %s", removed)
		}
	}
	if !strings.Contains(settings.String(), "Verificação em duas etapas ativa") {
		t.Fatal("2FA status missing")
	}
}
