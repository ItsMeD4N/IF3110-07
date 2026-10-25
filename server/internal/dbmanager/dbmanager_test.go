package dbmanager

import (
	"net/http/httptest"
	"testing"
)

func TestDBID(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/databases/12/schema", nil)
	r.SetPathValue("dbID", "12")
	id, ok := dbID(r)
	if !ok || id != 12 {
		t.Fatalf("dbID() = %d, %v; want 12, true", id, ok)
	}
	r.SetPathValue("dbID", "-1")
	if _, ok := dbID(r); ok {
		t.Fatal("dbID accepted a negative ID")
	}
	r.SetPathValue("dbID", "abc")
	if _, ok := dbID(r); ok {
		t.Fatal("dbID accepted a non-numeric ID")
	}
}

func TestQueryInt(t *testing.T) {
	r := httptest.NewRequest("GET", "/?limit=25", nil)
	if got := queryInt(r, "limit", 50); got != 25 {
		t.Fatalf("got %d, want 25", got)
	}
	if got := queryInt(r, "missing", 50); got != 50 {
		t.Fatalf("got %d, want default 50", got)
	}
	if got := queryInt(r, "limit", 50); got < 1 {
		t.Fatalf("limit must be positive, got %d", got)
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"public", "app"}, "app") {
		t.Fatal("expected schema to be found")
	}
	if contains([]string{"public", "app"}, "pg_catalog") {
		t.Fatal("unexpected schema match")
	}
}
