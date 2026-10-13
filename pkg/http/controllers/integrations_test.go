package controllers

import (
	"strings"
	"testing"

	"sophus/internal/repo"
)

func TestNormalizeWhatsAppWidget(t *testing.T) {
	input := repo.WhatsAppWidgetInput{
		Name: " Site ", PhoneNumber: "+55 (11) 99999-9999", CallToAction: " Fale conosco ",
		Color: "#25d366", IconOnly: true, IsActive: true,
	}
	if message := normalizeWhatsAppWidget(&input); message != "" {
		t.Fatalf("valid widget rejected: %s", message)
	}
	if input.PhoneNumber != "5511999999999" || input.Color != "#25D366" || input.Name != "Site" {
		t.Fatalf("widget not normalized: %#v", input)
	}
	if !input.IconOnly {
		t.Fatal("normalization reset compact widget option")
	}
}

func TestWhatsAppWidgetScriptSupportsCompactVariant(t *testing.T) {
	script := whatsAppWidgetScript(repo.WhatsAppWidget{PhoneNumber: "5511999999999", CallToAction: "Contato", Color: "#25D366", IconOnly: true})
	for _, expected := range []string{`"iconOnly":true`, "width:56px;height:56px", "if(!c.iconOnly)", `aria-label",c.text`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("widget script does not contain %q", expected)
		}
	}
}

func TestNormalizeWhatsAppWidgetRejectsInvalidValues(t *testing.T) {
	valid := repo.WhatsAppWidgetInput{Name: "Site", PhoneNumber: "5511999999999", CallToAction: "Contato", Color: "#25D366"}
	tests := []repo.WhatsAppWidgetInput{
		{Name: "", PhoneNumber: valid.PhoneNumber, CallToAction: valid.CallToAction, Color: valid.Color},
		{Name: valid.Name, PhoneNumber: "123", CallToAction: valid.CallToAction, Color: valid.Color},
		{Name: valid.Name, PhoneNumber: valid.PhoneNumber, CallToAction: "", Color: valid.Color},
		{Name: valid.Name, PhoneNumber: valid.PhoneNumber, CallToAction: valid.CallToAction, Color: "green"},
	}
	for index := range tests {
		if message := normalizeWhatsAppWidget(&tests[index]); message == "" {
			t.Fatalf("invalid widget %d accepted", index)
		}
	}
}
