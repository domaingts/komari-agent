package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAutoDiscoveryPreservesAuthentication(t *testing.T) {
	for _, credentials := range []struct {
		name, id, secret string
	}{
		{"paired", "access-id", "access-secret"},
		{"absent", "", ""},
		{"id only", "access-id", ""},
		{"secret only", "", "access-secret"},
	} {
		t.Run(credentials.name, func(t *testing.T) {
			resetConfigForTest(t)
			flags.AutoDiscoveryKey = "registration-key"
			flags.CFAccessClientID = credentials.id
			flags.CFAccessClientSecret = credentials.secret
			flags.PreferIPVersion = "6"
			panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/clients/register" {
					t.Errorf("unexpected registration request: %s %s", r.Method, r.URL)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer registration-key" {
					t.Errorf("authorization = %q", got)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("registration content type missing")
				}
				id, secret := "", ""
				if credentials.id != "" && credentials.secret != "" {
					id, secret = credentials.id, credentials.secret
				}
				if r.Header.Get("CF-Access-Client-Id") != id || r.Header.Get("CF-Access-Client-Secret") != secret {
					t.Error("Cloudflare header pair was not preserved")
				}
				var request RegisterRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Key != flags.AutoDiscoveryKey {
					t.Errorf("registration body = %+v, error = %v", request, err)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"status":"success","data":{"uuid":"registered-uuid","token":"registered-token"}}`)
			}))
			defer panel.Close()
			flags.Endpoint = panel.URL + "/"
			configPath := filepath.Join(t.TempDir(), "auto-discovery.json")
			if err := registerWithAutoDiscoveryAtPath(configPath); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			var config AutoDiscoveryConfig
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			if config.UUID != "registered-uuid" || config.Token != "registered-token" || flags.Token != config.Token {
				t.Fatalf("registration was not persisted/applied: %+v", config)
			}
		})
	}
}

func TestAutoDiscoveryFailureDoesNotPersist(t *testing.T) {
	resetConfigForTest(t)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer panel.Close()
	flags.Endpoint = panel.URL
	flags.Token = "existing-token"
	configPath := filepath.Join(t.TempDir(), "auto-discovery.json")
	if err := registerWithAutoDiscoveryAtPath(configPath); err == nil {
		t.Fatal("failed registration was accepted")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("failed registration persisted configuration: %v", err)
	}
	if flags.Token != "existing-token" {
		t.Fatal("failed registration changed the token")
	}
}
