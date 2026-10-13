package reporting

import (
	"sophus/internal/repo"
	"sort"
)

type Bar struct {
	Label string
	Value int64
}

func ConversationBars(report repo.ConversationReport, dimension string) []Bar {
	counts := map[string]int64{}
	for _, row := range report.Rows {
		label := row.Connection
		if dimension == "department" {
			label = row.Department
		}
		if dimension == "agent" {
			label = row.Agent
		}
		counts[label] += row.Conversations
	}
	bars := make([]Bar, 0, len(counts))
	for label, value := range counts {
		bars = append(bars, Bar{label, value})
	}
	sort.Slice(bars, func(i, j int) bool {
		if bars[i].Value == bars[j].Value {
			return bars[i].Label < bars[j].Label
		}
		return bars[i].Value > bars[j].Value
	})
	if len(bars) > 8 {
		var other int64
		for _, bar := range bars[8:] {
			other += bar.Value
		}
		bars = append(bars[:8], Bar{"Outros", other})
	}
	return bars
}

func MaxBar(bars []Bar) int64 {
	var max int64
	for _, bar := range bars {
		if bar.Value > max {
			max = bar.Value
		}
	}
	return max
}

func MaxTrend(points []repo.ReportTrend) int64 {
	var max int64
	for _, point := range points {
		if point.Sent > max {
			max = point.Sent
		}
		if point.Received > max {
			max = point.Received
		}
	}
	return max
}

func Percent(value, max int64) float64 {
	if max <= 0 || value <= 0 {
		return 0
	}
	return 100 * float64(value) / float64(max)
}

func OptionLabel(id int, options []repo.ReportOption, empty string) string {
	if id == 0 {
		return "Todas as opções"
	}
	if id == -1 {
		return empty
	}
	for _, option := range options {
		if option.ID == id {
			return option.Name
		}
	}
	return "Opção indisponível"
}
