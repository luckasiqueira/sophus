package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"sophus/utils/env"
)

func TestCatalogListsScansAndDeletesCompanyMedia(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	defer func() { env.Backend["MEDIA_DIRECTORY"] = original }()

	companyDir := filepath.Join(env.Backend["MEDIA_DIRECTORY"], "7")
	flowDir := filepath.Join(companyDir, "flows")
	if err := os.MkdirAll(flowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(companyDir, "photo.jpg"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flowDir, "clip.mp4"), []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := List(7)
	if err != nil || len(items) != 2 {
		t.Fatalf("List() = %d items, %v", len(items), err)
	}
	used, count, err := ScanCompany(7)
	if err != nil || used != 10 || count != 2 {
		t.Fatalf("ScanCompany() = (%d, %d, %v)", used, count, err)
	}
	size, err := Delete(7, ScopeMessages, "photo.jpg")
	if err != nil || size != 4 {
		t.Fatalf("Delete() = (%d, %v)", size, err)
	}
}

func TestScopedMediaSignatureBindsScopeAndExpiry(t *testing.T) {
	originalDirectory := env.Backend["MEDIA_DIRECTORY"]
	originalSecret := env.Backend["SALT_JWT"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() { env.Backend["MEDIA_DIRECTORY"] = originalDirectory; env.Backend["SALT_JWT"] = originalSecret }()
	if err := os.MkdirAll(filepath.Join(env.Backend["MEDIA_DIRECTORY"], "3", "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute).Unix()
	token, err := SignScoped(3, ScopeFlows, "asset.jpg", expires)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyScoped(3, ScopeFlows, "asset.jpg", expires, token) {
		t.Fatal("valid scoped signature rejected")
	}
	if VerifyScoped(3, ScopeMessages, "asset.jpg", expires, token) || VerifyScoped(4, ScopeFlows, "asset.jpg", expires, token) {
		t.Fatal("scoped signature accepted for a different asset")
	}
	if VerifyScoped(3, ScopeFlows, "asset.jpg", time.Now().Add(-time.Minute).Unix(), token) {
		t.Fatal("expired signature accepted")
	}
}

func TestScopedPathRejectsTraversalAndUnknownScope(t *testing.T) {
	original := env.Backend["MEDIA_DIRECTORY"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	defer func() { env.Backend["MEDIA_DIRECTORY"] = original }()
	for _, test := range []struct{ scope, name string }{{ScopeMessages, "../secret"}, {ScopeFlows, `..\secret`}, {"other", "file.jpg"}, {ScopeMessages, "C:secret"}} {
		if _, err := ScopedPath(1, test.scope, test.name); err == nil {
			t.Fatalf("ScopedPath(%q, %q) accepted", test.scope, test.name)
		}
	}
}
