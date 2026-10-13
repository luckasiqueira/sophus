package repo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrivateSessionCachePreservesRevocationVersionWithoutPassword(t *testing.T) {
	session := SessionAgent{Agent: Agent{Id: 7, CompanyId: 3, Role: RoleAdmin, IsActive: true, Password: "do-not-cache-this-hash"}, CompanyActive: true, SessionVersion: 4}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-cache-this-hash") {
		t.Fatal("password hash leaked into object cache")
	}
	var restored SessionAgent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.SessionVersion != 4 || restored.Agent.Id != 7 || !restored.CompanyActive {
		t.Fatalf("cache lost authenticated session fields: %+v", restored)
	}
}

func TestPrivateOperationCachePreservesCompanyTimezone(t *testing.T) {
	cached := cachedOperationSettings{Settings: CompanyOperationSettings{PhoneConversationCreationEnabled: true}, Timezone: "America/Rio_Branco"}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	var restored cachedOperationSettings
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Timezone != "America/Rio_Branco" || !restored.Settings.PhoneConversationCreationEnabled {
		t.Fatalf("cache lost operation settings: %+v", restored)
	}
}

func TestCachedAccessDoesNotExtendSubscriptionPastItsDeadline(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(time.Second)
	access := cachedCompanyAccess{Allowed: true, ValidUntil: &deadline}
	if !access.valid(now) {
		t.Fatal("active subscription rejected")
	}
	if access.valid(deadline.Add(time.Nanosecond)) {
		t.Fatal("cached access extended expired subscription")
	}
	if (cachedCompanyAccess{Allowed: true}).valid(now) {
		t.Fatal("unbounded cached grant accepted")
	}
}
