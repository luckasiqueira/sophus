package web

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"

	"github.com/google/uuid"
)

func TestConversationPreview(t *testing.T) {
	tests := []struct {
		name         string
		conversation repo.Conversation
		want         string
	}{
		{name: "text", conversation: repo.Conversation{LastContactMessage: "  Olá  "}, want: "Olá"},
		{name: "image", conversation: repo.Conversation{LastContactMediaType: "image"}, want: "Imagem"},
		{name: "view once", conversation: repo.Conversation{LastContactMediaType: "view_once"}, want: "Visualização única"},
		{name: "empty", conversation: repo.Conversation{}, want: "Nenhuma mensagem recebida"},
		{name: "agent text", conversation: repo.Conversation{LastContactMessage: "Olá", LastMessageFromMe: true}, want: "Agente: Olá"},
		{name: "agent attachment", conversation: repo.Conversation{LastContactMediaType: "audio", LastMessageFromMe: true}, want: "Agente: Áudio"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationPreview(test.conversation); got != test.want {
				t.Fatalf("conversationPreview() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConversationTimeLabel(t *testing.T) {
	now := time.Date(2026, time.August, 27, 15, 30, 0, 0, time.UTC)
	tests := []struct {
		value time.Time
		want  string
	}{
		{value: now.Add(-time.Hour), want: "14:30"},
		{value: now.AddDate(0, 0, -1), want: "Ontem"},
		{value: time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC), want: "20/08"},
		{value: time.Date(2025, time.December, 31, 10, 0, 0, 0, time.UTC), want: "31/12/25"},
	}
	for _, test := range tests {
		if got := conversationTimeLabel(test.value, "UTC", now); got != test.want {
			t.Fatalf("conversationTimeLabel(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestConversationTimeLabelUsesCompanyTimezone(t *testing.T) {
	now := time.Date(2026, time.September, 17, 17, 0, 0, 0, time.UTC)
	value := time.Date(2026, time.September, 17, 16, 0, 0, 0, time.UTC)
	if got := conversationTimeLabel(value, "America/Rio_Branco", now); got != "11:00" {
		t.Fatalf("conversationTimeLabel() = %q, want 11:00", got)
	}
}

func TestPendingRunningConversationShowsCompactActions(t *testing.T) {
	conversation := repo.Conversation{
		Status: repo.ConversationStatusRunning,
		URL:    uuid.New(),
	}
	var output bytes.Buffer
	if err := ConversationList([]repo.Conversation{conversation}, "pending", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation list: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`aria-label="Aceitar conversa"`, `aria-label="Encerrar conversa"`, `data-conversation-ignore-url=`, "fa-check", "fa-times"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("pending running conversation does not render %q: %s", expected, html)
		}
	}
	if strings.Contains(html, "hx-confirm") {
		t.Fatal("conversation list still uses the browser confirmation dialog")
	}
}

func TestConversationCardIsCompactAndSelected(t *testing.T) {
	conversationURL := uuid.New()
	conversation := repo.Conversation{
		URL: conversationURL, Contact: repo.Contact{Id: 9, Name: "Cliente"},
		AgentName: "Ana", DepartmentName: "Suporte", DepartmentColor: "#2563EB", LastContactMessage: "Preciso de ajuda",
	}
	var output bytes.Buffer
	if err := ConversationList([]repo.Conversation{conversation}, "active", "individual", repo.ConversationScopeCounts{}, conversationURL.String()).Render(context.Background(), &output); err != nil {
		t.Fatalf("render compact conversation card: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`aria-current="page"`, "h-10 w-10", "Preciso de ajuda", "ring-purple-100", "border-color: #2563EB", "fa-user", ">Ana</span>"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("compact conversation card does not contain %q", expected)
		}
	}
	if count := strings.Count(html, "Suporte"); count != 1 {
		t.Fatalf("department rendered %d times, want once", count)
	}
}

func TestConversationListShowsConversationProtocolNextToContact(t *testing.T) {
	conversation := repo.Conversation{URL: uuid.New(), Protocol: 481, Contact: repo.Contact{Id: 9, Name: "Cliente"}}
	var output bytes.Buffer
	if err := ConversationList([]repo.Conversation{conversation}, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Cliente", ">#481</span>", `aria-label="ID da conversa 481"`, `title="Protocolo da conversa: #481"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("conversation list missing %q", expected)
		}
	}
}

func TestMessagesIncludesSharedContactPicker(t *testing.T) {
	var output bytes.Buffer
	if err := Messages(nil, "active", "individual", repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render messages: %v", err)
	}
	for _, expected := range []string{"new-conversation-sidebar", "contact-global-search", "conversation-picker", "/management/api/contacts", "conversation-sidebar", `hx-push-url="false"`, "window.confirmAction('Encerrar conversa'", "data-conversation-ignore-url"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("messages page does not contain %q", expected)
		}
	}
}

func TestConversationDetailSearchesItsOwnMessages(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{
		URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID,
		Contact: repo.Contact{Id: 11, Name: "Cliente", Number: "5511999999999"},
	}
	agent := repo.Agent{Id: agentID, CompanyId: 3}
	messages := []repo.MessageData{{Text: "Mensagem pesquisável", MediaType: "text"}}
	var output bytes.Buffer
	if err := MessageSingle(conversation, agent, []repo.Conversation{conversation}, messages, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation detail: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`id="conversation-message-search-button"`, `id="conversation-message-search-input"`, `data-message-search="Mensagem pesquisável text"`, "Buscar no histórico desta conversa", "scrollIntoView"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("conversation detail does not contain %q", expected)
		}
	}
	if strings.Contains(html, `title="Buscar contato"`) {
		t.Fatal("conversation detail search still opens contact search")
	}
}

func TestConversationSidebarSeparatesIndividualsAndGroups(t *testing.T) {
	counts := repo.ConversationScopeCounts{Individual: 12, Group: 4}
	var output bytes.Buffer
	if err := ConversationSidebar(nil, "active", "group", counts).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation sidebar: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"12 conversas individuais com mensagens não lidas", "4 conversas em grupo com mensagens não lidas", "bg-[#25D366]"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("general unread conversation counts do not contain %q", expected)
		}
	}
	for _, expected := range []string{"Individuais", "Grupos", `scope=group`, ">12<", ">4<", "bg-white text-purple-700 shadow-sm"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("conversation scope tabs do not contain %q: %s", expected, html)
		}
	}
}

