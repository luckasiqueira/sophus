package routers

import (
	"bytes"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"

	"sophus/logos"

	"github.com/kataras/iris/v12"
)

func TestBrandAssetsArePublicAndBundled(t *testing.T) {
	app := iris.New()
	Router(app)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(logos.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := logos.Files.ReadFile(entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest("GET", "/logos/"+entry.Name(), nil))
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) {
				t.Fatalf("asset unavailable without login: status=%d", response.Code)
			}
			contentType := "image/svg+xml"
			if strings.HasSuffix(entry.Name(), ".png") {
				contentType = "image/png"
			}
			if !strings.HasPrefix(response.Header().Get("Content-Type"), contentType) {
				t.Fatalf("wrong MIME type: %q", response.Header().Get("Content-Type"))
			}
			head := httptest.NewRecorder()
			app.ServeHTTP(head, httptest.NewRequest("HEAD", "/logos/"+entry.Name(), nil))
			if head.Code != 200 || head.Body.Len() != 0 {
				t.Fatalf("HEAD status=%d body=%d bytes", head.Code, head.Body.Len())
			}
		})
	}
	for _, path := range []string{"/logos/assets.go", "/logos/Sophus_prancha_vetorial.svg", "/logos/missing.svg"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 {
			t.Errorf("non-production asset %s: status=%d", path, response.Code)
		}
	}
}
