package controllers

import (
	"testing"

	"sophus/internal/repo"
)

func TestOperatorMediaAllowedAppliesTypeAndSizePolicy(t *testing.T) {
	settings := repo.CompanyOperationSettings{AllowedAttachmentTypes: []string{"image", "document"}, MaxAttachmentMB: 5}
	if !operatorMediaAllowed("image", 2<<20, settings) {
		t.Fatal("expected image within policy to be allowed")
	}
	if operatorMediaAllowed("video", 2<<20, settings) {
		t.Fatal("expected disabled type to be rejected")
	}
	if operatorMediaAllowed("document", 6<<20, settings) {
		t.Fatal("expected oversized attachment to be rejected")
	}
	if operatorMediaAllowed("unknown", 1, settings) {
		t.Fatal("expected unknown type to be rejected")
	}
}
