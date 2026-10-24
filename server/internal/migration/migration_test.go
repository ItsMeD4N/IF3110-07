package migration

import (
	"net/http/httptest"
	"testing"
)

func TestDatabaseID(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/databases/3/migrations", nil)
	r.SetPathValue("dbID", "3")
	id, ok := databaseID(r)
	if !ok || id != 3 {
		t.Fatalf("databaseID() = %d, %v; want 3, true", id, ok)
	}
	r.SetPathValue("dbID", "0")
	if _, ok := databaseID(r); ok {
		t.Fatal("databaseID accepted zero")
	}
}
