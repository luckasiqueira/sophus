package controllers

import (
	"testing"
	"time"

	"sophus/internal/repo"
)

func TestQRCodeLifetime(t *testing.T) {
	if got := qrCodeLifetime(1); got != 60*time.Second {
		t.Fatalf("first QR lifetime = %s, want 60s", got)
	}
	if got := qrCodeLifetime(2); got != 20*time.Second {
		t.Fatalf("renewed QR lifetime = %s, want 20s", got)
	}
}

func TestDirectAIAgentPreventsAutoAssignment(t *testing.T) {
	if shouldAutoAssignConversation(true, repo.AIAgent{Id: 7}) {
		t.Fatal("direct AI agent conversation must not be assigned to a human first")
	}
	if !shouldAutoAssignConversation(true, repo.AIAgent{}) {
		t.Fatal("unbound conversation should keep automatic assignment behavior")
	}
	if shouldAutoAssignConversation(false, repo.AIAgent{}) {
		t.Fatal("disabled automatic assignment must remain disabled")
	}
}
