package grpcserver

import (
	"encoding/json"
	"time"
)

// A ChatGPT-mode codex refuses to start without this file, and parses it before
// it ever reaches the network. The id_token is a syntactically valid RS256 JWT
// whose claim set is empty -- enough to be decoded, and carrying nothing. Every
// credential field is blank on purpose: the proxy builds the upstream header
// from the resolved subscription, so anything here would be dead weight the
// container had no business holding.
const codexPlaceholderIDToken = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.e30." +
	"upI8kdqCUNUUgd1IrUpjNDDiif7yJZT_pI03g_DW6-aFIxEZD_kszt6E33_cjiUv6tWkutqTDgLr8XfKzFVfBKUTA9QDhpY9" +
	"Imavnu-CW5k6xSUdiSiwo5b7EyMGBO7bRPN9b0L3OL2CzqowqOalYiqY0lldy1IDUgD_n5Cm0CFLpMOipb_vGf2KJFYmR8T_" +
	"oZOAJzf6FYbZKFhjujeXiVLah2kj2qIZMIws9Q5t485udznl_gNlQwcnVB3bqEd6_msgUOo0ZRkyctQz9rZ70-JBviwXzhiq" +
	"oeDGeiqJeRbaWLOjhmpWlwc6DJgRgP1H59dzV9htOWjST6cs8vpG2A"

type codexAuth struct {
	AuthMode     string          `json:"auth_mode"`
	OpenAIAPIKey *string         `json:"OPENAI_API_KEY"`
	Tokens       codexAuthTokens `json:"tokens"`
	LastRefresh  string          `json:"last_refresh"`
}

type codexAuthTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// codexPlaceholderAuth stamps last_refresh at the moment the attachment is
// served, which is workload assembly. A fixed timestamp would age into codex
// treating the credential as stale and refreshing it -- against an auth host
// the platform does not intercept, so the refresh fails and takes the CLI with
// it. Freshly stamped, nothing tries.
func codexPlaceholderAuth(now time.Time) string {
	payload, err := json.Marshal(codexAuth{
		AuthMode: "chatgpt",
		Tokens: codexAuthTokens{
			IDToken: codexPlaceholderIDToken,
		},
		LastRefresh: now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return ""
	}
	return string(payload)
}
