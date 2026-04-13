package grpcserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBuildTestModelInputSuccess(t *testing.T) {
	input, err := buildTestModelInput(
		model.Model{RemoteName: "  remote-model  "},
		provider.ProviderWithToken{
			Provider: provider.Provider{
				Endpoint:   " https://example.com ",
				Protocol:   provider.ProtocolResponses,
				AuthMethod: provider.AuthMethodBearer,
			},
			Token: " token ",
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if input.endpoint != "https://example.com" {
		t.Fatalf("expected endpoint to be trimmed")
	}
	if input.remoteName != "remote-model" {
		t.Fatalf("expected remote name to be trimmed")
	}
	if input.protocol != provider.ProtocolResponses {
		t.Fatalf("expected protocol to be set")
	}
	if input.authMethod != provider.AuthMethodBearer {
		t.Fatalf("expected auth method to be set")
	}
	if input.token != "token" {
		t.Fatalf("expected token to be trimmed")
	}
}

func TestBuildTestModelInputErrors(t *testing.T) {
	baseModel := model.Model{RemoteName: "remote"}
	baseProvider := provider.ProviderWithToken{
		Provider: provider.Provider{
			Endpoint:   "https://example.com",
			Protocol:   provider.ProtocolResponses,
			AuthMethod: provider.AuthMethodBearer,
		},
		Token: "token",
	}

	for _, tt := range []struct {
		name    string
		mutate  func(*model.Model, *provider.ProviderWithToken)
		code    codes.Code
		message string
	}{
		{
			name: "missing endpoint",
			mutate: func(_ *model.Model, prov *provider.ProviderWithToken) {
				prov.Endpoint = ""
			},
			code:    codes.FailedPrecondition,
			message: "model endpoint missing",
		},
		{
			name: "missing remote name",
			mutate: func(mdl *model.Model, _ *provider.ProviderWithToken) {
				mdl.RemoteName = ""
			},
			code:    codes.FailedPrecondition,
			message: "model remote name missing",
		},
		{
			name: "unsupported protocol",
			mutate: func(_ *model.Model, prov *provider.ProviderWithToken) {
				prov.Protocol = provider.Protocol("legacy")
			},
			code:    codes.FailedPrecondition,
			message: "unsupported model protocol",
		},
		{
			name: "unsupported auth method",
			mutate: func(_ *model.Model, prov *provider.ProviderWithToken) {
				prov.AuthMethod = provider.AuthMethod("basic")
			},
			code:    codes.FailedPrecondition,
			message: "unsupported auth method",
		},
		{
			name: "missing token",
			mutate: func(_ *model.Model, prov *provider.ProviderWithToken) {
				prov.Token = ""
			},
			code:    codes.FailedPrecondition,
			message: "model token missing",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mdl := baseModel
			prov := baseProvider
			tt.mutate(&mdl, &prov)

			_, err := buildTestModelInput(mdl, prov)
			if err == nil {
				t.Fatalf("expected error")
			}
			var testErr *testModelError
			if !errors.As(err, &testErr) {
				t.Fatalf("expected testModelError, got %T", err)
			}
			if testErr.code != tt.code {
				t.Fatalf("expected code %v, got %v", tt.code, testErr.code)
			}
			if testErr.message != tt.message {
				t.Fatalf("expected message %q, got %q", tt.message, testErr.message)
			}
		})
	}
}

func TestBuildTestModelRequestResponses(t *testing.T) {
	body, headers, err := buildTestModelRequest(testModelInput{
		endpoint:   "https://example.com",
		remoteName: "remote",
		token:      "token",
		protocol:   provider.ProtocolResponses,
		authMethod: provider.AuthMethodBearer,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("expected bearer auth header, got %q", got)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content type header, got %q", got)
	}
	if got := headers.Get("anthropic-version"); got != "" {
		t.Fatalf("unexpected anthropic header: %q", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Fatalf("unexpected x-api-key header: %q", got)
	}
	var payload responsesRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload.Model != "remote" {
		t.Fatalf("expected model name to match")
	}
	if payload.Input != testModelPrompt {
		t.Fatalf("expected prompt to match")
	}
}

func TestBuildTestModelRequestAnthropic(t *testing.T) {
	body, headers, err := buildTestModelRequest(testModelInput{
		endpoint:   "https://example.com",
		remoteName: "remote",
		token:      "token",
		protocol:   provider.ProtocolAnthropicMessages,
		authMethod: provider.AuthMethodXAPIKey,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := headers.Get("x-api-key"); got != "token" {
		t.Fatalf("expected x-api-key header, got %q", got)
	}
	if got := headers.Get("anthropic-version"); got != testModelAnthropicHeader {
		t.Fatalf("expected anthropic version header, got %q", got)
	}
	var payload anthropicRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload.Model != "remote" {
		t.Fatalf("expected model name to match")
	}
	if payload.MaxTokens != testModelAnthropicMaxTokens {
		t.Fatalf("expected max tokens to match")
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("expected one message")
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Content != testModelPrompt {
		t.Fatalf("expected user prompt message")
	}
}

func TestParseResponsesOutput(t *testing.T) {
	body, err := json.Marshal(responsesResponse{
		Output: []responsesOutput{
			{Content: []responsesContent{{Text: ""}}},
			{Content: []responsesContent{{Text: "  response  "}}},
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}
	text, err := parseResponsesOutput(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "response" {
		t.Fatalf("expected trimmed response text, got %q", text)
	}
}

func TestParseAnthropicOutput(t *testing.T) {
	body, err := json.Marshal(anthropicResponse{
		Content: []anthropicContent{
			{Text: ""},
			{Text: "  response  "},
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}
	text, err := parseAnthropicOutput(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "response" {
		t.Fatalf("expected trimmed response text, got %q", text)
	}
}

func TestFormatUpstreamErrorEmpty(t *testing.T) {
	message := formatUpstreamError(http.StatusBadGateway, []byte("  "))
	expected := fmt.Sprintf("request failed with status %d", http.StatusBadGateway)
	if message != expected {
		t.Fatalf("expected message %q, got %q", expected, message)
	}
}

func TestFormatUpstreamErrorTruncates(t *testing.T) {
	body := strings.Repeat("a", testModelMaxErrorBodyBytes+10)
	message := formatUpstreamError(http.StatusBadGateway, []byte(body))
	prefix := fmt.Sprintf("request failed with status %d: ", http.StatusBadGateway)
	if !strings.HasPrefix(message, prefix) {
		t.Fatalf("expected prefix %q", prefix)
	}
	trimmed := strings.TrimPrefix(message, prefix)
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected truncated message suffix")
	}
	if len(trimmed) != testModelMaxErrorBodyBytes+3 {
		t.Fatalf("expected trimmed length %d, got %d", testModelMaxErrorBodyBytes+3, len(trimmed))
	}
}

func TestStatusErrorForTestModel(t *testing.T) {
	t.Run("test model error", func(t *testing.T) {
		err := statusErrorForTestModel(newTestModelError(codes.Unavailable, "boom"))
		statusErr, ok := status.FromError(err)
		if !ok {
			t.Fatalf("expected status error")
		}
		if statusErr.Code() != codes.Unavailable {
			t.Fatalf("expected code %v, got %v", codes.Unavailable, statusErr.Code())
		}
		if statusErr.Message() != "boom" {
			t.Fatalf("expected message %q, got %q", "boom", statusErr.Message())
		}
	})

	t.Run("status passthrough", func(t *testing.T) {
		original := status.Error(codes.NotFound, "missing")
		if got := statusErrorForTestModel(original); got != original {
			t.Fatalf("expected original error to be returned")
		}
	})

	t.Run("generic error", func(t *testing.T) {
		err := statusErrorForTestModel(errors.New("boom"))
		statusErr, ok := status.FromError(err)
		if !ok {
			t.Fatalf("expected status error")
		}
		if statusErr.Code() != codes.Internal {
			t.Fatalf("expected code %v, got %v", codes.Internal, statusErr.Code())
		}
		if statusErr.Message() != "boom" {
			t.Fatalf("expected message %q, got %q", "boom", statusErr.Message())
		}
	})
}
