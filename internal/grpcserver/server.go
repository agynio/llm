package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	llmv1 "github.com/agynio/llm/gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/agynio/llm/internal/proxy"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ProviderStore interface {
	Create(ctx context.Context, input provider.CreateInput) (provider.Provider, error)
	Get(ctx context.Context, id uuid.UUID) (provider.Provider, error)
	Update(ctx context.Context, input provider.UpdateInput) (provider.Provider, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, pageSize int32, cursor *provider.PageCursor) (provider.ListResult, error)
}

type ModelStore interface {
	Create(ctx context.Context, input model.CreateInput) (model.Model, error)
	Get(ctx context.Context, id uuid.UUID) (model.Model, error)
	Update(ctx context.Context, input model.UpdateInput) (model.Model, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, filter model.ListFilter, pageSize int32, cursor *model.PageCursor) (model.ListResult, error)
}

type Proxy interface {
	CreateResponse(ctx context.Context, modelID uuid.UUID, body []byte) (proxy.Response, error)
	CreateResponseStream(ctx context.Context, modelID uuid.UUID, body []byte) (proxy.StreamResponse, error)
}

type Server struct {
	llmv1.UnimplementedLLMServiceServer
	providers ProviderStore
	models    ModelStore
	proxy     Proxy
}

func New(providers ProviderStore, models ModelStore, proxyClient Proxy) *Server {
	return &Server{providers: providers, models: models, proxy: proxyClient}
}

