package grpcserver

import (
	"context"
	"errors"

	"log"
	"time"

	agentsv1 "github.com/agynio/llm/.gen/go/agynio/api/agents/v1"
	authorizationv1 "github.com/agynio/llm/.gen/go/agynio/api/authorization/v1"
	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	notificationsv1 "github.com/agynio/llm/.gen/go/agynio/api/notifications/v1"
	secretsv1 "github.com/agynio/llm/.gen/go/agynio/api/secrets/v1"
	"github.com/agynio/llm/internal/subscription"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SubscriptionStore interface {
	Create(ctx context.Context, input subscription.CreateInput) (subscription.Subscription, error)
	Get(ctx context.Context, id uuid.UUID) (subscription.Subscription, error)
	Update(ctx context.Context, input subscription.UpdateInput) (subscription.Subscription, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, organizationID uuid.UUID, pageSize int32, cursor *subscription.PageCursor) (subscription.ListResult, error)
	Attach(ctx context.Context, input subscription.AttachInput) (subscription.Attachment, error)
	Detach(ctx context.Context, id uuid.UUID) error
	GetAttachment(ctx context.Context, id uuid.UUID) (subscription.Attachment, error)
	ListAttachments(ctx context.Context, filter subscription.AttachmentFilter, pageSize int32, cursor *subscription.PageCursor) (subscription.AttachmentListResult, error)
	Resolve(ctx context.Context, agentID *uuid.UUID, environmentID uuid.UUID, vendor subscription.Vendor) (subscription.Subscription, error)
	CountReferencingSecret(ctx context.Context, secretID uuid.UUID) ([]uuid.UUID, error)
}

type secretsClient interface {
	ResolveSecret(ctx context.Context, in *secretsv1.ResolveSecretRequest, opts ...grpc.CallOption) (*secretsv1.ResolveSecretResponse, error)
	ResolveSecretExists(ctx context.Context, in *secretsv1.ResolveSecretExistsRequest, opts ...grpc.CallOption) (*secretsv1.ResolveSecretExistsResponse, error)
}

type agentsClient interface {
	GetEnvironment(ctx context.Context, in *agentsv1.GetEnvironmentRequest, opts ...grpc.CallOption) (*agentsv1.GetEnvironmentResponse, error)
}

type notificationsClient interface {
	Publish(ctx context.Context, in *notificationsv1.PublishRequest, opts ...grpc.CallOption) (*notificationsv1.PublishResponse, error)
}

func toProtoMeta(id uuid.UUID, createdAt, updatedAt time.Time) *llmv1.EntityMeta {
	return &llmv1.EntityMeta{
		Id:        id.String(),
		CreatedAt: timestamppb.New(createdAt),
		UpdatedAt: timestamppb.New(updatedAt),
	}
}

func isVendorAlreadyBound(err error) bool {
	return errors.Is(err, subscription.ErrVendorAlreadyBound)
}

// requireSecretExists validates the reference without reading the value: the
// token is resolved at binding time and never stored by this service.
func (s *Server) requireSecretExists(ctx context.Context, secretID uuid.UUID) error {
	if s.secrets == nil {
		return status.Error(codes.FailedPrecondition, "cannot verify the secret exists")
	}
	resp, err := s.secrets.ResolveSecretExists(ctx, &secretsv1.ResolveSecretExistsRequest{Id: secretID.String()})
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "cannot verify the secret exists: %v", err)
	}
	if !resp.GetExists() {
		return status.Errorf(codes.InvalidArgument, "secret %s does not exist", secretID)
	}
	return nil
}

func (s *Server) resolveSecretValue(ctx context.Context, secretID uuid.UUID) (string, error) {
	if s.secrets == nil {
		return "", status.Error(codes.FailedPrecondition, "secrets service is not configured")
	}
	resp, err := s.secrets.ResolveSecret(ctx, &secretsv1.ResolveSecretRequest{Id: secretID.String()})
	if err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "resolve subscription secret: %v", err)
	}
	return resp.GetValue(), nil
}

func (s *Server) environmentAllowedModels(ctx context.Context, environmentID uuid.UUID) ([]string, error) {
	if s.agents == nil {
		return nil, nil
	}
	resp, err := s.agents.GetEnvironment(ctx, &agentsv1.GetEnvironmentRequest{Id: environmentID.String()})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "read environment allowlist: %v", err)
	}
	return resp.GetEnvironment().GetLlmAllowedModels(), nil
}

