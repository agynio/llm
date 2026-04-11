package grpcserver

import (
	"context"
	"errors"
	"testing"
	"time"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestToStatusErrorMappings(t *testing.T) {
	statusErr := status.Error(codes.NotFound, "already status")
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "status error passthrough", err: statusErr, code: codes.NotFound},
		{name: "provider not found", err: provider.ErrProviderNotFound, code: codes.NotFound},
		{name: "model not found", err: model.ErrModelNotFound, code: codes.NotFound},
		{name: "provider in use", err: provider.ErrProviderInUse, code: codes.FailedPrecondition},
		{name: "no provider fields", err: provider.ErrNoFieldsToUpdate, code: codes.InvalidArgument},
		{name: "no model fields", err: model.ErrNoFieldsToUpdate, code: codes.InvalidArgument},
		{name: "fallback", err: errors.New("boom"), code: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := toStatusError(tc.err)
			if status.Code(err) != tc.code {
				t.Fatalf("expected code %v, got %v", tc.code, status.Code(err))
			}
		})
	}
}

type fakeProviderStore struct {
	createOrganizationID uuid.UUID
	createAuthMethod     provider.AuthMethod
	createProtocol       provider.Protocol
	listOrganizationID   uuid.UUID
	getID                uuid.UUID
	getWithTokenID       uuid.UUID
	getWithTokenOrgID    uuid.UUID
	getWithTokenAuth     provider.AuthMethod
	getWithTokenProtocol provider.Protocol
	updateID             uuid.UUID
	updateAuthMethod     provider.AuthMethod
	updateProtocol       provider.Protocol
	updateAuthMethodSet  bool
	updateProtocolSet    bool
	deleteID             uuid.UUID
}

func (f *fakeProviderStore) Create(ctx context.Context, input provider.CreateInput) (provider.Provider, error) {
	f.createOrganizationID = input.OrganizationID
	f.createAuthMethod = input.AuthMethod
	f.createProtocol = input.Protocol
	return provider.Provider{
		ID:             uuid.MustParse("b0d2c70c-f2e7-4b33-94a1-d52468ed7ff1"),
		OrganizationID: input.OrganizationID,
		Endpoint:       input.Endpoint,
		AuthMethod:     input.AuthMethod,
		Protocol:       input.Protocol,
		CreatedAt:      time.Unix(0, 0),
		UpdatedAt:      time.Unix(0, 0),
	}, nil
}

