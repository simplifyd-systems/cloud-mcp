package tools

import "testing"

// Omitting the database must address the database every service is created with,
// not send an empty name the API would reject.
func TestDatabaseOrDefault(t *testing.T) {
	if got := databaseOrDefault(""); got != "app" {
		t.Fatalf("got %q, want the default app database", got)
	}
	if got := databaseOrDefault("analytics"); got != "analytics" {
		t.Fatalf("got %q, want the requested database", got)
	}
}
