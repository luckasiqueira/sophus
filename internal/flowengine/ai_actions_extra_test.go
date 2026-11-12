package flowengine

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func testServiceAccountJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	encoded, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "agenda@test.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	return string(encoded)
}

func TestValidateExtraAIActions(t *testing.T) {
	account := testServiceAccountJSON(t)
	valid := []AIAction{
		{Type: "file", MediaKind: "document", MediaPath: "catalogo.pdf", MediaName: "Catálogo"},
		{Type: "file", MediaKind: "image", MediaURL: "https://example.com/foto.png"},
		{Type: "schedule"},
		{Type: "schedule", MaxDays: 7},
		{Type: "smartTransfer"},
		{Type: "smartTransfer", DepartmentID: 3, Strategy: "random"},
		{Type: "pix", PixKeyType: "email", PixKey: "vendas@example.com", PixName: "Loja", PixCity: "Recife"},
		{Type: "pix", PixKeyType: "cnpj", PixKey: "11.222.333/0001-81", PixName: "Loja", PixCity: "Recife", AmountMode: "fixed", Amount: 99.9, SendQRCode: true},
		{Type: "pix", PixKeyType: "phone", PixKey: "11988887777", PixName: "Loja", PixCity: "Recife", AmountMode: "ai", MaxAmount: 500},
		{Type: "calendar", CalendarID: "agenda@example.com", ServiceAccount: account},
		{Type: "calendar", CalendarID: "agenda@example.com", ServiceAccount: account, Duration: 60, WorkStart: "08:00", WorkEnd: "12:00", WorkDays: []int{1, 3}},
	}
	for _, action := range valid {
		action.Name, action.Description = "Ação", "Quando precisar"
		if err := validateAIAction(action); err != nil {
			t.Fatalf("%s rejected: %v (%+v)", action.Type, err, action)
		}
	}
	invalid := []AIAction{
		{Type: "file", MediaKind: "image"},
		{Type: "file", MediaKind: "sticker", MediaPath: "a.webp"},
		{Type: "file", MediaKind: "document", MediaPath: "../segredo.pdf"},
		{Type: "schedule", MaxDays: 31},
		{Type: "smartTransfer", Strategy: "vip"},
		{Type: "pix", PixKeyType: "cpf", PixKey: "111.111.111-11", PixName: "Loja", PixCity: "Recife"},
		{Type: "pix", PixKeyType: "email", PixKey: "vendas@example.com", PixCity: "Recife"},
		{Type: "pix", PixKeyType: "email", PixKey: "vendas@example.com", PixName: "Loja", PixCity: "Recife", AmountMode: "fixed"},
		{Type: "calendar", CalendarID: "agenda@example.com", ServiceAccount: `{"type":"service_account"}`},
		{Type: "calendar", ServiceAccount: account},
		{Type: "calendar", CalendarID: "a", ServiceAccount: account, WorkStart: "17:50", WorkEnd: "18:00"},
		{Type: "calendar", CalendarID: "a", ServiceAccount: account, WorkStart: "9h"},
	}
	for _, action := range invalid {
		action.Name, action.Description = "Ação", "Quando precisar"
		if err := validateAIAction(action); err == nil {
			t.Fatalf("%s must be rejected: %+v", action.Type, action)
		}
	}
}

func TestExtraAIActionTools(t *testing.T) {
	actions := []AIAction{
		{Type: "file", Name: "Catálogo", MediaKind: "document", MediaName: "catalogo.pdf"},
		{Type: "schedule", Name: "Lembrete"},
		{Type: "pix", Name: "Pix", AmountMode: "ai", MaxAmount: 200},
		{Type: "calendar", Name: "Agenda de reuniões", Duration: 45},
	}
	tools, byName := aiActionTools(actions)
	if len(tools) != 5 {
		t.Fatalf("calendar must become two tools, got %d", len(tools))
	}
	if tools[3].Name != "acao_4_agenda_de_reunioes_horarios" || byName[tools[3].Name].operation != aiCalendarSlotsTool {
		t.Fatalf("slots tool = %q op=%q", tools[3].Name, byName[tools[3].Name].operation)
	}
	if tools[4].Name != "acao_4_agenda_de_reunioes_agendar" || byName[tools[4].Name].operation != aiCalendarBookTool || !strings.Contains(tools[4].Description, "45 minutos") {
		t.Fatalf("book tool = %+v", tools[4])
	}
	schedule, _ := json.Marshal(tools[1].Parameters)
	if !strings.Contains(string(schedule), `"required":["mensagem","dataHora"]`) {
		t.Fatalf("schedule schema = %s", schedule)
	}
	pixSchema, _ := json.Marshal(tools[2].Parameters)
	if !strings.Contains(string(pixSchema), `"required":["valor"]`) || !strings.Contains(string(pixSchema), "R$ 200,00") {
		t.Fatalf("pix schema = %s", pixSchema)
	}
	long, _ := aiActionTools([]AIAction{{Type: "calendar", Name: strings.Repeat("agenda muito longa ", 6)}})
	for _, tool := range long {
		if len(tool.Name) > 64 {
			t.Fatalf("tool name too long: %q", tool.Name)
		}
	}
	if long[0].Name == long[1].Name {
		t.Fatal("calendar tools must have different names")
	}
}

