package media

import (
	"os"
	"path/filepath"
	"testing"

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
