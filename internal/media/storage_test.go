package media

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sophus/utils/env"
)

func TestSaveAndLoadCompanyMedia(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	originalDomain := env.Backend["APP_DOMAIN"]
	originalSecret := env.Backend["SALT_JWT"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	env.Backend["APP_DOMAIN"] = "https://example.com"
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() {
		env.Backend["MEDIA_DIRECTORY"] = original
		env.Backend["APP_DOMAIN"] = originalDomain
		env.Backend["SALT_JWT"] = originalSecret
	}()

	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	stored, err := Save(7, "image", "photo.png", "image/png", bytes.NewReader(png))
	if err != nil {
		t.Fatalf("save media: %v", err)
	}
	if !strings.HasPrefix(stored.URL, "/medias/7/flows/") {
		t.Fatalf("unexpected media URL: %s", stored.URL)
	}
	signedURL, err := SignedURL(7, stored.Path)
	if err != nil {
		t.Fatalf("sign media: %v", err)
	}
	if !strings.HasPrefix(signedURL, "https://example.com/medias/7/flows/") {
		t.Fatalf("unexpected signed URL: %s", signedURL)
	}
	if _, err := os.Stat(filepath.Join(env.Backend["MEDIA_DIRECTORY"], "7", "flows", stored.Path)); err != nil {
		t.Fatalf("stored file not found: %v", err)
	}
}

func TestSignedURLRejectsTraversal(t *testing.T) {
	if _, err := SignedURL(1, "../secret"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestVerifyRejectsChangedCompany(t *testing.T) {
	original := env.Backend["SALT_JWT"]
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() { env.Backend["SALT_JWT"] = original }()

	token, err := Sign(1, "image.png")
	if err != nil {
		t.Fatalf("sign media: %v", err)
	}
	if !Verify(1, "image.png", token) {
		t.Fatal("expected valid signature")
	}
	if Verify(2, "image.png", token) {
		t.Fatal("signature must not be valid for another company")
	}
}