func TestAIScheduleRejectsInvalidDates(t *testing.T) {
	location := time.FixedZone("BRT", -3*3600)
	runner := &aiToolRunner{timezone: location}
	now := time.Now().In(location)
	cases := map[string]map[string]interface{}{
		"no message": {"dataHora": now.Add(time.Hour).Format("2006-01-02 15:04")},
		"bad format": {"mensagem": "oi", "dataHora": "amanhã às 10h"},
		"past":       {"mensagem": "oi", "dataHora": now.Add(-time.Hour).Format("2006-01-02 15:04")},
		"too far":    {"mensagem": "oi", "dataHora": now.AddDate(0, 0, 8).Format("2006-01-02 15:04")},
	}
	for name, arguments := range cases {
		if _, err := runner.schedule(AIAction{Type: "schedule", MaxDays: 7}, arguments); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	parsed, err := parseAIDateTime("2026-11-20T09:30", location)
	if err != nil || parsed.Hour() != 9 || parsed.Location() != location {
		t.Fatalf("parsed = %v, %v", parsed, err)
	}
}

func TestAIPixRejectsInvalidAmounts(t *testing.T) {
	runner := &aiToolRunner{}
	action := AIAction{Type: "pix", PixKeyType: "email", PixKey: "vendas@example.com", PixName: "Loja", PixCity: "Recife", AmountMode: "ai", MaxAmount: 100}
	for _, value := range []string{"", "abc", "-5", "150,00"} {
		if _, err := runner.sendPix(action, map[string]interface{}{"valor": value}); err == nil {
			t.Fatalf("valor %q must be rejected", value)
		}
	}
}

func TestAICalendarRejectsOutsideHours(t *testing.T) {
	location := time.FixedZone("BRT", -3*3600)
	runner := &aiToolRunner{timezone: location}
	action := AIAction{Type: "calendar", Name: "Agenda", CalendarID: "a", ServiceAccount: "{}", WorkStart: "09:00", WorkEnd: "12:00", WorkDays: []int{0, 1, 2, 3, 4, 5, 6}}
	tomorrow := time.Now().In(location).AddDate(0, 0, 1)
	late := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 11, 45, 0, 0, location)
	if _, err := runner.bookCalendar(action, map[string]interface{}{"dataHora": late.Format("2006-01-02 15:04")}); err == nil || !strings.Contains(err.Error(), "fora do atendimento") {
		t.Fatalf("a booking ending after hours must be rejected: %v", err)
	}
	closed := AIAction{Type: "calendar", Name: "Agenda", CalendarID: "a", ServiceAccount: "{}", WorkDays: []int{int((tomorrow.Weekday() + 1) % 7)}}
	if result, err := runner.calendarSlots(closed, map[string]interface{}{"data": tomorrow.Format("2006-01-02")}); err != nil || !strings.Contains(result, "Não há atendimento") {
		t.Fatalf("closed day = %q, %v", result, err)
	}
}

func TestFormatBRLAndClock(t *testing.T) {
	for value, expected := range map[float64]string{0.5: "R$ 0,50", 49.9: "R$ 49,90", 1234567.891: "R$ 1.234.567,89"} {
		if got := formatBRL(value); got != expected {
			t.Fatalf("formatBRL(%v) = %q", value, got)
		}
	}
	location := time.FixedZone("America/Sao_Paulo", -3*3600)
	if prompt := aiClockPrompt(time.Date(2026, 11, 12, 15, 4, 0, 0, location)); !strings.Contains(prompt, "quinta-feira, 12/11/2026 15:04") {
		t.Fatalf("clock prompt = %q", prompt)
	}
	if !aiActionsNeedClock([]AIAction{{Type: "tag"}, {Type: "calendar"}}) || aiActionsNeedClock([]AIAction{{Type: "pix"}}) {
		t.Fatal("only schedule and calendar actions need the clock")
	}
}
