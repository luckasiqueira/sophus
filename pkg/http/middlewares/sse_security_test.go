package middlewares

import "testing"

func TestSSEConnectionLimit(t *testing.T) {
	const agentID = 987654
	for index := 0; index < maxSSEConnectionsPerAgent; index++ {
		if !acquireSSEConnection(agentID) {
			t.Fatalf("connection %d was unexpectedly rejected", index+1)
		}
	}
	if acquireSSEConnection(agentID) {
		t.Fatal("connection above the per-agent limit was accepted")
	}
	for index := 0; index < maxSSEConnectionsPerAgent; index++ {
		releaseSSEConnection(agentID)
	}
	if !acquireSSEConnection(agentID) {
		t.Fatal("connection was not released")
	}
	releaseSSEConnection(agentID)
}
