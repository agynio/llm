package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestParseRequestPayload(t *testing.T) {
	modelID := uuid.MustParse("5a987e7c-cb1f-4d6f-9ebf-2305e6f7b0ea")
	body := []byte(`{"model":"` + modelID.String() + `","stream":true}`)

	payload, gotID, gotStream, err := parseRequestPayload(body)
	if err != nil {
		t.Fatalf("parseRequestPayload: %v", err)
	}
	if payload == nil {
		t.Fatalf("expected payload")
	}
	if gotID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, gotID)
	}
	if !gotStream {
		t.Fatalf("expected stream true")
	}
}

func TestParseRequestPayloadErrors(t *testing.T) {
	modelID := uuid.MustParse("5893a536-c0ba-4d68-acde-bf1d703514ef")
	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "invalid json", body: "{", want: ErrInvalidBody},
		{name: "missing model", body: `{"stream":true}`, want: ErrMissingModel},
		{name: "model not uuid", body: `{"model":"not-a-uuid"}`, want: ErrInvalidBody},
		{name: "stream wrong type", body: `{"model":"` + modelID.String() + `","stream":"true"}`, want: ErrInvalidBody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseRequestPayload([]byte(tc.body))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected error %v, got %v", tc.want, err)
			}
		})
	}
}
func TestUpdateRequestBody(t *testing.T) {
	remote := "remote-model"
	body := []byte(`{"model":"local","stream":false,"extra":1}`)
	updated, err := updateRequestBody(body, remote, true)
	if err != nil {
		t.Fatalf("updateRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(updated, &payload); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if payload["model"] != remote {
		t.Fatalf("expected model %s, got %v", remote, payload["model"])
	}
	if payload["stream"] != true {
		t.Fatalf("expected stream true, got %v", payload["stream"])
	}
	if payload["extra"].(float64) != 1 {
		t.Fatalf("expected extra to remain")
	}
}

func TestReadSSE(t *testing.T) {
	input := strings.NewReader("event: message\n" +
		"data: hello\n" +
		"data: world\n\n" +
		"data: [DONE]\n\n")

	var events []Event
	err := ReadSSE(context.Background(), input, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSSE: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "message" {
		t.Fatalf("expected event type message, got %q", events[0].EventType)
	}
	if string(events[0].Data) != "hello\nworld" {
		t.Fatalf("unexpected data: %s", string(events[0].Data))
	}
	if events[1].EventType != "" {
		t.Fatalf("expected empty event type, got %q", events[1].EventType)
	}
	if string(events[1].Data) != "[DONE]" {
		t.Fatalf("unexpected data: %s", string(events[1].Data))
	}
}

func TestReadSSEContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ReadSSE(ctx, strings.NewReader("data: hello\n\n"), func(Event) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestResolverResolve(t *testing.T) {
	modelID := uuid.MustParse("5e3d6e5b-907c-4c7b-b470-e8a2d4c9b7f9")
	providerID := uuid.MustParse("63af7c73-8d62-4ce8-81b3-3713a55c9c27")
	mdl := model.Model{ID: modelID, ProviderID: providerID}
	prov := provider.ProviderWithToken{Provider: provider.Provider{ID: providerID}, Token: "token"}

	modelStore := &fakeModelStore{model: mdl}
	providerStore := &fakeProviderStore{provider: prov}
	resolver := NewResolver(providerStore, modelStore)

	resolved, err := resolver.Resolve(context.Background(), modelID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Model.ID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, resolved.Model.ID)
	}
	if resolved.Provider.ID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, resolved.Provider.ID)
	}
	if modelStore.lastID != modelID {
		t.Fatalf("expected model lookup for %s, got %s", modelID, modelStore.lastID)
	}
	if providerStore.lastID != providerID {
		t.Fatalf("expected provider lookup for %s, got %s", providerID, providerStore.lastID)
	}
}

func TestResolverResolveErrors(t *testing.T) {
	modelID := uuid.MustParse("8f556852-d99a-41a9-83ef-3ef053f048fe")
	providerID := uuid.MustParse("8f6cdcad-8e14-4d67-89e2-75462e493f46")

	modelStore := &fakeModelStore{err: model.ErrModelNotFound}
	resolver := NewResolver(&fakeProviderStore{}, modelStore)
	if _, err := resolver.Resolve(context.Background(), modelID); !errors.Is(err, model.ErrModelNotFound) {
		t.Fatalf("expected model not found, got %v", err)
	}

	modelStore = &fakeModelStore{model: model.Model{ID: modelID, ProviderID: providerID}}
	providerStore := &fakeProviderStore{err: provider.ErrProviderNotFound}
	resolver = NewResolver(providerStore, modelStore)
	if _, err := resolver.Resolve(context.Background(), modelID); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("expected provider not found, got %v", err)
	}
}

func TestUpdateRequestBodyRejectsEmpty(t *testing.T) {
	_, err := updateRequestBody(nil, "remote", false)
	if !errors.Is(err, ErrInvalidBody) {
		t.Fatalf("expected invalid body, got %v", err)
	}
}

func TestUpdateRequestPayloadRoundTrip(t *testing.T) {
	payload := map[string]any{"model": "local", "stream": false}
	updated, err := updateRequestPayload(payload, "remote", true)
	if err != nil {
		t.Fatalf("updateRequestPayload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("unmarshal updated payload: %v", err)
	}
	if decoded["model"] != "remote" {
		t.Fatalf("expected model remote, got %v", decoded["model"])
	}
	if decoded["stream"] != true {
		t.Fatalf("expected stream true, got %v", decoded["stream"])
	}
}
func TestResolverResolveTimeFields(t *testing.T) {
	modelID := uuid.MustParse("65563f15-6e3f-4c7b-86b9-dae97d9b4d2a")
	providerID := uuid.MustParse("e8b2293a-3a2e-4693-8a94-9bcf1b7d5d42")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	modelStore := &fakeModelStore{model: model.Model{ID: modelID, ProviderID: providerID, CreatedAt: created}}
	providerStore := &fakeProviderStore{provider: provider.ProviderWithToken{Provider: provider.Provider{ID: providerID}}}
	resolver := NewResolver(providerStore, modelStore)
	resolved, err := resolver.Resolve(context.Background(), modelID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resolved.Model.CreatedAt.Equal(created) {
		t.Fatalf("expected created_at %v, got %v", created, resolved.Model.CreatedAt)
	}
}
