package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestPagesLoadThemeRuntimeBeforeStyles(t *testing.T) {
	pages := map[string]templ.Component{"app": AgentSettings(), "login": Login(), "settings": CompanySettings(), "company": CompanyOverview()}
	for name, page := range pages {
		var html bytes.Buffer
		if err := page.Render(context.Background(), &html); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		output := html.String()
		runtime := strings.Index(output, "sophus-theme")
		tailwind := strings.Index(output, "cdn.tailwindcss.com")
		if runtime < 0 || !strings.Contains(output, `id="app-theme-dark"`) {
			t.Fatalf("%s: dark theme runtime missing", name)
		}
		if tailwind >= 0 && runtime > tailwind {
			t.Fatalf("%s: theme must be applied before the stylesheet loads to avoid a light flash", name)
		}
	}
}

func TestAvatarMenuOffersThemeToggle(t *testing.T) {
	var html bytes.Buffer
	if err := AvatarMenu().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`role="switch"`, "data-theme-toggle", "Modo escuro"} {
		if !strings.Contains(html.String(), expected) {
			t.Fatalf("avatar menu missing %q", expected)
		}
	}
}

func TestDarkThemeCoversCoreSurfaces(t *testing.T) {
	var html bytes.Buffer
	if err := themeDarkStyles().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"html.theme-dark .bg-white", "html.theme-dark .bg-stone-50", "html.theme-dark .text-gray-900", "html.theme-dark .border-stone-200", `html.theme-dark [data-date-control] input[type="date"]`} {
		if !strings.Contains(html.String(), selector) {
			t.Fatalf("dark theme missing %q", selector)
		}
	}
}
