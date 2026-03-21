package grpcserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/agynio/llm/internal/proxy"
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
		{name: "invalid body", err: proxy.ErrInvalidBody, code: codes.InvalidArgument},
		{name: "missing model", err: proxy.ErrMissingModel, code: codes.InvalidArgument},
		{name: "unsupported auth", err: proxy.ErrUnsupportedAuthMethod, code: codes.FailedPrecondition},
		{name: "missing token", err: proxy.ErrMissingToken, code: codes.FailedPrecondition},
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
	createTenantID uuid.UUID
	getTenantID    uuid.UUID
	updateTenantID uuid.UUID
	deleteTenantID uuid.UUID
	listTenantID   uuid.UUID
}

func (f *fakeProviderStore) Create(ctx context.Context, tenantID uuid.UUID, input provider.CreateInput) (provider.Provider, error) {
	f.createTenantID = tenantID
	return provider.Provider{
		ID:         uuid.MustParse("b0d2c70c-f2e7-4b33-94a1-d52468ed7ff1"),
		TenantID:   tenantID,
		Endpoint:   input.Endpoint,
		AuthMethod: input.AuthMethod,
		CreatedAt:  time.Unix(0, 0),
		UpdatedAt:  time.Unix(0, 0),
	}, nil
}

func (f *fakeProviderStore) Get(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) (provider.Provider, error) {
	f.getTenantID = tenantID
	return provider.Provider{ID: id, TenantID: tenantID, Endpoint: "https://example.com", AuthMethod: provider.AuthMethodBearer, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeProviderStore) Update(ctx context.Context, tenantID uuid.UUID, input provider.UpdateInput) (provider.Provider, error) {
	f.updateTenantID = tenantID
	return provider.Provider{ID: input.ID, TenantID: tenantID, Endpoint: "https://example.com", AuthMethod: provider.AuthMethodBearer, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeProviderStore) Delete(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) error {
	f.deleteTenantID = tenantID
	return nil
}

func (f *fakeProviderStore) List(ctx context.Context, tenantID uuid.UUID, pageSize int32, cursor *provider.PageCursor) (provider.ListResult, error) {
	f.listTenantID = tenantID
	return provider.ListResult{Providers: []provider.Provider{}}, nil
}

type fakeModelStore struct {
	createTenantID uuid.UUID
	getTenantID    uuid.UUID
	updateTenantID uuid.UUID
	deleteTenantID uuid.UUID
	listTenantID   uuid.UUID
}

func (f *fakeModelStore) Create(ctx context.Context, tenantID uuid.UUID, input model.CreateInput) (model.Model, error) {
	f.createTenantID = tenantID
	return model.Model{ID: uuid.MustParse("1bb21ea2-03c8-453b-a0ef-c4a12f0f8f2a"), TenantID: tenantID, Name: input.Name, ProviderID: input.ProviderID, RemoteName: input.RemoteName, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Get(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) (model.Model, error) {
	f.getTenantID = tenantID
	return model.Model{ID: id, TenantID: tenantID, Name: "model", ProviderID: uuid.MustParse("3ef53c23-7d5e-4ca8-8d1a-5df6cbdedce2"), RemoteName: "remote", CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Update(ctx context.Context, tenantID uuid.UUID, input model.UpdateInput) (model.Model, error) {
	f.updateTenantID = tenantID
	return model.Model{ID: input.ID, TenantID: tenantID, Name: "model", ProviderID: uuid.MustParse("3ef53c23-7d5e-4ca8-8d1a-5df6cbdedce2"), RemoteName: "remote", CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0)}, nil
}

func (f *fakeModelStore) Delete(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) error {
	f.deleteTenantID = tenantID
	return nil
}

func (f *fakeModelStore) List(ctx context.Context, tenantID uuid.UUID, filter model.ListFilter, pageSize int32, cursor *model.PageCursor) (model.ListResult, error) {
	f.listTenantID = tenantID
	return model.ListResult{Models: []model.Model{}}, nil
}

type fakeProxy struct {
	createTenantID       uuid.UUID
	createModelID        uuid.UUID
	createStreamTenantID uuid.UUID
	createStreamModelID  uuid.UUID
}

func (f *fakeProxy) CreateResponse(ctx context.Context, tenantID uuid.UUID, modelID uuid.UUID, body []byte) (proxy.Response, error) {
	f.createTenantID = tenantID
	f.createModelID = modelID
	return proxy.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: []byte("ok")}, nil
}

func (f *fakeProxy) CreateResponseStream(ctx context.Context, tenantID uuid.UUID, modelID uuid.UUID, body []byte) (proxy.StreamResponse, error) {
	f.createStreamTenantID = tenantID
	f.createStreamModelID = modelID
	return proxy.StreamResponse{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: hello\n\n"))}, nil
}

type fakeResponseStream struct {
	ctx    context.Context
	events []*llmv1.CreateResponseStreamResponse
}

func (f *fakeResponseStream) Send(resp *llmv1.CreateResponseStreamResponse) error {
	f.events = append(f.events, resp)
	return nil
}

func (f *fakeResponseStream) SetHeader(metadata.MD) error {
	return nil
}

func (f *fakeResponseStream) SendHeader(metadata.MD) error {
	return nil
}

func (f *fakeResponseStream) SetTrailer(metadata.MD) {}

func (f *fakeResponseStream) Context() context.Context {
	return f.ctx
}

func (f *fakeResponseStream) SendMsg(m any) error {
	return nil
}

func (f *fakeResponseStream) RecvMsg(m any) error {
	return nil
}

func contextWithTenant(tenantID uuid.UUID) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-agyn-tenant-id", tenantID.String(),
		"x-agyn-identity-id", "identity-1",
		"x-agyn-identity-type", "test",
		"x-agyn-auth-method", "unit",
	))
}

func TestCreateLLMProviderUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("f79b0bde-9e46-44c0-9756-9eac9f383acd")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{}, &fakeProxy{})

	_, err := server.CreateLLMProvider(contextWithTenant(tenantID), &llmv1.CreateLLMProviderRequest{
		Endpoint:   "https://example.com",
		Token:      "token",
		AuthMethod: llmv1.AuthMethod_AUTH_METHOD_BEARER,
	})
	if err != nil {
		t.Fatalf("CreateLLMProvider: %v", err)
	}
	if providers.createTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, providers.createTenantID)
	}
}

func TestGetLLMProviderUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("b2910fd9-9f3b-4f31-9c3b-369b3a2040e0")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{}, &fakeProxy{})

	_, err := server.GetLLMProvider(contextWithTenant(tenantID), &llmv1.GetLLMProviderRequest{Id: uuid.MustParse("8714de9a-915e-4e14-90e1-fb6bd3fdc03f").String()})
	if err != nil {
		t.Fatalf("GetLLMProvider: %v", err)
	}
	if providers.getTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, providers.getTenantID)
	}
}

func TestUpdateLLMProviderUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("56a4b7e4-3954-4f85-9df2-b0b2371c91f8")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{}, &fakeProxy{})
	endpoint := "https://update.example.com"

	_, err := server.UpdateLLMProvider(contextWithTenant(tenantID), &llmv1.UpdateLLMProviderRequest{
		Id:       uuid.MustParse("5d2416ab-6529-4709-9f88-300664c32bdc").String(),
		Endpoint: &endpoint,
	})
	if err != nil {
		t.Fatalf("UpdateLLMProvider: %v", err)
	}
	if providers.updateTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, providers.updateTenantID)
	}
}

func TestDeleteLLMProviderUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("0ea4b5b9-7fe0-4e1f-b1a1-a4b9f71001d5")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{}, &fakeProxy{})

	_, err := server.DeleteLLMProvider(contextWithTenant(tenantID), &llmv1.DeleteLLMProviderRequest{Id: uuid.MustParse("87c2127a-0f6f-4d91-a9ad-20af2ae1a571").String()})
	if err != nil {
		t.Fatalf("DeleteLLMProvider: %v", err)
	}
	if providers.deleteTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, providers.deleteTenantID)
	}
}

func TestListLLMProvidersUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("22cb0675-4c69-4aa6-a41f-44cde1d2b0f4")
	providers := &fakeProviderStore{}
	server := New(providers, &fakeModelStore{}, &fakeProxy{})

	_, err := server.ListLLMProviders(contextWithTenant(tenantID), &llmv1.ListLLMProvidersRequest{PageSize: 5})
	if err != nil {
		t.Fatalf("ListLLMProviders: %v", err)
	}
	if providers.listTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, providers.listTenantID)
	}
}

func TestCreateModelUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("6d68256e-4f2a-4d20-9f2b-8e93b5c09a4b")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models, &fakeProxy{})

	_, err := server.CreateModel(contextWithTenant(tenantID), &llmv1.CreateModelRequest{
		Name:          "name",
		LlmProviderId: uuid.MustParse("b89fc3c9-0e60-4a74-84c0-c4f1d26c1ee1").String(),
		RemoteName:    "remote",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if models.createTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, models.createTenantID)
	}
}

func TestGetModelUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("64b7b95d-1d57-4a95-8a13-2fdc7d4e5408")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models, &fakeProxy{})

	_, err := server.GetModel(contextWithTenant(tenantID), &llmv1.GetModelRequest{Id: uuid.MustParse("d8a2a8d4-02ef-4dd7-9154-4740d79c6f23").String()})
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if models.getTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, models.getTenantID)
	}
}

func TestUpdateModelUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("534d5726-ecb8-4d47-967b-8a337df56c52")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models, &fakeProxy{})
	name := "updated"

	_, err := server.UpdateModel(contextWithTenant(tenantID), &llmv1.UpdateModelRequest{
		Id:   uuid.MustParse("3c55d2fb-1e4d-4d48-b110-9b46c8d8fef0").String(),
		Name: &name,
	})
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if models.updateTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, models.updateTenantID)
	}
}

func TestDeleteModelUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("da8c9c3a-8791-4d9f-a73f-83f31138dc28")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models, &fakeProxy{})

	_, err := server.DeleteModel(contextWithTenant(tenantID), &llmv1.DeleteModelRequest{Id: uuid.MustParse("9e7262ad-9f74-4b08-91d0-1ee01b8d73d8").String()})
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if models.deleteTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, models.deleteTenantID)
	}
}

func TestListModelsUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("0f42fd48-4d3e-4382-b787-6e681d8b82a0")
	models := &fakeModelStore{}
	server := New(&fakeProviderStore{}, models, &fakeProxy{})

	_, err := server.ListModels(contextWithTenant(tenantID), &llmv1.ListModelsRequest{PageSize: 5})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if models.listTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, models.listTenantID)
	}
}

func TestCreateResponseUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("c5a128c7-34be-4f54-8849-b1380a8d3e75")
	proxyClient := &fakeProxy{}
	server := New(&fakeProviderStore{}, &fakeModelStore{}, proxyClient)
	modelID := uuid.MustParse("f5d90ff0-9f4a-4c75-a23c-770a041ce5f5")

	_, err := server.CreateResponse(contextWithTenant(tenantID), &llmv1.CreateResponseRequest{ModelId: modelID.String(), Body: []byte("body")})
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if proxyClient.createTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, proxyClient.createTenantID)
	}
}

func TestCreateResponseStreamUsesTenantID(t *testing.T) {
	tenantID := uuid.MustParse("6c15c0f3-5b50-4d99-95fb-44ffb3ce45d9")
	proxyClient := &fakeProxy{}
	server := New(&fakeProviderStore{}, &fakeModelStore{}, proxyClient)
	modelID := uuid.MustParse("64b0bc5b-9c5f-4174-8aa6-02f1aa9d4ad8")
	ctx := contextWithTenant(tenantID)
	stream := &fakeResponseStream{ctx: ctx}

	err := server.CreateResponseStream(&llmv1.CreateResponseStreamRequest{ModelId: modelID.String(), Body: []byte("body")}, stream)
	if err != nil {
		t.Fatalf("CreateResponseStream: %v", err)
	}
	if proxyClient.createStreamTenantID != tenantID {
		t.Fatalf("expected tenant %s, got %s", tenantID, proxyClient.createStreamTenantID)
	}
}
