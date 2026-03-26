package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
)

type fakeProviderStore struct {
	provider provider.ProviderWithToken
	err      error
	lastID   uuid.UUID
}

func (f *fakeProviderStore) GetWithToken(ctx context.Context, id uuid.UUID) (provider.ProviderWithToken, error) {
	f.lastID = id
	if f.err != nil {
		return provider.ProviderWithToken{}, f.err
	}
	return f.provider, nil
}

type fakeModelStore struct {
	model  model.Model
	err    error
	lastID uuid.UUID
}

func (f *fakeModelStore) Get(ctx context.Context, id uuid.UUID) (model.Model, error) {
	f.lastID = id
	if f.err != nil {
		return model.Model{}, f.err
	}
	return f.model, nil
}

type fakeDoer struct {
	request  *http.Request
	body     []byte
	response *http.Response
	err      error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.request = req
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		f.body = body
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func TestParseRequestPayload(t *testing.T) {
	modelID := uuid.MustParse("5a987e7c-cb1f-4d6f-9ebf-2305e6f7b0ea")
	body := []byte(`{"model":"` + modelID.String() + `","stream":true}`)

	gotID, gotStream, err := parseRequestPayload(body)
	if err != nil {
		t.Fatalf("parseRequestPayload: %v", err)
	}
	if gotID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, gotID)
	}
	if !gotStream {
		t.Fatalf("expected stream true")
	}
}

func TestParseRequestPayloadErrors(t *testing.T) {
	modelID := uuid.MustParse("5a987e7c-cb1f-4d6f-9ebf-2305e6f7b0ea")
	cases := []struct {
		name string
		body []byte
		want error
	}{
		{name: "empty body", body: nil, want: ErrInvalidBody},
		{name: "missing model", body: []byte(`{"stream":true}`), want: ErrMissingModel},
		{name: "invalid uuid", body: []byte(`{"model":"not-a-uuid"}`), want: ErrInvalidBody},
		{name: "stream wrong type", body: []byte(`{"model":"` + modelID.String() + `","stream":"true"}`), want: ErrInvalidBody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseRequestPayload(tc.body)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected error %v, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildRequestURL(t *testing.T) {
	service := &Service{}
	resolved := ResolvedModel{
		Model: model.Model{RemoteName: "remote"},
		Provider: provider.ProviderWithToken{
			Provider: provider.Provider{
				Endpoint:   "https://api.example.com/v1/",
				AuthMethod: provider.AuthMethodBearer,
			},
			Token: "token",
		},
	}

	req, err := service.buildRequest(context.Background(), resolved, []byte(`{"model":"remote"}`), false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.URL.String() != "https://api.example.com/v1/responses" {
		t.Fatalf("expected URL https://api.example.com/v1/responses, got %s", req.URL.String())
	}
}

func TestWriteProxyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid body", err: ErrInvalidBody, want: http.StatusBadRequest},
		{name: "missing model", err: ErrMissingModel, want: http.StatusBadRequest},
		{name: "provider not found", err: provider.ErrProviderNotFound, want: http.StatusNotFound},
		{name: "model not found", err: model.ErrModelNotFound, want: http.StatusNotFound},
		{name: "unsupported auth", err: ErrUnsupportedAuthMethod, want: http.StatusBadRequest},
		{name: "missing token", err: ErrMissingToken, want: http.StatusBadRequest},
		{name: "default", err: errors.New("boom"), want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeProxyError(recorder, tc.err)
			if recorder.Code != tc.want {
				t.Fatalf("expected status %d, got %d", tc.want, recorder.Code)
			}
		})
	}
}

func TestHandlerNotFound(t *testing.T) {
	handler := NewHandler(NewService(NewResolver(&fakeProviderStore{}, &fakeModelStore{}), &fakeDoer{}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/wrong", strings.NewReader("{}"))

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	handler := NewHandler(NewService(NewResolver(&fakeProviderStore{}, &fakeModelStore{}), &fakeDoer{}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", strings.NewReader("{}"))

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("expected Allow header %s, got %s", http.MethodPost, allow)
	}
}

func TestHandlerNonStream(t *testing.T) {
	modelID := uuid.MustParse("5a987e7c-cb1f-4d6f-9ebf-2305e6f7b0ea")
	providerID := uuid.MustParse("63af7c73-8d62-4ce8-81b3-3713a55c9c27")
	providerStore := &fakeProviderStore{provider: provider.ProviderWithToken{Provider: provider.Provider{ID: providerID, Endpoint: "https://api.example.com/v1", AuthMethod: provider.AuthMethodBearer}, Token: "token"}}
	modelStore := &fakeModelStore{model: model.Model{ID: modelID, ProviderID: providerID, RemoteName: "remote-model"}}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Test": []string{"ok"}}, Body: io.NopCloser(strings.NewReader("provider-body"))}
	client := &fakeDoer{response: response}

	handler := NewHandler(NewService(NewResolver(providerStore, modelStore), client))
	recorder := httptest.NewRecorder()
	body := `{"model":"` + modelID.String() + `","stream":false,"prompt":"hi"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Body.String() != "provider-body" {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if client.request == nil {
		t.Fatalf("expected request to upstream")
	}
	if got := client.request.URL.String(); got != "https://api.example.com/v1/responses" {
		t.Fatalf("unexpected URL: %s", got)
	}
	if client.request.Header.Get("Authorization") != "Bearer token" {
		t.Fatalf("missing authorization header")
	}
	var payload map[string]any
	if err := json.Unmarshal(client.body, &payload); err != nil {
		t.Fatalf("unmarshal upstream body: %v", err)
	}
	if payload["model"] != "remote-model" {
		t.Fatalf("expected model remote-model, got %v", payload["model"])
	}
	if payload["stream"] != false {
		t.Fatalf("expected stream false, got %v", payload["stream"])
	}
}

func TestHandlerStream(t *testing.T) {
	modelID := uuid.MustParse("65563f15-6e3f-4c7b-86b9-dae97d9b4d2a")
	providerID := uuid.MustParse("e8b2293a-3a2e-4693-8a94-9bcf1b7d5d42")
	providerStore := &fakeProviderStore{provider: provider.ProviderWithToken{Provider: provider.Provider{ID: providerID, Endpoint: "https://api.example.com/v1", AuthMethod: provider.AuthMethodBearer}, Token: "token"}}
	modelStore := &fakeModelStore{model: model.Model{ID: modelID, ProviderID: providerID, RemoteName: "remote-model"}}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": []string{"10"}}, Body: io.NopCloser(strings.NewReader("stream-data"))}
	client := &fakeDoer{response: response}

	handler := NewHandler(NewService(NewResolver(providerStore, modelStore), client))
	recorder := httptest.NewRecorder()
	body := `{"model":"` + modelID.String() + `","stream":true,"prompt":"hi"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if recorder.Body.String() != "stream-data" {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if client.request == nil {
		t.Fatalf("expected request to upstream")
	}
	if client.request.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("expected Accept header text/event-stream")
	}
	var payload map[string]any
	if err := json.Unmarshal(client.body, &payload); err != nil {
		t.Fatalf("unmarshal upstream body: %v", err)
	}
	if payload["model"] != "remote-model" {
		t.Fatalf("expected model remote-model, got %v", payload["model"])
	}
	if payload["stream"] != true {
		t.Fatalf("expected stream true, got %v", payload["stream"])
	}
}