func (s *Server) CreateLLMProvider(ctx context.Context, req *llmv1.CreateLLMProviderRequest) (*llmv1.CreateLLMProviderResponse, error) {
	endpoint := strings.TrimSpace(req.GetEndpoint())
	if endpoint == "" {
		return nil, status.Error(codes.InvalidArgument, "endpoint is required")
	}
	token := strings.TrimSpace(req.GetToken())
	if token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	authMethod, err := parseAuthMethod(req.GetAuthMethod(), true)
	if err != nil {
		return nil, err
	}

	created, err := s.providers.Create(ctx, provider.CreateInput{
		Endpoint:   endpoint,
		AuthMethod: authMethod,
		Token:      token,
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.CreateLLMProviderResponse{Provider: toProtoProvider(created)}, nil
}

func (s *Server) GetLLMProvider(ctx context.Context, req *llmv1.GetLLMProviderRequest) (*llmv1.GetLLMProviderResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	prov, err := s.providers.Get(ctx, id)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &llmv1.GetLLMProviderResponse{Provider: toProtoProvider(prov)}, nil
}

func (s *Server) UpdateLLMProvider(ctx context.Context, req *llmv1.UpdateLLMProviderRequest) (*llmv1.UpdateLLMProviderResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}

	input := provider.UpdateInput{ID: id}
	if req.Endpoint != nil {
		endpoint := strings.TrimSpace(req.GetEndpoint())
		if endpoint == "" {
			return nil, status.Error(codes.InvalidArgument, "endpoint cannot be empty")
		}
		input.Endpoint = &endpoint
	}
	if req.AuthMethod != nil {
		method, err := parseAuthMethod(req.GetAuthMethod(), false)
		if err != nil {
			return nil, err
		}
		input.AuthMethod = &method
	}
	if req.Token != nil {
		token := strings.TrimSpace(req.GetToken())
		if token == "" {
			return nil, status.Error(codes.InvalidArgument, "token cannot be empty")
		}
		input.Token = &token
	}

	updated, err := s.providers.Update(ctx, input)
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.UpdateLLMProviderResponse{Provider: toProtoProvider(updated)}, nil
}

func (s *Server) DeleteLLMProvider(ctx context.Context, req *llmv1.DeleteLLMProviderRequest) (*llmv1.DeleteLLMProviderResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	if err := s.providers.Delete(ctx, id); err != nil {
		return nil, toStatusError(err)
	}
	return &llmv1.DeleteLLMProviderResponse{}, nil
}

func (s *Server) ListLLMProviders(ctx context.Context, req *llmv1.ListLLMProvidersRequest) (*llmv1.ListLLMProvidersResponse, error) {
	var cursor *provider.PageCursor
	if req.GetPageToken() != "" {
		decoded, err := provider.DecodePageToken(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		cursor = &decoded
	}

	result, err := s.providers.List(ctx, req.GetPageSize(), cursor)
	if err != nil {
		return nil, toStatusError(err)
	}

	providers := make([]*llmv1.LLMProvider, 0, len(result.Providers))
	for _, prov := range result.Providers {
		providers = append(providers, toProtoProvider(prov))
	}

	resp := &llmv1.ListLLMProvidersResponse{Providers: providers}
	if result.NextCursor != nil {
		token, err := provider.EncodePageToken(*result.NextCursor)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to encode page token")
		}
		resp.NextPageToken = token
	}

	return resp, nil
}

func (s *Server) CreateModel(ctx context.Context, req *llmv1.CreateModelRequest) (*llmv1.CreateModelResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	providerID, err := parseUUID(req.GetLlmProviderId(), "llm_provider_id")
	if err != nil {
		return nil, err
	}
	remoteName := strings.TrimSpace(req.GetRemoteName())
	if remoteName == "" {
		return nil, status.Error(codes.InvalidArgument, "remote_name is required")
	}

	created, err := s.models.Create(ctx, model.CreateInput{
		Name:       name,
		ProviderID: providerID,
		RemoteName: remoteName,
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.CreateModelResponse{Model: toProtoModel(created)}, nil
}

func (s *Server) GetModel(ctx context.Context, req *llmv1.GetModelRequest) (*llmv1.GetModelResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	mdl, err := s.models.Get(ctx, id)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &llmv1.GetModelResponse{Model: toProtoModel(mdl)}, nil
}

func (s *Server) UpdateModel(ctx context.Context, req *llmv1.UpdateModelRequest) (*llmv1.UpdateModelResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	input := model.UpdateInput{ID: id}

	if req.Name != nil {
		name := strings.TrimSpace(req.GetName())
		if name == "" {
			return nil, status.Error(codes.InvalidArgument, "name cannot be empty")
		}
		input.Name = &name
	}
	if req.LlmProviderId != nil {
		providerID, err := parseUUID(req.GetLlmProviderId(), "llm_provider_id")
		if err != nil {
			return nil, err
		}
		input.ProviderID = &providerID
	}
	if req.RemoteName != nil {
		remoteName := strings.TrimSpace(req.GetRemoteName())
		if remoteName == "" {
			return nil, status.Error(codes.InvalidArgument, "remote_name cannot be empty")
		}
		input.RemoteName = &remoteName
	}

	updated, err := s.models.Update(ctx, input)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &llmv1.UpdateModelResponse{Model: toProtoModel(updated)}, nil
}

func (s *Server) DeleteModel(ctx context.Context, req *llmv1.DeleteModelRequest) (*llmv1.DeleteModelResponse, error) {
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	if err := s.models.Delete(ctx, id); err != nil {
		return nil, toStatusError(err)
	}
	return &llmv1.DeleteModelResponse{}, nil
}

func (s *Server) ListModels(ctx context.Context, req *llmv1.ListModelsRequest) (*llmv1.ListModelsResponse, error) {
	filter := model.ListFilter{}
	if req.GetLlmProviderId() != "" {
		providerID, err := parseUUID(req.GetLlmProviderId(), "llm_provider_id")
		if err != nil {
			return nil, err
		}
		filter.ProviderID = &providerID
	}

	var cursor *model.PageCursor
	if req.GetPageToken() != "" {
		decoded, tokenProviderID, err := model.DecodePageToken(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		cursor = &decoded
		if tokenProviderID != nil {
			if filter.ProviderID == nil {
				filter.ProviderID = tokenProviderID
			} else if *filter.ProviderID != *tokenProviderID {
				return nil, status.Error(codes.InvalidArgument, "page_token does not match llm_provider_id")
			}
		}
	}

	result, err := s.models.List(ctx, filter, req.GetPageSize(), cursor)
	if err != nil {
		return nil, toStatusError(err)
	}

	models := make([]*llmv1.Model, 0, len(result.Models))
	for _, mdl := range result.Models {
		models = append(models, toProtoModel(mdl))
	}

	resp := &llmv1.ListModelsResponse{Models: models}
	if result.NextCursor != nil {
		token, err := model.EncodePageToken(*result.NextCursor, filter.ProviderID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to encode page token")
		}
		resp.NextPageToken = token
	}

	return resp, nil
}

func (s *Server) CreateResponse(ctx context.Context, req *llmv1.CreateResponseRequest) (*llmv1.CreateResponseResponse, error) {
	modelID, err := parseUUID(req.GetModelId(), "model_id")
	if err != nil {
		return nil, err
	}
	if len(req.GetBody()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "body is required")
	}

	resp, err := s.proxy.CreateResponse(ctx, modelID, req.GetBody())
	if err != nil {
		return nil, toStatusError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, status.Errorf(codes.Internal, "provider response status %d: %s", resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	return &llmv1.CreateResponseResponse{Body: resp.Body}, nil
}

func (s *Server) CreateResponseStream(req *llmv1.CreateResponseStreamRequest, stream llmv1.LLMService_CreateResponseStreamServer) error {
	modelID, err := parseUUID(req.GetModelId(), "model_id")
	if err != nil {
		return err
	}
	if len(req.GetBody()) == 0 {
		return status.Error(codes.InvalidArgument, "body is required")
	}

	resp, err := s.proxy.CreateResponseStream(stream.Context(), modelID, req.GetBody())
	if err != nil {
		return toStatusError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return status.Errorf(codes.Internal, "provider response status %d", resp.StatusCode)
		}
		return status.Errorf(codes.Internal, "provider response status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return proxy.ReadSSE(stream.Context(), resp.Body, func(event proxy.Event) error {
		return stream.Send(&llmv1.CreateResponseStreamResponse{
			EventType: event.EventType,
			Data:      event.Data,
		})
	})
}

func parseUUID(value string, field string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "%s is required", field)
	}
	id, err := uuid.Parse(trimmed)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "%s must be a valid UUID", field)
	}
	return id, nil
}

