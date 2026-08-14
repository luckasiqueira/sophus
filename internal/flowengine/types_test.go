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

func TestValidateFlowDataRejectsUnsafeHTTPConfiguration(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "arbitrary method", data: `{"method":"CONNECT","url":"https://example.com"}`},
		{name: "unsafe header", data: `{"method":"GET","url":"https://example.com","headerMode":"fields","headerFields":[{"key":"X-Forwarded-For","value":"127.0.0.1"}]}`},
		{name: "unbounded timeout", data: `{"method":"GET","url":"https://example.com","timeout":60001}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"nodes":[{"id":"http","type":"httpRequest","data":` + test.data + `}],"edges":[]}`)
			if err := ValidateFlowData(raw); err == nil {
				t.Fatal("expected unsafe HTTP configuration to be rejected")
			}
		})
	}
}