func TestConversationComposerIncludesAttachmentMenuAndGalleryModal(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{
		URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID,
		Contact: repo.Contact{Id: 11, Name: "Cliente", Number: "5511999999999"},
	}
	var output bytes.Buffer
	if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation composer: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"/read", "markVisibleMessagesRead"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("conversation read tracking does not contain %q", expected)
		}
	}
	for _, expected := range []string{"message-attachment-menu", "Enviar do dispositivo", "Escolher da galeria", "message-gallery-modal", "Galeria de mídias", "/media/upload", "/media/send", `name="message"`, `class="flex-1`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("attachment UI does not contain %q", expected)
		}
	}
	for _, expected := range []string{`id="transfer-conversation-agent"`, `id="transfer-conversation-department"`, "window.refreshAdminSelects(modal)", "agentSelect._adminTrigger", "departmentSelect._refreshAdminSelect"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("transfer dropdown is missing system select behavior %q", expected)
		}
	}
	attachment := strings.Index(html, `id="message-attachment-details"`)
	composer := strings.Index(html, `id="message-composer"`)
	if attachment < 0 || composer < 0 {
		t.Fatal("attachment control or text-message form is missing")
	}
	attachmentEnd := strings.Index(html[attachment:], "</details>")
	if attachmentEnd < 0 || composer < attachment+attachmentEnd {
		t.Fatal("attachment control must remain outside the text-message form")
	}
	composerEnd := strings.Index(html[composer:], ">")
	if composerEnd < 0 {
		t.Fatal("text-message form opening tag is incomplete")
	}
	composerTag := html[composer : composer+composerEnd]
	for _, expected := range []string{`hx-post="/api/message/send"`, `hx-trigger="submit"`, `hx-target="#chatMessages"`} {
		if !strings.Contains(composerTag, expected) {
			t.Fatalf("text-message form does not contain %q", expected)
		}
	}
	if strings.Contains(html, `data-send-url="/api/message/send"`) || strings.Contains(html, "fetch(composer.dataset.sendUrl") {
		t.Fatal("text-message submission must use the existing HTMX flow")
	}
}

func TestConversationComposerOffersEmojiAndAudioRecording(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID}
	agent := repo.Agent{Id: agentID, CompanyId: 3}
	var output bytes.Buffer
	if err := MessageSingle(conversation, agent, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation composer: %v", err)
	}
	for _, expected := range []string{`id="message-emoji-button"`, `id="message-emoji-tray"`, `id="message-record-button"`, `id="message-recording-stop"`, `id="message-recording-preview"`, `id="message-recording-send"`, `id="message-microphone-retry"`, `getUserMedia({ audio: true })`, `navigator.permissions.query({ name: 'microphone' })`, `microphoneRetry.addEventListener('click', startRecording)`, `mude Microfone para Permitir`, `galleryModal.dataset.uploadUrl`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("conversation composer missing %q", expected)
		}
	}
	conversation.Status = repo.ConversationStatusClosed
	output.Reset()
	if err := MessageSingle(conversation, agent, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render closed conversation: %v", err)
	}
	if strings.Contains(output.String(), `id="message-record-button"`) || strings.Contains(output.String(), `id="message-emoji-button"`) {
		t.Fatal("closed conversation should not offer composer controls")
	}
}

