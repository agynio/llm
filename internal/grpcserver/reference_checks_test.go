package grpcserver

import (
	"context"
	"testing"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/model"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type missingModelStore struct{ fakeModelStore }

func (m *missingModelStore) Get(context.Context, uuid.UUID) (model.Model, error) {
	return model.Model{}, model.ErrModelNotFound
}

func TestResolveModelExists(t *testing.T) {
	organizationID := uuid.MustParse("f79b0bde-9e46-44c0-9756-9eac9f383acd")
	modelID := uuid.MustParse("1bb21ea2-03c8-453b-a0ef-c4a12f0f8f2a")
	models := &fakeModelStore{getModel: model.Model{ID: modelID, OrganizationID: organizationID}}
	server := newTestServer(&fakeProviderStore{}, models)

	resp, err := server.ResolveModelExists(contextWithIdentity(), &llmv1.ResolveModelExistsRequest{
		ModelId:        modelID.String(),
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resp.GetExists() {
		t.Fatal("a model in the organization did not resolve")
	}

	// Another organization answers exactly as a missing model does, so the
	// check cannot be used to probe for models elsewhere.
	resp, err = server.ResolveModelExists(contextWithIdentity(), &llmv1.ResolveModelExistsRequest{
		ModelId:        modelID.String(),
		OrganizationId: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("resolve for another organization: %v", err)
	}
	if resp.GetExists() {
		t.Fatal("a model resolved for an organization it does not belong to")
	}
}

func TestResolveModelExistsOnMissingModel(t *testing.T) {
	server := newTestServer(&fakeProviderStore{}, &missingModelStore{})

	resp, err := server.ResolveModelExists(contextWithIdentity(), &llmv1.ResolveModelExistsRequest{
		ModelId:        uuid.NewString(),
		OrganizationId: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("a missing model should answer false, not error: %v", err)
	}
	if resp.GetExists() {
		t.Fatal("a missing model resolved")
	}
}

func TestResolveModelExistsRejectsMalformedIDs(t *testing.T) {
	server := newTestServer(&fakeProviderStore{}, &fakeModelStore{})

	if _, err := server.ResolveModelExists(contextWithIdentity(), &llmv1.ResolveModelExistsRequest{
		ModelId:        "not-a-uuid",
		OrganizationId: uuid.NewString(),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for a malformed model_id, got %v", err)
	}

	if _, err := server.ResolveModelExists(contextWithIdentity(), &llmv1.ResolveModelExistsRequest{
		ModelId:        uuid.NewString(),
		OrganizationId: "not-a-uuid",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for a malformed organization_id, got %v", err)
	}
}
