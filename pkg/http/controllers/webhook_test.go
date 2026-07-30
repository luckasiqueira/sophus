package controllers

import (
	"testing"
	"time"
)

func TestQRCodeLifetime(t *testing.T) {
	if got := qrCodeLifetime(1); got != 60*time.Second {
		t.Fatalf("first QR lifetime = %s, want 60s", got)
	}
	if got := qrCodeLifetime(2); got != 20*time.Second {
		t.Fatalf("renewed QR lifetime = %s, want 20s", got)
	}
}
