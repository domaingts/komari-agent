package utils

import (
	"net/http"
	"testing"
)

func TestSetCloudflareAccessHeadersRequiresBothCredentials(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		secret string
	}{
		{name: "absent", id: "", secret: ""},
		{name: "missing id", id: "", secret: "secret"},
		{name: "missing secret", id: "client", secret: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			SetCloudflareAccessHeaders(headers, tt.id, tt.secret)
			if got := headers.Values("CF-Access-Client-Id"); len(got) != 0 {
				t.Fatalf("client ID header = %q, want absent", got)
			}
			if got := headers.Values("CF-Access-Client-Secret"); len(got) != 0 {
				t.Fatalf("client secret header = %q, want absent", got)
			}
		})
	}
}

func TestSetCloudflareAccessHeadersSetsPair(t *testing.T) {
	headers := make(http.Header)
	SetCloudflareAccessHeaders(headers, "client-id", "client-secret")

	if got := headers.Get("CF-Access-Client-Id"); got != "client-id" {
		t.Fatalf("client ID header = %q, want %q", got, "client-id")
	}
	if got := headers.Get("CF-Access-Client-Secret"); got != "client-secret" {
		t.Fatalf("client secret header = %q, want %q", got, "client-secret")
	}
}

func TestSetCloudflareAccessHeadersNilHeader(t *testing.T) {
	SetCloudflareAccessHeaders(nil, "client-id", "client-secret")
}
