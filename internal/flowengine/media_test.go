package flowengine

import (
	"strings"
	"testing"

	"sophus/utils/env"
)

func TestFlowMediaSourceUsesSignedHTTPURL(t *testing.T) {
	originalDirectory := env.Backend["MEDIA_DIRECTORY"]
	originalDomain := env.Backend["APP_DOMAIN"]
	originalSecret := env.Backend["SALT_JWT"]
	env.Backend["MEDIA_DIRECTORY"] = t.TempDir()
	env.Backend["APP_DOMAIN"] = "https://example.com"
	env.Backend["SALT_JWT"] = "test-secret"
	defer func() {
		env.Backend["MEDIA_DIRECTORY"] = originalDirectory
		env.Backend["APP_DOMAIN"] = originalDomain
		env.Backend["SALT_JWT"] = originalSecret
	}()

	source, err := flowMediaSource(map[string]interface{}{
		"imagePath": "image.png",
		"imageUrl":  "/medias/9/image.png",
	}, "imagePath", "imageUrl", 9, ExecutionContext{})
	if err != nil {
		t.Fatalf("resolve media source: %v", err)
	}
	if !strings.HasPrefix(source, "https://example.com/medias/9/flows/image.png?token=") {
		t.Fatalf("expected signed HTTP URL, got %q", source)
	}
}