func TestConversationImagePreviewOpensAboveChat(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{
		URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID,
		Contact: repo.Contact{Id: 11, Name: "Cliente"},
	}
	messages := []repo.MessageData{{MessageId: "photo-1", MediaType: "image", MediaPath: "photo.jpg"}}
	var output bytes.Buffer
	if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, messages, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation image: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`data-conversation-media-preview`, `data-media-kind="image"`, `id="conversation-media-preview"`, `document.body.appendChild(modal)`, `document.getElementById('chatMessages').addEventListener('click'`} {
		if !strings.Contains(html, expected) {
			t.Errorf("image preview missing %q", expected)
		}
	}
}

func TestConversationDocumentPreviewReadsPDFInsideThePlatform(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID, Contact: repo.Contact{Id: 11, Name: "Cliente"}}
	messages := []repo.MessageData{{MessageId: "pdf-1", MediaType: "document", MediaPath: "contrato.pdf", DisplayData: []byte(`{"mimeType":"application/pdf"}`)}}
	var output bytes.Buffer
	if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, messages, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation document: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`data-media-kind="document"`, `data-media-mime="application/pdf"`, "canReadDocument", "application/pdf", "fetch(item.dataset.mediaContent, { credentials: 'same-origin' })", "URL.createObjectURL(blob)", "URL.revokeObjectURL(documentURL)", "document.createElement('iframe')", "Documento para leitura", "Este formato não pode ser lido no navegador"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("document preview does not contain %q", expected)
		}
	}
}

func TestConversationSidebarShowsUnreadConversationAndTabBadges(t *testing.T) {
	conversation := repo.Conversation{URL: uuid.New(), Contact: repo.Contact{Id: 4, Name: "Cliente"}, UnreadCount: 7}
	counts := repo.ConversationScopeCounts{ActiveUnread: 3, PendingUnread: 2}
	var output bytes.Buffer
	if err := ConversationSidebar([]repo.Conversation{conversation}, "active", "individual", counts).Render(context.Background(), &output); err != nil {
		t.Fatalf("render unread conversation sidebar: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"3 conversas ativas com mensagens não lidas", "2 conversas pendentes com mensagens não lidas", "7 mensagens não lidas", ">7</span>"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("unread sidebar does not contain %q", expected)
		}
	}
}

func TestUnreadCountLabelCapsAtNinetyNine(t *testing.T) {
	if got := unreadCountLabel(100); got != "99+" {
		t.Fatalf("unreadCountLabel(100) = %q, want 99+", got)
	}
}

func TestConversationAudioPlayerInitializationIsIdempotent(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID, Contact: repo.Contact{Id: 11, Name: "Cliente"}}
	var output bytes.Buffer
	if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, nil, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render conversation audio runtime: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"player.dataset.audioInitialized === 'true'", "player.dataset.audioInitialized = 'true'", "new MutationObserver", "err.name !== 'AbortError'", "data-volume-control", "data-speed-control", "playbackRates = [1, 1.5, 2]"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("audio runtime does not contain %q", expected)
		}
	}
	if strings.Contains(html, "audio.load()") {
		t.Fatal("audio runtime still forces load before playback")
	}
}

func TestConversationGroupsMessagesWithDateDividers(t *testing.T) {
	agentID := 7
	conversation := repo.Conversation{URL: uuid.New(), Status: repo.ConversationStatusOpen, AgentID: &agentID, Contact: repo.Contact{Id: 11, Name: "Cliente"}}
	messages := []repo.MessageData{
		{MessageId: "first", Text: "Primeira", UpdatedAt: time.Date(2026, time.September, 16, 10, 0, 0, 0, time.UTC), Timezone: "UTC"},
		{MessageId: "second", Text: "Segunda", UpdatedAt: time.Date(2026, time.September, 16, 11, 0, 0, 0, time.UTC), Timezone: "UTC"},
		{MessageId: "third", Text: "Terceira", UpdatedAt: time.Date(2026, time.September, 17, 9, 0, 0, 0, time.UTC), Timezone: "UTC"},
	}
	var output bytes.Buffer
	if err := MessageSingle(conversation, repo.Agent{Id: agentID, CompanyId: 3}, nil, messages, nil, repo.ConversationScopeCounts{}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render dated conversation: %v", err)
	}
	html := output.String()
	if count := strings.Count(html, "data-message-date-divider="); count != 2 {
		t.Fatalf("rendered %d date dividers, want 2", count)
	}
	for _, expected := range []string{`data-message-date-divider="2026-09-16"`, `data-message-date-divider="2026-09-17"`, "initMessageDateDividers", "createMessageDateDivider"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("dated conversation does not contain %q", expected)
		}
	}
}
