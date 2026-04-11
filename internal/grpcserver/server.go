package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ProviderStore interface {
	Create(ctx context.Context, input provider.CreateInput) (provider.Provider, error)
	Get(ctx context.Context, id uuid.UUID) (provider.Provider, error)
	GetWithToken(ctx context.Context, id uuid.UUID) (provider.ProviderWithToken, error)
	Update(ctx context.Context, input provider.UpdateInput) (provider.Provider, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, organizationID uuid.UUID, pageSize int32, cursor *provider.PageCursor) (provider.ListResult, error)
}

type ModelStore interface {
	Create(ctx context.Context, input model.CreateInput) (model.Model, error)
	Get(ctx context.Context, id uuid.UUID) (model.Model, error)
	Update(ctx context.Context, input model.UpdateInput) (model.Model, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, organizationID uuid.UUID, filter model.ListFilter, pageSize int32, cursor *model.PageCursor) (model.ListResult, error)
}

type Server struct {
	llmv1.UnimplementedLLMServiceServer
	providers ProviderStore
	models    ModelStore
}

func New(providers ProviderStore, models ModelStore) *Server {
	return &Server{providers: providers, models: models}
}

func (s *Server) CreateLLMProvider(ctx context.Context, req *llmv1.CreateLLMProviderRequest) (*llmv1.CreateLLMProviderResponse, error) {
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}

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
	protocol, err := parseProtocol(req.GetProtocol(), true)
	if err != nil {
		return nil, err
	}

	created, err := s.providers.Create(ctx, provider.CreateInput{
		OrganizationID: organizationID,
		Endpoint:       endpoint,
		AuthMethod:     authMethod,
		Protocol:       protocol,
		Token:          token,
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.CreateLLMProviderResponse{Provider: toProtoProvider(created)}, nil
}

func (s *Server) GetLLMProvider(ctx context.Context, req *llmv1.GetLLMProviderRequest) (*llmv1.GetLLMProviderResponse, error) {
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	if req.Protocol != nil {
		protocol, err := parseProtocol(req.GetProtocol(), false)
		if err != nil {
			return nil, err
		}
		input.Protocol = &protocol
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
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}

	var cursor *provider.PageCursor
	if req.GetPageToken() != "" {
		decoded, err := provider.DecodePageToken(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		cursor = &decoded
	}

	result, err := s.providers.List(ctx, organizationID, req.GetPageSize(), cursor)
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
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}

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
		OrganizationID: organizationID,
		Name:           name,
		ProviderID:     providerID,
		RemoteName:     remoteName,
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.CreateModelResponse{Model: toProtoModel(created)}, nil
}

func (s *Server) GetModel(ctx context.Context, req *llmv1.GetModelRequest) (*llmv1.GetModelResponse, error) {
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	if _, err := identity.FromContext(ctx); err != nil {
		return nil, err
	}

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
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}

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

	result, err := s.models.List(ctx, organizationID, filter, req.GetPageSize(), cursor)
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

func (s *Server) ResolveModel(ctx context.Context, req *llmv1.ResolveModelRequest) (*llmv1.ResolveModelResponse, error) {
	modelID, err := parseUUID(req.GetModelId(), "model_id")
	if err != nil {
		return nil, err
	}

	mdl, err := s.models.Get(ctx, modelID)
	if err != nil {
		return nil, toStatusError(err)
	}

	prov, err := s.providers.GetWithToken(ctx, mdl.ProviderID)
	if err != nil {
		return nil, toStatusError(err)
	}

	return &llmv1.ResolveModelResponse{
		Endpoint:       prov.Endpoint,
		Token:          prov.Token,
		RemoteName:     mdl.RemoteName,
		OrganizationId: prov.OrganizationID.String(),
		Protocol:       toProtoProtocol(prov.Protocol),
		AuthMethod:     toProtoAuthMethod(prov.AuthMethod),
	}, nil
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
	case llmv1.AuthMethod_AUTH_METHOD_X_API_KEY:
		return provider.AuthMethodXAPIKey, nil
	case llmv1.AuthMethod_AUTH_METHOD_UNSPECIFIED:
		if allowUnspecified {
			return provider.AuthMethodBearer, nil
		}
		return "", status.Error(codes.InvalidArgument, "auth_method must be set")
	default:
		return "", status.Error(codes.InvalidArgument, "auth_method is invalid")
	}
}

func parseProtocol(value llmv1.Protocol, allowUnspecified bool) (provider.Protocol, error) {
	switch value {
	case llmv1.Protocol_PROTOCOL_RESPONSES:
		return provider.ProtocolResponses, nil
	case llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES:
		return provider.ProtocolAnthropicMessages, nil
	case llmv1.Protocol_PROTOCOL_UNSPECIFIED:
		if allowUnspecified {
			return provider.ProtocolResponses, nil
		}
		return "", status.Error(codes.InvalidArgument, "protocol must be set")
	default:
		return "", status.Error(codes.InvalidArgument, "protocol is invalid")
	}
}

func toProtoProvider(prov provider.Provider) *llmv1.LLMProvider {
	return &llmv1.LLMProvider{
		Meta: &llmv1.EntityMeta{
			Id:        prov.ID.String(),
			CreatedAt: timestamppb.New(prov.CreatedAt),
			UpdatedAt: timestamppb.New(prov.UpdatedAt),
		},
		Endpoint:       prov.Endpoint,
		AuthMethod:     toProtoAuthMethod(prov.AuthMethod),
		OrganizationId: prov.OrganizationID.String(),
		Protocol:       toProtoProtocol(prov.Protocol),
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
	case provider.AuthMethodXAPIKey:
		return llmv1.AuthMethod_AUTH_METHOD_X_API_KEY
	default:
		panic(fmt.Sprintf("unexpected auth method: %q", method))
	}
}

func toProtoProtocol(protocol provider.Protocol) llmv1.Protocol {
	switch protocol {
	case provider.ProtocolResponses:
		return llmv1.Protocol_PROTOCOL_RESPONSES
	case provider.ProtocolAnthropicMessages:
		return llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES
	default:
		panic(fmt.Sprintf("unexpected protocol: %q", protocol))
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
	return status.Error(codes.Internal, err.Error())
}
