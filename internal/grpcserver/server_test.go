package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authorizationv1 "github.com/agynio/llm/.gen/go/agynio/api/authorization/v1"
	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
	"google.golang.org/grpc"
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
	getWithTokenEndpoint string
	getWithTokenToken    string
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
	endpoint := f.getWithTokenEndpoint
	if endpoint == "" {
		endpoint = "https://example.com"
	}
	token := f.getWithTokenToken
	if token == "" {
		token = "token"
	}
	return provider.ProviderWithToken{
		Provider: provider.Provider{
			ID:             id,
			OrganizationID: f.getWithTokenOrgID,
			Endpoint:       endpoint,
			AuthMethod:     f.getWithTokenAuth,
			Protocol:       f.getWithTokenProtocol,
			CreatedAt:      time.Unix(0, 0),
			UpdatedAt:      time.Unix(0, 0),
		},
		Token: token,
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

type fakeAuthorizationClient struct {
	checkFn       func(ctx context.Context, req *authorizationv1.CheckRequest) (*authorizationv1.CheckResponse, error)
	writeFn       func(ctx context.Context, req *authorizationv1.WriteRequest) (*authorizationv1.WriteResponse, error)
	checkRequests []*authorizationv1.CheckRequest
	writeRequests []*authorizationv1.WriteRequest
}

func (f *fakeAuthorizationClient) Check(ctx context.Context, req *authorizationv1.CheckRequest, _ ...grpc.CallOption) (*authorizationv1.CheckResponse, error) {
	f.checkRequests = append(f.checkRequests, req)
	if f.checkFn != nil {
		return f.checkFn(ctx, req)
	}
	return &authorizationv1.CheckResponse{Allowed: true}, nil
}

func (f *fakeAuthorizationClient) Write(ctx context.Context, req *authorizationv1.WriteRequest, _ ...grpc.CallOption) (*authorizationv1.WriteResponse, error) {
	f.writeRequests = append(f.writeRequests, req)
	if f.writeFn != nil {
		return f.writeFn(ctx, req)
	}
	return &authorizationv1.WriteResponse{}, nil
}

func contextWithIdentity() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		identity.MetadataKeyIdentityID, "identity-1",
		identity.MetadataKeyIdentityType, "test",
	))
}

func newTestServer(providers ProviderStore, models ModelStore) *Server {
	return New(providers, models, &fakeAuthorizationClient{}, http.DefaultClient)
}

func TestCreateLLMProviderUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("f79b0bde-9e46-44c0-9756-9eac9f383acd")
	providers := &fakeProviderStore{}
	server := newTestServer(providers, &fakeModelStore{})

	_, err := server.CreateLLMProvider(contextWithIdentity(), &llmv1.CreateLLMProviderRequest{
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

func TestCreateLLMProviderDefaultsProtocol(t *testing.T) {
	organizationID := uuid.MustParse("6a8262f5-64f3-4c19-8215-8c0275733b39")
	providers := &fakeProviderStore{}
	server := newTestServer(providers, &fakeModelStore{})

	_, err := server.CreateLLMProvider(contextWithIdentity(), &llmv1.CreateLLMProviderRequest{
		Endpoint:       "https://example.com",
		Token:          "token",
		AuthMethod:     llmv1.AuthMethod_AUTH_METHOD_BEARER,
		Protocol:       llmv1.Protocol_PROTOCOL_UNSPECIFIED,
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("CreateLLMProvider: %v", err)
	}
	if providers.createProtocol != provider.ProtocolResponses {
		t.Fatalf("expected protocol %s, got %s", provider.ProtocolResponses, providers.createProtocol)
	}
}

func TestCreateLLMProviderSupportsXAPIKey(t *testing.T) {
	organizationID := uuid.MustParse("83eb6de8-289b-4b25-8752-23d73bb2e346")
	providers := &fakeProviderStore{}
	server := newTestServer(providers, &fakeModelStore{})
	protocol := llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES

	_, err := server.CreateLLMProvider(contextWithIdentity(), &llmv1.CreateLLMProviderRequest{
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

func TestCreateLLMProviderAuthorizationDenied(t *testing.T) {
	organizationID := uuid.MustParse("6a34b377-3fbb-4dbf-91c4-b14925ac7d7c")
	providers := &fakeProviderStore{}
	authorization := &fakeAuthorizationClient{
		checkFn: func(_ context.Context, req *authorizationv1.CheckRequest) (*authorizationv1.CheckResponse, error) {
			if req.GetTupleKey().GetRelation() != "owner" {
				t.Fatalf("expected relation owner, got %s", req.GetTupleKey().GetRelation())
			}
			if req.GetTupleKey().GetObject() != "organization:"+organizationID.String() {
				t.Fatalf("unexpected object %s", req.GetTupleKey().GetObject())
			}
			return &authorizationv1.CheckResponse{Allowed: false}, nil
		},
	}
	server := New(providers, &fakeModelStore{}, authorization, http.DefaultClient)

	_, err := server.CreateLLMProvider(contextWithIdentity(), &llmv1.CreateLLMProviderRequest{
		Endpoint:       "https://example.com",
		Token:          "token",
		AuthMethod:     llmv1.AuthMethod_AUTH_METHOD_BEARER,
		OrganizationId: organizationID.String(),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", status.Code(err))
	}
	if providers.createOrganizationID != uuid.Nil {
		t.Fatalf("expected create not called")
	}
}

func TestGetLLMProviderUsesID(t *testing.T) {
	providerID := uuid.MustParse("b2910fd9-9f3b-4f31-9c3b-369b3a2040e0")
	providers := &fakeProviderStore{}
	server := newTestServer(providers, &fakeModelStore{})

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
	server := newTestServer(providers, &fakeModelStore{})
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
	server := newTestServer(providers, &fakeModelStore{})

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
	server := newTestServer(providers, &fakeModelStore{})

	_, err := server.ListLLMProviders(contextWithIdentity(), &llmv1.ListLLMProvidersRequest{PageSize: 5, OrganizationId: organizationID.String()})
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
	server := newTestServer(&fakeProviderStore{}, models)

	_, err := server.CreateModel(contextWithIdentity(), &llmv1.CreateModelRequest{
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

func TestCreateModelWritesAuthorizationTuple(t *testing.T) {
	organizationID := uuid.MustParse("9fd2468a-3387-48f0-9859-7eaf84f8b6d0")
	modelID := uuid.MustParse("1bb21ea2-03c8-453b-a0ef-c4a12f0f8f2a")
	models := &fakeModelStore{}
	authorization := &fakeAuthorizationClient{
		checkFn: func(_ context.Context, _ *authorizationv1.CheckRequest) (*authorizationv1.CheckResponse, error) {
			return &authorizationv1.CheckResponse{Allowed: true}, nil
		},
		writeFn: func(_ context.Context, req *authorizationv1.WriteRequest) (*authorizationv1.WriteResponse, error) {
			if len(req.GetWrites()) != 1 {
				t.Fatalf("expected 1 write, got %d", len(req.GetWrites()))
			}
			tuple := req.GetWrites()[0]
			if tuple.GetUser() != "organization:"+organizationID.String() {
				t.Fatalf("unexpected user %s", tuple.GetUser())
			}
			if tuple.GetRelation() != "org" {
				t.Fatalf("unexpected relation %s", tuple.GetRelation())
			}
			if tuple.GetObject() != "model:"+modelID.String() {
				t.Fatalf("unexpected object %s", tuple.GetObject())
			}
			return &authorizationv1.WriteResponse{}, nil
		},
	}
	server := New(&fakeProviderStore{}, models, authorization, http.DefaultClient)

	_, err := server.CreateModel(contextWithIdentity(), &llmv1.CreateModelRequest{
		Name:           "name",
		LlmProviderId:  uuid.MustParse("b89fc3c9-0e60-4a74-84c0-c4f1d26c1ee1").String(),
		RemoteName:     "remote",
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
}

func TestGetModelUsesID(t *testing.T) {
	modelID := uuid.MustParse("64b7b95d-1d57-4a95-8a13-2fdc7d4e5408")
	models := &fakeModelStore{}
	server := newTestServer(&fakeProviderStore{}, models)

	_, err := server.GetModel(contextWithIdentity(), &llmv1.GetModelRequest{Id: modelID.String()})
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if models.getID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.getID)
	}
}

func TestGetModelAuthorizationDenied(t *testing.T) {
	modelID := uuid.MustParse("66c03014-4709-480f-876a-f05cbaf3a1d7")
	organizationID := uuid.MustParse("c1f0da2f-cf7b-49ef-a821-1466dc1592a4")
	models := &fakeModelStore{getModel: model.Model{OrganizationID: organizationID}}
	authorization := &fakeAuthorizationClient{
		checkFn: func(_ context.Context, _ *authorizationv1.CheckRequest) (*authorizationv1.CheckResponse, error) {
			return &authorizationv1.CheckResponse{Allowed: false}, nil
		},
	}
	server := New(&fakeProviderStore{}, models, authorization, http.DefaultClient)

	_, err := server.GetModel(contextWithIdentity(), &llmv1.GetModelRequest{Id: modelID.String()})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", status.Code(err))
	}
}

func TestUpdateModelUsesID(t *testing.T) {
	modelID := uuid.MustParse("534d5726-ecb8-4d47-967b-8a337df56c52")
	models := &fakeModelStore{}
	server := newTestServer(&fakeProviderStore{}, models)
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
	server := newTestServer(&fakeProviderStore{}, models)

	_, err := server.DeleteModel(contextWithIdentity(), &llmv1.DeleteModelRequest{Id: modelID.String()})
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if models.deleteID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.deleteID)
	}
}

func TestDeleteModelDeletesAuthorizationTuple(t *testing.T) {
	modelID := uuid.MustParse("0d0243f7-7eab-49f6-8a57-bfeec466e28f")
	organizationID := uuid.MustParse("d19a2438-ea47-4b6e-8c62-a58f71c78c9c")
	models := &fakeModelStore{getModel: model.Model{ID: modelID, OrganizationID: organizationID}}
	authorization := &fakeAuthorizationClient{
		checkFn: func(_ context.Context, _ *authorizationv1.CheckRequest) (*authorizationv1.CheckResponse, error) {
			return &authorizationv1.CheckResponse{Allowed: true}, nil
		},
		writeFn: func(_ context.Context, req *authorizationv1.WriteRequest) (*authorizationv1.WriteResponse, error) {
			if len(req.GetDeletes()) != 1 {
				t.Fatalf("expected 1 delete, got %d", len(req.GetDeletes()))
			}
			tuple := req.GetDeletes()[0]
			if tuple.GetUser() != "organization:"+organizationID.String() {
				t.Fatalf("unexpected user %s", tuple.GetUser())
			}
			if tuple.GetRelation() != "org" {
				t.Fatalf("unexpected relation %s", tuple.GetRelation())
			}
			if tuple.GetObject() != "model:"+modelID.String() {
				t.Fatalf("unexpected object %s", tuple.GetObject())
			}
			return &authorizationv1.WriteResponse{}, nil
		},
	}
	server := New(&fakeProviderStore{}, models, authorization, http.DefaultClient)

	_, err := server.DeleteModel(contextWithIdentity(), &llmv1.DeleteModelRequest{Id: modelID.String()})
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
}

func TestListModelsUsesOrganizationID(t *testing.T) {
	organizationID := uuid.MustParse("0f42fd48-4d3e-4382-b787-6e681d8b82a0")
	models := &fakeModelStore{}
	server := newTestServer(&fakeProviderStore{}, models)

	_, err := server.ListModels(contextWithIdentity(), &llmv1.ListModelsRequest{PageSize: 5, OrganizationId: organizationID.String()})
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
	server := newTestServer(providers, models)

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

func TestTestModelReturnsOutput(t *testing.T) {
	modelID := uuid.MustParse("7603bcce-803d-4caa-91c2-9c46c142a22b")
	providerID := uuid.MustParse("cd3a55ac-6bef-4d18-b0f1-7d41447ecfe0")
	handlerErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handlerErr <- errors.New("expected POST request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			handlerErr <- errors.New("authorization header missing")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			handlerErr <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload responsesRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			handlerErr <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "remote" {
			handlerErr <- errors.New("unexpected model")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Input != testModelPrompt {
			handlerErr <- errors.New("unexpected prompt")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"text":"ok"}]}]}`))
	}))
	t.Cleanup(server.Close)

	providers := &fakeProviderStore{
		getWithTokenEndpoint: server.URL,
		getWithTokenAuth:     provider.AuthMethodBearer,
		getWithTokenProtocol: provider.ProtocolResponses,
		getWithTokenToken:    "token",
	}
	models := &fakeModelStore{getModel: model.Model{ProviderID: providerID, RemoteName: "remote"}}
	service := New(providers, models, &fakeAuthorizationClient{}, server.Client())

	resp, err := service.TestModel(contextWithIdentity(), &llmv1.TestModelRequest{ModelId: modelID.String()})
	if err != nil {
		t.Fatalf("TestModel: %v", err)
	}
	if models.getID != modelID {
		t.Fatalf("expected model id %s, got %s", modelID, models.getID)
	}
	if providers.getWithTokenID != providerID {
		t.Fatalf("expected provider id %s, got %s", providerID, providers.getWithTokenID)
	}
	if resp.OutputText != "ok" {
		t.Fatalf("expected output ok, got %s", resp.OutputText)
	}
	select {
	case err := <-handlerErr:
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
	default:
	}
}

func TestParseAuthMethodXAPIKey(t *testing.T) {
	method, err := parseAuthMethod(llmv1.AuthMethod_AUTH_METHOD_X_API_KEY, false)
	if err != nil {
		t.Fatalf("parseAuthMethod: %v", err)
	}
	if method != provider.AuthMethodXAPIKey {
		t.Fatalf("expected auth method %s, got %s", provider.AuthMethodXAPIKey, method)
	}
}

func TestParseProtocol(t *testing.T) {
	cases := []struct {
		name             string
		value            llmv1.Protocol
		allowUnspecified bool
		want             provider.Protocol
		wantCode         codes.Code
	}{
		{
			name:             "responses",
			value:            llmv1.Protocol_PROTOCOL_RESPONSES,
			allowUnspecified: false,
			want:             provider.ProtocolResponses,
		},
		{
			name:             "anthropic messages",
			value:            llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES,
			allowUnspecified: false,
			want:             provider.ProtocolAnthropicMessages,
		},
		{
			name:             "unspecified allowed",
			value:            llmv1.Protocol_PROTOCOL_UNSPECIFIED,
			allowUnspecified: true,
			want:             provider.ProtocolResponses,
		},
		{
			name:             "unspecified rejected",
			value:            llmv1.Protocol_PROTOCOL_UNSPECIFIED,
			allowUnspecified: false,
			wantCode:         codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			protocol, err := parseProtocol(tc.value, tc.allowUnspecified)
			if tc.wantCode != codes.OK {
				if err == nil {
					t.Fatalf("expected error")
				}
				if status.Code(err) != tc.wantCode {
					t.Fatalf("expected code %v, got %v", tc.wantCode, status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProtocol: %v", err)
			}
			if protocol != tc.want {
				t.Fatalf("expected protocol %s, got %s", tc.want, protocol)
			}
		})
	}
}
