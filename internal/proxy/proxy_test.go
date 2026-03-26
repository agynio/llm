package proxy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
)

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

func TestParseRequestPayload(t *testing.T) {
	modelID := uuid.MustParse("5a987e7c-cb1f-4d6f-9ebf-2305e6f7b0ea")
	body := []byte(`{"model":"` + modelID.String() + `","stream":true}`)

	payload, gotID, gotStream, err := parseRequestPayload(body)
	if err != nil {
		t.Fatalf("parseRequestPayload: %v", err)
	}
	if gotID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, gotID)
	}
	if !gotStream {
		t.Fatalf("expected stream true")
	}
	if payload["model"] != modelID.String() {
		t.Fatalf("expected payload model %s, got %v", modelID, payload["model"])
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