func (f *fakeProviderStore) Get(ctx context.Context, id uuid.UUID) (provider.Provider, error) {
	f.getID = id
	return provider.Provider{ID: id, OrganizationID: uuid.Nil, Endpoint: "https://example.com", AuthMethod: provider.AuthMethodBearer, Protocol: provider.ProtocolResponses, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeProviderStore) GetWithToken(ctx context.Context, id uuid.UUID) (provider.ProviderWithToken, error) {
	f.getWithTokenID = id
	authMethod := f.getWithTokenAuth
	if authMethod == "" {
		authMethod = provider.AuthMethodBearer
	}
	protocol := f.getWithTokenProtocol
	if protocol == "" {
		protocol = provider.ProtocolResponses
	}
	return provider.ProviderWithToken{
		Provider: provider.Provider{
			ID:             id,
			OrganizationID: f.getWithTokenOrgID,
			Endpoint:       "https://example.com",
			AuthMethod:     authMethod,
			Protocol:       protocol,
			CreatedAt:      time.Unix(0, 0),
			UpdatedAt:      time.Unix(0, 0),
		},
		Token: "token",
	}, nil
}

func (f *fakeProviderStore) Update(ctx context.Context, input provider.UpdateInput) (provider.Provider, error) {
	f.updateID = input.ID
	authMethod := provider.AuthMethodBearer
	protocol := provider.ProtocolResponses
	if input.AuthMethod != nil {
		f.updateAuthMethod = *input.AuthMethod
		f.updateAuthMethodSet = true
		authMethod = *input.AuthMethod
	}
	if input.Protocol != nil {
		f.updateProtocol = *input.Protocol
		f.updateProtocolSet = true
		protocol = *input.Protocol
	}
	return provider.Provider{ID: input.ID, OrganizationID: uuid.Nil, Endpoint: "https://example.com", AuthMethod: authMethod, Protocol: protocol, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeProviderStore) Delete(ctx context.Context, id uuid.UUID) error {
	f.deleteID = id
	return nil
}

func (f *fakeProviderStore) List(ctx context.Context, organizationID uuid.UUID, pageSize int32, cursor *provider.PageCursor) (provider.ListResult, error) {
	f.listOrganizationID = organizationID
	return provider.ListResult{Providers: []provider.Provider{}}, nil
}

type fakeModelStore struct {
	createOrganizationID uuid.UUID
	listOrganizationID   uuid.UUID
	getID                uuid.UUID
	getModel             model.Model
	updateID             uuid.UUID
	deleteID             uuid.UUID
}

func (f *fakeModelStore) Create(ctx context.Context, input model.CreateInput) (model.Model, error) {
	f.createOrganizationID = input.OrganizationID
	return model.Model{ID: uuid.MustParse("1bb21ea2-03c8-453b-a0ef-c4a12f0f8f2a"), OrganizationID: input.OrganizationID, Name: input.Name, ProviderID: input.ProviderID, RemoteName: input.RemoteName, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Get(ctx context.Context, id uuid.UUID) (model.Model, error) {
	f.getID = id
	if f.getModel.ID != uuid.Nil || f.getModel.ProviderID != uuid.Nil || f.getModel.RemoteName != "" || f.getModel.OrganizationID != uuid.Nil {
		mdl := f.getModel
		if mdl.ID == uuid.Nil {
			mdl.ID = id
		}
		return mdl, nil
	}
	return model.Model{ID: id, OrganizationID: uuid.Nil, Name: "model", ProviderID: uuid.MustParse("3ef53c23-7d5e-4ca8-8d1a-5df6cbdedce2"), RemoteName: "remote", CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Update(ctx context.Context, input model.UpdateInput) (model.Model, error) {
	f.updateID = input.ID
	return model.Model{ID: input.ID, OrganizationID: uuid.Nil, Name: "model", ProviderID: uuid.MustParse("3ef53c23-7d5e-4ca8-8d1a-5df6cbdedce2"), RemoteName: "remote", CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Delete(ctx context.Context, id uuid.UUID) error {
	f.deleteID = id
	return nil
}

func (f *fakeModelStore) List(ctx context.Context, organizationID uuid.UUID, filter model.ListFilter, pageSize int32, cursor *model.PageCursor) (model.ListResult, error) {
	f.listOrganizationID = organizationID
	return model.ListResult{Models: []model.Model{}}, nil
}

func contextWithIdentity() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		identity.MetadataKeyIdentityID, "identity-1",
		identity.MetadataKeyIdentityType, "test",
	))
}

func TestCreateLLMProviderUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("f79b0bde-9e46-44c0-9756-9eac9f383acd")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})

	_, err := server.CreateLLMProvider(context.Background(), &llmv1.CreateLLMProviderRequest{
		Endpoint:       "https://example.com",
		Token:          "token",
		AuthMethod:     llmv1.AuthMethod_AUTH_METHOD_BEARER,
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("CreateLLMProvider: %v", err)
	}
	if providers.createOrganizationID != organizationID {
		t.Fatalf("expected organization %s, got %s", organizationID, providers.createOrganizationID)
	}
	if providers.createAuthMethod != provider.AuthMethodBearer {
		t.Fatalf("expected auth method %s, got %s", provider.AuthMethodBearer, providers.createAuthMethod)
	}
	if providers.createProtocol != provider.ProtocolResponses {
		t.Fatalf("expected protocol %s, got %s", provider.ProtocolResponses, providers.createProtocol)
	}
}

func TestCreateLLMProviderSupportsXAPIKey(t *testing.T) {
	organizationID := uuid.MustParse("83eb6de8-289b-4b25-8752-23d73bb2e346")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})
	protocol := llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES

	_, err := server.CreateLLMProvider(context.Background(), &llmv1.CreateLLMProviderRequest{
		Endpoint:       "https://example.com",
		Token:          "token",
		AuthMethod:     llmv1.AuthMethod_AUTH_METHOD_X_API_KEY,
		Protocol:       protocol,
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("CreateLLMProvider: %v", err)
	}
	if providers.createAuthMethod != provider.AuthMethodXAPIKey {
		t.Fatalf("expected auth method %s, got %s", provider.AuthMethodXAPIKey, providers.createAuthMethod)
	}
	if providers.createProtocol != provider.ProtocolAnthropicMessages {
		t.Fatalf("expected protocol %s, got %s", provider.ProtocolAnthropicMessages, providers.createProtocol)
	}
}

func TestGetLLMProviderUsesID(t *testing.T) {
	providerID := uuid.MustParse("b2910fd9-9f3b-4f31-9c3b-369b3a2040e0")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})

	_, err := server.GetLLMProvider(contextWithIdentity(), &llmv1.GetLLMProviderRequest{Id: providerID.String()})
	if err != nil {
		t.Fatalf("GetLLMProvider: %v", err)
	}
	if providers.getID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, providers.getID)
	}
}

