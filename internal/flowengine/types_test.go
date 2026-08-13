package flowengine

import (
	"encoding/json"
	"testing"
)

func TestValidateFlowDataRequiresAudio(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"audio","type":"sendAudio","data":{}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err == nil {
		t.Fatal("expected sendAudio without media to be rejected")
	}
}

func TestValidateFlowDataAcceptsUploadedImage(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"image","type":"sendImage","data":{"imagePath":"image.png"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected uploaded image to be accepted: %v", err)
	}
}

func TestValidateFlowDataAcceptsUploadedDocument(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[{"id":"document","type":"sendFile","data":{"filePath":"contract.pdf"}}],"edges":[]}`)
	if err := ValidateFlowData(raw); err != nil {
		t.Fatalf("expected uploaded document to be accepted: %v", err)
	}
}
