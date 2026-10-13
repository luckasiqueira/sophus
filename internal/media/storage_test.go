package media

import (
	"bytes"
	"errors"
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

func TestSaveDocument(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	originalSecret := env.Backend["SALT_JWT"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() {
		env.Backend["MEDIA_DIRECTORY"] = original
		env.Backend["SALT_JWT"] = originalSecret
	}()

	stored, err := Save(5, "document", "contrato.pdf", "application/pdf", strings.NewReader("%PDF-1.7"))
	if err != nil {
		t.Fatalf("save document: %v", err)
	}
	if filepath.Ext(stored.Path) != ".pdf" || stored.Name != "contrato.pdf" {
		t.Fatalf("unexpected stored document: %#v", stored)
	}
}

func TestSaveScopedStoresConversationMediaOutsideFlowDirectory(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	defer func() { env.Backend["MEDIA_DIRECTORY"] = original }()

	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	stored, err := SaveScoped(9, ScopeMessages, "image", "anexo.png", "image/png", bytes.NewReader(png))
	if err != nil {
		t.Fatalf("save scoped media: %v", err)
	}
	if !strings.HasPrefix(stored.URL, "/management/api/media/messages/") {
		t.Fatalf("unexpected scoped URL: %s", stored.URL)
	}
	if _, err := os.Stat(filepath.Join(env.Backend["MEDIA_DIRECTORY"], "9", stored.Path)); err != nil {
		t.Fatalf("conversation media not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Backend["MEDIA_DIRECTORY"], "9", "flows", stored.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("conversation media was stored in flow directory")
	}
}

func TestRecordedWebMAudioKeepsAudioKindAfterStorage(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	defer func() { env.Backend["MEDIA_DIRECTORY"] = original }()
	webm := []byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81, 0x01}
	stored, err := SaveScoped(9, ScopeMessages, "audio", "gravacao.webm", "audio/webm;codecs=opus", bytes.NewReader(webm))
	if err != nil {
		t.Fatal(err)
	}
	item, err := Inspect(9, ScopeMessages, stored.Path)
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "audio" || item.MimeType != "audio/webm" {
		t.Fatalf("recording reclassified: %+v", item)
	}
	items, err := List(9)
	if err != nil || len(items) != 1 || items[0].Kind != "audio" {
		t.Fatalf("recording reclassified in gallery: %+v, %v", items, err)
	}
	if got := catalogMimeType("video.webm"); got != "video/webm" {
		t.Fatalf("video MIME changed: %s", got)
	}
}
