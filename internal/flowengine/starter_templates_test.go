package flowengine

import (
	"encoding/json"
	"testing"
)

func TestStarterTemplatesAreExecutableGraphs(t *testing.T) {
	seen := map[string]bool{}
	for _, template := range StarterTemplates() {
		t.Run(template.ID, func(t *testing.T) {
			if seen[template.ID] || template.Name == "" || template.TriggerType != "always" || template.TriggerValue != "" || template.Instructions == "" {
				t.Fatal("incomplete or duplicate starter template")
			}
			seen[template.ID] = true
			data, err := json.Marshal(template.FlowData)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateActiveFlowData(data); err != nil {
				t.Fatalf("starter cannot be activated: %v", err)
			}
			for _, node := range template.FlowData.Nodes {
				for _, key := range []string{"credentialId", "aiAgentId", "assignId", "departmentId", "flowId", "templateId"} {
					if _, exists := node.Data[key]; exists {
						t.Fatalf("template contains company-specific dependency %q", key)
					}
				}
			}
		})
	}
	if len(seen) < 5 {
		t.Fatal("basic starter examples missing")
	}
}

func TestStarterTemplateEditsDoNotChangeCatalog(t *testing.T) {
	first := StarterTemplates()
	first[0].FlowData.Nodes[1].Data["message"] = "Mensagem modificada"
	first[0].FlowData.Nodes[0].Position["x"] = 999
	second := StarterTemplates()
	if second[0].FlowData.Nodes[1].Data["message"] == "Mensagem modificada" || second[0].FlowData.Nodes[0].Position["x"] == 999 {
		t.Fatal("editing a draft mutated the shared template catalog")
	}
}
