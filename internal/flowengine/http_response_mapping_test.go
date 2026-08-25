package flowengine

import "testing"

func TestResolveJSONPath(t *testing.T) {
	body := map[string]interface{}{
		"evaluation":  map[string]interface{}{"result": "DENIED"},
		"items":       []interface{}{map[string]interface{}{"id": float64(10)}},
		"special.key": map[string]interface{}{"value": true},
	}
	tests := []struct {
		path string
		want interface{}
	}{
		{path: "evaluation.result", want: "DENIED"},
		{path: "items[0].id", want: float64(10)},
		{path: `$["special.key"].value`, want: true},
		{path: "$", want: body},
	}
	for _, test := range tests {
		got, found := resolveJSONPath(body, test.path)
		if !found {
			t.Fatalf("path %q was not found", test.path)
		}
		if test.path != "$" && got != test.want {
			t.Fatalf("resolveJSONPath(%q) = %#v, want %#v", test.path, got, test.want)
		}
	}
}

func TestApplyHTTPResponseMappings(t *testing.T) {
	response := map[string]interface{}{"data": map[string]interface{}{
		"evaluation": map[string]interface{}{"result": "DENIED", "status": "ACCEPTED"},
		"contratos": []interface{}{
			map[string]interface{}{"contratoId": float64(10243), "policy": nil},
			map[string]interface{}{"contratoId": float64(10380)},
		},
	}}
	data := map[string]interface{}{"responseMappings": []interface{}{
		map[string]interface{}{"path": "evaluation.result", "variableName": "creditResult"},
		map[string]interface{}{"path": "evaluation.status", "variableName": "creditStatus"},
		map[string]interface{}{"path": "contratos[1].contratoId", "variableName": "secondContractId"},
		map[string]interface{}{"path": "contratos[0].policy", "variableName": "contractPolicy"},
	}}
	mapped, mappingErrors := applyHTTPResponseMappings(response, data)
	if len(mappingErrors) != 0 {
		t.Fatalf("unexpected mapping errors: %#v", mappingErrors)
	}
	if mapped["creditResult"] != "DENIED" || mapped["creditStatus"] != "ACCEPTED" {
		t.Fatalf("unexpected mapped context: %#v", mapped)
	}
	if mapped["secondContractId"] != float64(10380) || mapped["contractPolicy"] != "null" {
		t.Fatalf("unexpected array/null mappings: %#v", mapped)
	}
}

func TestValidateHTTPRequestNodeRejectsInvalidResponseMapping(t *testing.T) {
	data := map[string]interface{}{
		"method": "GET", "url": "https://example.com",
		"responseMappings": []interface{}{
			map[string]interface{}{"path": "evaluation.result", "variableName": "_reserved"},
		},
	}
	if err := validateHTTPRequestNode(data); err == nil {
		t.Fatal("expected invalid mapped variable to be rejected")
	}
}
