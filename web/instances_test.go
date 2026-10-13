package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"sophus/internal/repo"
)

func TestDisconnectedInstanceShowsReconnectAction(t *testing.T) {
	connection := repo.ConnectionEVO{Id: 7, Name: "Suporte", Status: repo.ConnectionStatusDisconnected}
	var output bytes.Buffer
	if err := InstanceList([]repo.ConnectionEVO{connection}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render instance list: %v", err)
	}
	html := output.String()
	if !strings.Contains(html, "Reconectar") || !strings.Contains(html, "border-red-300") {
		t.Fatalf("offline card does not show reconnect styling: %s", html)
	}
}

func TestInstanceActionsUseStyledModals(t *testing.T) {
	connection := repo.ConnectionEVO{Id: 7, Name: "Suporte", Status: repo.ConnectionStatusConnected}
	var output bytes.Buffer
	if err := Instances([]repo.ConnectionEVO{connection}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render instances page: %v", err)
	}
	html := output.String()
	for _, expected := range []string{`id="instance-rename-modal"`, `id="instance-delete-modal"`, `id="instance-rename-form"`, "Remover conexão?", "data-close-instance-modal"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("instances page does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{"window.prompt(", "window.confirm("} {
		if strings.Contains(html, unexpected) {
			t.Fatalf("instances page still contains browser dialog %q", unexpected)
		}
	}
}

func TestBaseIncludesLiveNotificationControls(t *testing.T) {
	var output bytes.Buffer
	if err := base().Render(context.Background(), &output); err != nil {
		t.Fatalf("render base: %v", err)
	}
	html := output.String()
	for _, expected := range []string{"app-notification-stack", "app-notification-panel", "app-notification-center-button", "notificationColor", "addPanelItem", "incoming-message", "/sse/conversations", "replaceChildren(link)", "unseenConversations", "item.dataset.conversation === data.conversation", "/preferences#notifications", "playNotificationSound", "data.isGroup && !notificationSettings.groups"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("base notification UI does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{`id="app-notification-settings"`, "data-notification-setting=", `id="app-notification-sound-test"`} {
		if strings.Contains(html, unexpected) {
			t.Fatalf("notification center still contains setting control %q", unexpected)
		}
	}
}
