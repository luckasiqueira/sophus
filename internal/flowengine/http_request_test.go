package flowengine

import (
	"net/url"
	"strings"
	"testing"
)

func TestHTTPRequestJSONFields(t *testing.T) {
	payload, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "json",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "name", "value": "{{name}}"},
			map[string]interface{}{"key": "active", "value": "true"},
			map[string]interface{}{"key": "count", "value": "3"},
		},
	}, ExecutionContext{"name": "Ana"}, "POST")
	if err != nil {
		t.Fatalf("build JSON payload: %v", err)
	}
	got := payload.(map[string]interface{})
	if got["name"] != "Ana" || got["active"] != true || got["count"] != float64(3) {
		t.Fatalf("unexpected JSON payload: %#v", got)
	}
	if body != nil || contentType != "application/json" {
		t.Fatalf("unexpected body/content type: %q %q", body, contentType)
	}
}

func TestHTTPRequestFormFields(t *testing.T) {
	_, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "form",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "name", "value": "Ana Maria"},
			map[string]interface{}{"key": "tag", "value": "vendas"},
		},
	}, ExecutionContext{}, "POST")
	if err != nil {
		t.Fatalf("build form payload: %v", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse form payload: %v", err)
	}
	if values.Get("name") != "Ana Maria" || values.Get("tag") != "vendas" {
		t.Fatalf("unexpected form payload: %q", body)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestHTTPRequestLegacyBodyUsesRawJSON(t *testing.T) {
	payload, _, contentType, err := httpRequestPayload(map[string]interface{}{
		"body": `{"name":"{{name}}"}`,
	}, ExecutionContext{"name": "Ana"}, "POST")
	if err != nil {
		t.Fatalf("build legacy payload: %v", err)
	}
	if payload.(map[string]interface{})["name"] != "Ana" || contentType != "application/json" {
		t.Fatalf("unexpected legacy payload: %#v, %q", payload, contentType)
	}
}

func TestHTTPRequestHeaderFields(t *testing.T) {
	headers, err := httpRequestHeaders(map[string]interface{}{
		"headerMode": "fields",
		"headerFields": []interface{}{
			map[string]interface{}{"key": "authorization", "value": "Bearer {{token}}"},
			map[string]interface{}{"key": "x-client-id", "value": "zubly"},
		},
	}, ExecutionContext{"token": "secret"})
	if err != nil {
		t.Fatalf("build headers: %v", err)
	}
	if headers["Authorization"] != "Bearer secret" || headers["X-Client-Id"] != "zubly" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestHTTPRequestRawJSONHeaders(t *testing.T) {
	headers, err := httpRequestHeaders(map[string]interface{}{
		"headerMode": "rawJSON",
		"headersRaw": `{"Accept":"application/json","X-Account":"{{account}}"}`,
	}, ExecutionContext{"account": "42"})
	if err != nil {
		t.Fatalf("build headers: %v", err)
	}
	if headers["Accept"] != "application/json" || headers["X-Account"] != "42" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestHTTPRequestRejectsUnsafeHeaders(t *testing.T) {
	for _, header := range []string{"Host", "X-Forwarded-For", "Proxy-Authorization", "Transfer-Encoding"} {
		_, err := httpRequestHeaders(map[string]interface{}{
			"headerMode":   "fields",
			"headerFields": []interface{}{map[string]interface{}{"key": header, "value": "value"}},
		}, ExecutionContext{})
		if err == nil || !strings.Contains(err.Error(), "não permitido") {
			t.Errorf("header %s error = %v", header, err)
		}
	}
}

func TestHTTPRequestRejectsHeaderLineBreak(t *testing.T) {
	_, err := httpRequestHeaders(map[string]interface{}{
		"headerMode":   "fields",
		"headerFields": []interface{}{map[string]interface{}{"key": "X-Test", "value": "safe\r\nInjected: true"}},
	}, ExecutionContext{})
	if err == nil || !strings.Contains(err.Error(), "valor inválido") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteHTTPRequestRejectsPrivateDestination(t *testing.T) {
	result := (&Messenger{}).ExecuteHTTPRequest(map[string]interface{}{
		"method": "GET",
		"url":    "http://127.0.0.1/internal",
	}, ExecutionContext{})
	if result["error"] != true || !strings.Contains(result["message"].(string), "non-public IP") {
		t.Fatalf("result = %#v", result)
	}
}
