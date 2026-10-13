package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
)

func TestNormalizeDepartmentInput(t *testing.T) {
	tests := []struct {
		name      string
		input     departmentInput
		want      departmentInput
		wantValid bool
	}{
		{
			name:      "normalizes name and color",
			input:     departmentInput{Name: "  Comercial  ", Color: "#2563eb"},
			want:      departmentInput{Name: "Comercial", Color: "#2563EB"},
			wantValid: true,
		},
		{
			name:      "uses default color",
			input:     departmentInput{Name: "Suporte"},
			want:      departmentInput{Name: "Suporte", Color: defaultDepartmentColor},
			wantValid: true,
		},
		{
			name:      "rejects invalid color",
			input:     departmentInput{Name: "Suporte", Color: "purple"},
			want:      departmentInput{Name: "Suporte", Color: "PURPLE"},
			wantValid: false,
		},
		{
			name:      "rejects empty name",
			input:     departmentInput{Name: "  ", Color: "#6123CC"},
			want:      departmentInput{Name: "", Color: "#6123CC"},
			wantValid: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := normalizeDepartmentInput(test.input)
			if got != test.want || valid != test.wantValid {
				t.Fatalf("normalizeDepartmentInput() = (%+v, %t), want (%+v, %t)", got, valid, test.want, test.wantValid)
			}
		})
	}
}

func TestReadDepartmentRequestFromBrowserForm(t *testing.T) {
	app := iris.New()
	var got departmentInput
	app.Put("/departments/7", func(ctx iris.Context) {
		if err := readDepartmentRequest(ctx, &got); err != nil {
			ctx.StopWithError(iris.StatusBadRequest, err)
			return
		}
		ctx.StatusCode(iris.StatusNoContent)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "/departments/7", strings.NewReader("name=Comercial&color=%232563EB"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != iris.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, iris.StatusNoContent)
	}
	if got.Name != "Comercial" || got.Color != "#2563EB" {
		t.Fatalf("decoded input = %+v", got)
	}
}

func TestReadAgentDepartmentsFromBrowserForm(t *testing.T) {
	app := iris.New()
	var got agentDepartmentsInput
	app.Put("/departments/agents/4", func(ctx iris.Context) {
		if err := readDepartmentRequest(ctx, &got); err != nil {
			ctx.StopWithError(iris.StatusBadRequest, err)
			return
		}
		ctx.StatusCode(iris.StatusNoContent)
	})
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "/departments/agents/4", strings.NewReader("departmentIds=2&departmentIds=5"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != iris.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, iris.StatusNoContent)
	}
	if len(got.DepartmentIds) != 2 || got.DepartmentIds[0] != 2 || got.DepartmentIds[1] != 5 {
		t.Fatalf("decoded department IDs = %v", got.DepartmentIds)
	}
}
