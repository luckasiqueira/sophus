package monitor

import (
	"testing"
	"time"
)

func TestTrafficAndCPUAccounting(t *testing.T) {
	traffic := &Traffic{}
	traffic.Record(10*time.Millisecond, false)
	traffic.Record(30*time.Millisecond, true)
	traffic.Webhook()
	traffic.Webhook()
	requests, errors, webhooks, _, latencies := traffic.drain()
	if requests != 2 || errors != 1 || webhooks != 2 || len(latencies) != 2 {
		t.Fatalf("drain: %d %d %d %v", requests, errors, webhooks, latencies)
	}
	if average, p95 := latencyStats(latencies); average != 20 || p95 != 30 {
		t.Fatalf("latency average=%v p95=%v", average, p95)
	}
	if requests, _, _, _, latencies := traffic.drain(); requests != 0 || len(latencies) != 0 {
		t.Fatal("drain did not reset the window")
	}
	if busy := (cpuTimes{idle: 150, total: 400}).busyPercent(cpuTimes{idle: 100, total: 200}); busy != 75 {
		t.Fatalf("system CPU = %v, want 75", busy)
	}
	if busy := (cpuTimes{idle: 1, total: 1}).busyPercent(cpuTimes{idle: 5, total: 10}); busy != 0 {
		t.Fatalf("counter reset produced %v", busy)
	}
	if _, ok := processCPUTime(); !ok {
		t.Fatal("process CPU time unavailable on this platform")
	}
}
