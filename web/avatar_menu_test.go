package web

import (
	"context"
	"strings"
	"testing"
)

func TestAccountActionsBelongToAvatar(t *testing.T) {
	var html strings.Builder
	if err := appSidebar().Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	content := html.String()
	avatar := strings.Index(content, `id="app-user-menu"`)
	profile := strings.Index(content, `href="/profile"`)
	logout := strings.Index(content, `action="/logout"`)
	if avatar < 0 || profile < avatar || logout < avatar {
		t.Fatal("profile/logout must be contained in avatar menu, outside navigation links")
	}
}
