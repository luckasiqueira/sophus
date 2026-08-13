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