// requireTargetEditable checks can_edit_config on the attachment's target. In
// native mode the attachment is the authorization, so this is the only point at
// which target rights are checked.
func (s *Server) requireTargetEditable(ctx context.Context, identityID string, organizationID uuid.UUID, agentID, environmentID *uuid.UUID) error {
	var object string
	switch {
	case agentID != nil:
		object = "agent:" + agentID.String()
	case environmentID != nil:
		object = "environment:" + environmentID.String()
	default:
		return status.Error(codes.InvalidArgument, "exactly one of agent_id or environment_id is required")
	}

	resp, err := s.authorization.Check(ctx, &authorizationv1.CheckRequest{
		TupleKey: &authorizationv1.TupleKey{
			User:     identityObjectPrefix + identityID,
			Relation: "can_edit_config",
			Object:   object,
		},
	})
	if err != nil {
		return status.Errorf(codes.Internal, "authorization check: %v", err)
	}
	if !resp.GetAllowed() {
		return status.Errorf(codes.PermissionDenied, "permission denied on %s", object)
	}

	// Cross-org guard: a target in another organization must not be attachable.
	orgResp, err := s.authorization.Check(ctx, &authorizationv1.CheckRequest{
		TupleKey: &authorizationv1.TupleKey{
			User:     organizationObjectPrefix + organizationID.String(),
			Relation: "org",
			Object:   object,
		},
	})
	if err != nil {
		return status.Errorf(codes.Internal, "authorization check: %v", err)
	}
	if !orgResp.GetAllowed() {
		return status.Errorf(codes.InvalidArgument, "%s belongs to a different organization", object)
	}
	return nil
}

const (
	notificationSource            = "llm"
	subscriptionUpdatedEvent      = "subscription.updated"
	subscriptionAttachmentUpdated = "subscription_attachment.updated"
	// A flat room alongside the organization's, mirroring the egress_rules room
	// the Egress Gateway subscribes to. Notification subscriptions are fixed at
	// request time, and the LLM Proxy cannot enumerate the organizations it will
	// serve, so it needs one room it can hold open for the process's life.
	subscriptionsRoom = "llm_subscriptions"
)

func organizationRoom(organizationID uuid.UUID) string {
	return "organization:" + organizationID.String()
}

func (s *Server) publishSubscriptionUpdated(ctx context.Context, organizationID, subscriptionID uuid.UUID, operation string) {
	s.publish(ctx, subscriptionUpdatedEvent, organizationID, map[string]any{
		"organization_id": organizationID.String(),
		"subscription_id": subscriptionID.String(),
		"operation":       operation,
	})
}

func (s *Server) publishAttachmentUpdated(ctx context.Context, organizationID, subscriptionID, attachmentID uuid.UUID, agentID, environmentID *uuid.UUID, operation string) {
	payload := map[string]any{
		"organization_id":            organizationID.String(),
		"subscription_id":            subscriptionID.String(),
		"subscription_attachment_id": attachmentID.String(),
		"operation":                  operation,
	}
	if agentID != nil {
		payload["agent_id"] = agentID.String()
	}
	if environmentID != nil {
		payload["environment_id"] = environmentID.String()
	}
	s.publish(ctx, subscriptionAttachmentUpdated, organizationID, payload)
}

// Fire-and-forget: a failed publish costs a stale binding until the connection
// closes, which is not worth failing the write the operator asked for.
func (s *Server) publish(ctx context.Context, event string, organizationID uuid.UUID, payload map[string]any) {
	if s.notifications == nil {
		return
	}
	structPayload, err := structpb.NewStruct(payload)
	if err != nil {
		log.Printf("llm: build %s payload: %v", event, err)
		return
	}
	if _, err := s.notifications.Publish(ctx, &notificationsv1.PublishRequest{
		Event:   event,
		Rooms:   []string{organizationRoom(organizationID), subscriptionsRoom},
		Payload: structPayload,
		Source:  notificationSource,
	}); err != nil {
		log.Printf("llm: publish %s: %v", event, err)
	}
}
