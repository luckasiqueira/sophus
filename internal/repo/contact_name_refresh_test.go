package repo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestContactNameRefreshDoesNotBlockOrRepeatKnownLookups(t *testing.T) {
	oldURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = oldURL })
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	defer func() {
		if !released {
			close(release)
		}
	}()
	ApiBaseURL = server.URL
	connection := ConnectionEVO{Id: 11, CompanyID: 3}
	contact := Contact{Id: 500, CompanyId: 3, Name: "Contato salvo automaticamente", Number: "5511999999999"}
	returned := make(chan struct{})
	go func() { ScheduleContactNameRefresh(contact, connection); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("name lookup blocked the caller")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background lookup did not start")
	}
	ScheduleContactNameRefresh(contact, connection)
	known := contact
	known.Id++
	known.Name = "Nome editado manualmente"
	ScheduleContactNameRefresh(known, connection)
	if len(contactNameRefreshSlots) != 1 {
		t.Error("duplicate or known contact name started another lookup")
	}
	close(release)
	released = true
	deadline := time.Now().Add(time.Second)
	for {
		if _, active := contactNameRefreshInFlight.Load([2]int{contact.CompanyId, contact.Id}); !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not release its slot")
		}
		time.Sleep(time.Millisecond)
	}
}
