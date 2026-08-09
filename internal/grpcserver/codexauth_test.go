package grpcserver

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCodexPlaceholderAuthShape(t *testing.T) {
	now := time.Date(2026, 8, 9, 16, 59, 35, 0, time.UTC)

	var auth codexAuth
	if err := json.Unmarshal([]byte(codexPlaceholderAuth(now)), &auth); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if auth.AuthMode != "chatgpt" {
		t.Fatalf("auth_mode = %q", auth.AuthMode)
	}
	if auth.OpenAIAPIKey != nil {
		t.Fatalf("OPENAI_API_KEY = %v, want null", *auth.OpenAIAPIKey)
	}
	if auth.Tokens.IDToken == "" {
		t.Fatal("id_token is empty")
	}
	// Blank on purpose: the proxy builds the upstream header from the resolved
	// subscription, so a real credential here would be dead weight.
	if auth.Tokens.AccessToken != "" || auth.Tokens.RefreshToken != "" || auth.Tokens.AccountID != "" {
		t.Fatalf("credential fields not blank: %+v", auth.Tokens)
	}
	if auth.LastRefresh != "2026-08-09T16:59:35Z" {
		t.Fatalf("last_refresh = %q", auth.LastRefresh)
	}
}

// codex decodes the token before it reaches the network, so it has to be a
// real JWT -- three segments, and a claim set that parses.
func TestCodexPlaceholderIDTokenDecodes(t *testing.T) {
	segments := strings.Split(codexPlaceholderIDToken, ".")
	if len(segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(segments))
	}
	for _, segment := range segments[:2] {
		decoded, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			t.Fatalf("decode %q: %v", segment, err)
		}
		var claims map[string]any
		if err := json.Unmarshal(decoded, &claims); err != nil {
			t.Fatalf("parse %s: %v", decoded, err)
		}
	}
}

// A fixed timestamp would age into codex treating the credential as stale and
// refreshing it against a host the platform does not intercept.
func TestCodexPlaceholderAuthStampsEachCall(t *testing.T) {
	first := codexPlaceholderAuth(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	second := codexPlaceholderAuth(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	if first == second {
		t.Fatal("last_refresh did not follow the clock")
	}
}
