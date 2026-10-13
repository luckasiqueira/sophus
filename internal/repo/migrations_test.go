package repo

import (
	"strconv"
	"strings"
	"testing"
)

func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	versions := make(map[int]string)
	for _, entry := range entries {
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			t.Fatalf("invalid migration name: %s", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			t.Fatalf("duplicate migration version %d: %s and %s", version, previous, entry.Name())
		}
		versions[version] = entry.Name()
	}
}
