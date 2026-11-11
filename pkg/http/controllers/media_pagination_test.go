package controllers

import (
	"fmt"
	"testing"
	"time"

	"sophus/internal/media"
)

func TestPaginateMediaLimitsPageSize(t *testing.T) {
	items := make([]media.Item, 320)
	for index := range items {
		items[index] = media.Item{Name: fmt.Sprintf("file-%03d.png", index)}
	}
	page, total, current, pages := paginateMedia(items, 1, mediaPageSize)
	if len(page) != 150 || total != 320 || current != 1 || pages != 3 {
		t.Fatalf("first page = %d items, total %d, page %d of %d", len(page), total, current, pages)
	}
	page, _, current, _ = paginateMedia(items, 3, mediaPageSize)
	if len(page) != 20 || current != 3 || page[0].Name != "file-300.png" {
		t.Fatalf("last page = %d items starting at %q (page %d)", len(page), page[0].Name, current)
	}
	_, _, current, _ = paginateMedia(items, 99, mediaPageSize)
	if current != 3 {
		t.Fatalf("out-of-range page clamped to %d", current)
	}
	page, total, current, pages = paginateMedia(nil, 0, mediaPageSize)
	if len(page) != 0 || total != 0 || current != 1 || pages != 1 {
		t.Fatalf("empty list = %d items, total %d, page %d of %d", len(page), total, current, pages)
	}
}

func TestFilterMediaAppliesKindQueryAndDates(t *testing.T) {
	day := func(value string) time.Time { parsed, _ := time.Parse("2006-01-02", value); return parsed.Add(12 * time.Hour) }
	items := []media.Item{
		{Name: "Foto-A.png", Kind: "image", CreatedAt: day("2026-01-10")},
		{Name: "foto-b.png", Kind: "image", CreatedAt: day("2026-02-10")},
		{Name: "contrato.pdf", Kind: "document", CreatedAt: day("2026-02-15")},
	}
	got := filterMedia(items, mediaListFilter{Kind: "image", Query: "foto", From: "2026-02-01"})
	if len(got) != 1 || got[0].Name != "foto-b.png" {
		t.Fatalf("filtered = %+v", got)
	}
	if got := filterMedia(items, mediaListFilter{Kind: "all", To: "2026-02-10"}); len(got) != 2 {
		t.Fatalf("date range kept %d items", len(got))
	}
}
