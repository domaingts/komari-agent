package utils

import "net/http"

// SetCloudflareAccessHeaders adds the paired Cloudflare Access credentials to
// a panel request. Credentials are intentionally omitted unless both values
// are present; this helper does not mutate pooled HTTP clients or global state.
func SetCloudflareAccessHeaders(headers http.Header, clientID, clientSecret string) {
	if headers == nil || clientID == "" || clientSecret == "" {
		return
	}
	headers.Set("CF-Access-Client-Id", clientID)
	headers.Set("CF-Access-Client-Secret", clientSecret)
}