func parseAuthMethod(value llmv1.AuthMethod, allowUnspecified bool) (provider.AuthMethod, error) {
	switch value {
	case llmv1.AuthMethod_AUTH_METHOD_BEARER:
		return provider.AuthMethodBearer, nil
	case llmv1.AuthMethod_AUTH_METHOD_UNSPECIFIED:
		if allowUnspecified {
			return provider.AuthMethodBearer, nil
		}
		return "", status.Error(codes.InvalidArgument, "auth_method must be set")
	default:
		return "", status.Error(codes.InvalidArgument, "auth_method is invalid")
	}
}

func toProtoProvider(prov provider.Provider) *llmv1.LLMProvider {
	return &llmv1.LLMProvider{
		Meta: &llmv1.EntityMeta{
			Id:        prov.ID.String(),
			CreatedAt: timestamppb.New(prov.CreatedAt),
			UpdatedAt: timestamppb.New(prov.UpdatedAt),
		},
		Endpoint:   prov.Endpoint,
		AuthMethod: toProtoAuthMethod(prov.AuthMethod),
	}
}

func toProtoModel(mdl model.Model) *llmv1.Model {
	return &llmv1.Model{
		Meta: &llmv1.EntityMeta{
			Id:        mdl.ID.String(),
			CreatedAt: timestamppb.New(mdl.CreatedAt),
			UpdatedAt: timestamppb.New(mdl.UpdatedAt),
		},
		Name:          mdl.Name,
		LlmProviderId: mdl.ProviderID.String(),
		RemoteName:    mdl.RemoteName,
	}
}

func toProtoAuthMethod(method provider.AuthMethod) llmv1.AuthMethod {
	switch method {
	case provider.AuthMethodBearer:
		return llmv1.AuthMethod_AUTH_METHOD_BEARER
	default:
		panic(fmt.Sprintf("unexpected auth method: %q", method))
	}
}

func toStatusError(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, provider.ErrProviderNotFound) || errors.Is(err, model.ErrModelNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, provider.ErrNoFieldsToUpdate) || errors.Is(err, model.ErrNoFieldsToUpdate) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if errors.Is(err, provider.ErrProviderInUse) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if errors.Is(err, proxy.ErrInvalidBody) || errors.Is(err, proxy.ErrMissingModel) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if errors.Is(err, proxy.ErrUnsupportedAuthMethod) || errors.Is(err, proxy.ErrMissingToken) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
