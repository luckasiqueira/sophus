package flowengine

import (
	"strings"
	"testing"
)

func TestMenuListLimitReason(t *testing.T) {
	menu := func(rows int, title string) menuPayload {
		section := menuSection{Title: "Opções"}
		for index := 0; index < rows; index++ {
			section.Rows = append(section.Rows, menuRow{Title: title, RowID: strings.Repeat("x", index+1)})
		}
		return menuPayload{ButtonText: "Ver Menu", Description: "Escolha", Sections: []menuSection{section}}
	}
	if reason := menuListLimitReason(menu(10, "Opção com acentuação")); reason != "" {
		t.Fatalf("10 short options fit a list: %s", reason)
	}
	if reason := menuListLimitReason(menu(11, "Opção")); !strings.Contains(reason, "11 opções") {
		t.Fatalf("more than 10 options must fall back to text: %q", reason)
	}
	if reason := menuListLimitReason(menu(2, strings.Repeat("a", 25))); reason == "" {
		t.Fatal("an option longer than 24 characters must fall back to text")
	}
	long := menu(2, "Opção")
	long.ButtonText = strings.Repeat("b", 21)
	if menuListLimitReason(long) == "" {
		t.Fatal("a button text longer than 20 characters must fall back to text")
	}
}

func TestMenuFooterCanBeRemoved(t *testing.T) {
	if footer := menuFooter(map[string]interface{}{"footerText": ""}, nil); footer != "" {
		t.Fatalf("a cleared footer must stay empty, got %q", footer)
	}
	if footer := menuFooter(map[string]interface{}{}, nil); footer != "Sophus" {
		t.Fatalf("menus saved without the field keep the old default, got %q", footer)
	}
	if footer := menuFooter(map[string]interface{}{"footerText": " Loja {{cidade}} "}, ExecutionContext{"cidade": "Centro"}); footer != "Loja Centro" {
		t.Fatalf("footer = %q", footer)
	}
	if text := formatMenuAsText(menuPayload{Description: "Escolha", Sections: []menuSection{{Title: "S", Rows: []menuRow{{Title: "A", RowID: "a"}}}}}); strings.Contains(text, "_") {
		t.Fatalf("a menu without footer must not print one: %q", text)
	}
}
