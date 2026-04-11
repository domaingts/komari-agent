package update

import "testing"

func TestCurrentVersionDefault(t *testing.T) {
	if CurrentVersion == "" {
		t.Fatal("CurrentVersion should not be empty")
	}
}

func TestRepoDefault(t *testing.T) {
	if Repo != "komari-monitor/komari-agent" {
		t.Fatalf("Repo = %q, want %q", Repo, "komari-monitor/komari-agent")
	}
}
