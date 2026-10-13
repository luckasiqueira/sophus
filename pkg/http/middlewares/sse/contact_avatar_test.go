package sse

import (
	"strings"
	"testing"
)

func TestContactAvatarEventIsScopedToCompany(t *testing.T) {
	one := Conversations.Register("12345")
	two := Conversations.Register("12346")
	defer Conversations.Unregister(one)
	defer Conversations.Unregister(two)
	NotifyContactAvatar(12345, 77)
	select {
	case event := <-one.Ch:
		if event.Name != "contact-avatar" || !strings.Contains(event.Data, `"contactId":77`) {
			t.Fatalf("event = %+v", event)
		}
	default:
		t.Fatal("avatar event missing")
	}
	select {
	case event := <-two.Ch:
		t.Fatalf("event leaked to other company: %+v", event)
	default:
	}
}
