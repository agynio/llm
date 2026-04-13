package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"google.golang.org/grpc/codes"
)

func TestNewTestModelInputSuccess(t *testing.T) {
	input, err := newTestModelInput(
		model.Model{RemoteName: "remote-model"},
		provider.ProviderWithToken{
			Provider: provider.Provider{
				Endpoint:   "https://example.com",
				Protocol:   provider.ProtocolResponses,
				AuthMethod: provider.AuthMethodBearer,
			},
			Token: "token",
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if input.endpoint != "https://example.com" {
		t.Fatalf("expected endpoint to be set")
	}
	if input.remoteName != "remote-model" {
		t.Fatalf("expected remote name to be set")
	}
	if input.protocol != provider.ProtocolResponses {
		t.Fatalf("expected protocol to be set")
	}
	if input.authMethod != provider.AuthMethodBearer {
		t.Fatalf("expected auth method to be set")
	}
	if input.token != "token" {
		t.Fatalf("expected token to be set")
	}
}

func TestNewTestModelInputErrors(t *testing.T) {
	baseModel := func() model.Model {
		return model.Model{RemoteName: "remote-model"}
	}
	baseProvider := func() provider.ProviderWithToken {
		return provider.ProviderWithToken{
			Provider: provider.Provider{
				Endpoint:   "https://example.com",
				Protocol:   provider.ProtocolResponses,
				AuthMethod: provider.AuthMethodBearer,
			},
			Token: "token",
		}
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
				prov.Protocol = provider.Protocol("")
			},
			code:    codes.FailedPrecondition,
			message: "unsupported model protocol",
		},
		{
			name: "unsupported auth method",
			mutate: func(_ *model.Model, prov *provider.ProviderWithToken) {
				prov.AuthMethod = provider.AuthMethod("")
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
			mdl := baseModel()
			prov := baseProvider()
			tt.mutate(&mdl, &prov)
			_, err := newTestModelInput(mdl, prov)
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

func TestParseResponsesOutputEmpty(t *testing.T) {
	body, err := json.Marshal(responsesResponse{
		Output: []responsesOutput{{Content: []responsesContent{{Text: "  "}}}},
	})
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}
	_, err = parseResponsesOutput(body)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "response output text missing" {
		t.Fatalf("expected missing output error, got %q", err.Error())
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

func TestFormatUpstreamErrorTruncates(t *testing.T) {
	body := strings.Repeat("✓", (testModelMaxErrorBodyBytes/3)+10)
	message := formatUpstreamError(http.StatusBadGateway, []byte(body))
	prefix := fmt.Sprintf("request failed with status %d: ", http.StatusBadGateway)
	if !strings.HasPrefix(message, prefix) {
		t.Fatalf("expected prefix %q", prefix)
	}
	trimmed := strings.TrimPrefix(message, prefix)
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected truncated message suffix")
	}
	trimmedContent := strings.TrimSuffix(trimmed, "...")
	if len(trimmedContent) > testModelMaxErrorBodyBytes {
		t.Fatalf("expected trimmed length <= %d, got %d", testModelMaxErrorBodyBytes, len(trimmedContent))
	}
	if trimmedContent == "" {
		t.Fatalf("expected truncated content to be non-empty")
	}
	if !utf8.ValidString(trimmedContent) {
		t.Fatalf("expected valid UTF-8 content")
	}
}

func TestTestModelSuccessResponses(t *testing.T) {
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			select {
			case errCh <- fmt.Errorf("expected POST, got %s", r.Method):
			default:
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			select {
			case errCh <- fmt.Errorf("expected bearer auth, got %q", got):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			select {
			case errCh <- fmt.Errorf("expected content type, got %q", got):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			select {
			case errCh <- fmt.Errorf("failed to read body: %v", err):
			default:
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload responsesRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			select {
			case errCh <- fmt.Errorf("invalid payload: %v", err):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "remote" || payload.Input != testModelPrompt {
			select {
			case errCh <- fmt.Errorf("unexpected payload: %#v", payload):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(responsesResponse{
			Output: []responsesOutput{{Content: []responsesContent{{Text: "ok"}}}},
		}); err != nil {
			select {
			case errCh <- fmt.Errorf("failed to write response: %v", err):
			default:
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := testModel(ctx, server.Client(), testModelInput{
		endpoint:   server.URL,
		remoteName: "remote",
		token:      "token",
		protocol:   provider.ProtocolResponses,
		authMethod: provider.AuthMethodBearer,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "ok" {
		t.Fatalf("expected output to match, got %q", output)
	}
	select {
	case err := <-errCh:
		t.Fatalf("server validation failed: %v", err)
	default:
	}
}

func TestTestModelSuccessAnthropic(t *testing.T) {
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			select {
			case errCh <- fmt.Errorf("expected POST, got %s", r.Method):
			default:
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("x-api-key"); got != "token" {
			select {
			case errCh <- fmt.Errorf("expected x-api-key, got %q", got):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("anthropic-version"); got != testModelAnthropicHeader {
			select {
			case errCh <- fmt.Errorf("expected anthropic version, got %q", got):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			select {
			case errCh <- fmt.Errorf("expected content type, got %q", got):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			select {
			case errCh <- fmt.Errorf("failed to read body: %v", err):
			default:
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload anthropicRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			select {
			case errCh <- fmt.Errorf("invalid payload: %v", err):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "remote" || payload.MaxTokens != testModelAnthropicMaxTokens {
			select {
			case errCh <- fmt.Errorf("unexpected payload: %#v", payload):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != testModelPrompt {
			select {
			case errCh <- fmt.Errorf("unexpected messages: %#v", payload.Messages):
			default:
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContent{{Text: "ok"}},
		}); err != nil {
			select {
			case errCh <- fmt.Errorf("failed to write response: %v", err):
			default:
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := testModel(ctx, server.Client(), testModelInput{
		endpoint:   server.URL,
		remoteName: "remote",
		token:      "token",
		protocol:   provider.ProtocolAnthropicMessages,
		authMethod: provider.AuthMethodXAPIKey,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "ok" {
		t.Fatalf("expected output to match, got %q", output)
	}
	select {
	case err := <-errCh:
		t.Fatalf("server validation failed: %v", err)
	default:
	}
}

func TestTestModelErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("nope"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := testModel(ctx, server.Client(), testModelInput{
		endpoint:   server.URL,
		remoteName: "remote",
		token:      "token",
		protocol:   provider.ProtocolResponses,
		authMethod: provider.AuthMethodBearer,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var testErr *testModelError
	if !errors.As(err, &testErr) {
		t.Fatalf("expected testModelError, got %T", err)
	}
	if testErr.code != codes.Unavailable {
		t.Fatalf("expected code %v, got %v", codes.Unavailable, testErr.code)
	}
	if testErr.message != "request failed with status 502: nope" {
		t.Fatalf("unexpected message %q", testErr.message)
	}
}