func TestUpdateLLMProviderUsesID(t *testing.T) {
	providerID := uuid.MustParse("56a4b7e4-3954-4f85-9df2-b0b2371c91f8")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})
	endpoint := "https://update.example.com"
	protocol := llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES
	authMethod := llmv1.AuthMethod_AUTH_METHOD_X_API_KEY

	_, err := server.UpdateLLMProvider(contextWithIdentity(), &llmv1.UpdateLLMProviderRequest{
		Id:         providerID.String(),
		Endpoint:   &endpoint,
		Protocol:   &protocol,
		AuthMethod: &authMethod,
	})
	if err != nil {
		t.Fatalf("UpdateLLMProvider: %v", err)
	}
	if providers.updateID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, providers.updateID)
	}
	if !providers.updateAuthMethodSet || providers.updateAuthMethod != provider.AuthMethodXAPIKey {
		t.Fatalf("expected auth method %s, got %s", provider.AuthMethodXAPIKey, providers.updateAuthMethod)
	}
	if !providers.updateProtocolSet || providers.updateProtocol != provider.ProtocolAnthropicMessages {
		t.Fatalf("expected protocol %s, got %s", provider.ProtocolAnthropicMessages, providers.updateProtocol)
	}
}

func TestDeleteLLMProviderUsesID(t *testing.T) {
	providerID := uuid.MustParse("0ea4b5b9-7fe0-4e1f-b1a1-a4b9f71001d5")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})

	_, err := server.DeleteLLMProvider(contextWithIdentity(), &llmv1.DeleteLLMProviderRequest{Id: providerID.String()})
	if err != nil {
		t.Fatalf("DeleteLLMProvider: %v", err)
	}
	if providers.deleteID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, providers.deleteID)
	}
}

func TestListLLMProvidersUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("22cb0675-4c69-4aa6-a41f-44cde1d2b0f4")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{})

	_, err := server.ListLLMProviders(context.Background(), &llmv1.ListLLMProvidersRequest{PageSize: 5, OrganizationId: organizationID.String()})
	if err != nil {
		t.Fatalf("ListLLMProviders: %v", err)
	}
	if providers.listOrganizationID != organizationID {
		t.Fatalf("expected organization %s, got %s", organizationID, providers.listOrganizationID)
	}
}

func TestCreateModelUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("6d68256e-4f2a-4d20-9f2b-8e93b5c09a4b")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models)

	_, err := server.CreateModel(context.Background(), &llmv1.CreateModelRequest{
		Name:           "name",
		LlmProviderId:  uuid.MustParse("b89fc3c9-0e60-4a74-84c0-c4f1d26c1ee1").String(),
		RemoteName:     "remote",
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if models.createOrganizationID != organizationID {
		t.Fatalf("expected organization %s, got %s", organizationID, models.createOrganizationID)
	}
}

func TestGetModelUsesID(t *testing.T) {
	modelID := uuid.MustParse("64b7b95d-1d57-4a95-8a13-2fdc7d4e5408")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models)

	_, err := server.GetModel(contextWithIdentity(), &llmv1.GetModelRequest{Id: modelID.String()})
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if models.getID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.getID)
	}
}

func TestUpdateModelUsesID(t *testing.T) {
	modelID := uuid.MustParse("534d5726-ecb8-4d47-967b-8a337df56c52")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models)
	name := "updated"

	_, err := server.UpdateModel(contextWithIdentity(), &llmv1.UpdateModelRequest{
		Id:   modelID.String(),
		Name: &name,
	})
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if models.updateID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.updateID)
	}
}

func TestDeleteModelUsesID(t *testing.T) {
	modelID := uuid.MustParse("da8c9c3a-8791-4d9f-a73f-83f31138dc28")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models)

	_, err := server.DeleteModel(contextWithIdentity(), &llmv1.DeleteModelRequest{Id: modelID.String()})
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if models.deleteID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.deleteID)
	}
}

func TestListModelsUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("0f42fd48-4d3e-4382-b787-6e681d8b82a0")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models)

	_, err := server.ListModels(context.Background(), &llmv1.ListModelsRequest{PageSize: 5, OrganizationId: organizationID.String()})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if models.listOrganizationID != organizationID {
		t.Fatalf("expected organization %s, got %s", organizationID, models.listOrganizationID)
	}
}

func TestResolveModelReturnsProviderDetails(t *testing.T) {
	modelID := uuid.MustParse("6e1a1a68-1e0f-4b07-8362-0a22e4c6bb86")
	providerID := uuid.MustParse("3a6f3fb1-6372-4c30-bf34-0ff24511d9c0")
	organizationID := uuid.MustParse("d65d0b42-33a1-45ac-a025-78f9117c3468")
	providers := &fakeProviderStore{
		getWithTokenOrgID:    organizationID,
		getWithTokenAuth:     provider.AuthMethodXAPIKey,
		getWithTokenProtocol: provider.ProtocolAnthropicMessages,
	}
	models := &fakeModelStore{getModel: model.Model{ProviderID: providerID, RemoteName: "remote", OrganizationID: organizationID, Name: "model"}}
	server := New(providers, models)

	resp, err := server.ResolveModel(context.Background(), &llmv1.ResolveModelRequest{ModelId: modelID.String()})
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if models.getID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.getID)
	}
	if providers.getWithTokenID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, providers.getWithTokenID)
	}
	if resp.Endpoint != "https://example.com" {
		t.Fatalf("expected endpoint https://example.com, got %s", resp.Endpoint)
	}
	if resp.Token != "token" {
		t.Fatalf("expected token token, got %s", resp.Token)
	}
	if resp.RemoteName != "remote" {
		t.Fatalf("expected remote name remote, got %s", resp.RemoteName)
	}
	if resp.OrganizationId != organizationID.String() {
		t.Fatalf("expected organization %s, got %s", organizationID, resp.OrganizationId)
	}
	if resp.AuthMethod != llmv1.AuthMethod_AUTH_METHOD_X_API_KEY {
		t.Fatalf("expected auth method %v, got %v", llmv1.AuthMethod_AUTH_METHOD_X_API_KEY, resp.AuthMethod)
	}
	if resp.Protocol != llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES {
		t.Fatalf("expected protocol %v, got %v", llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES, resp.Protocol)
	}
}
