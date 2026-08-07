package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/subscription"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// vendorBinding is everything the platform must know about a vendor without
// configuration: what to intercept, where to send it, what to inject, and what
// the container's placeholder variable is called. The table lives here because
// this service is the only thing that reads it -- the proxy and the
// orchestrator are told, so neither carries a copy.
type vendorBinding struct {
	upstream       string
	protocol       llmv1.Protocol
	placeholderEnv string
	// shippable is false for a vendor whose credential cannot yet be delivered
	// to a workload; CreateSubscription refuses it rather than storing a record
	// that can never produce a working one.
	shippable bool
}

var vendorBindings = map[subscription.Vendor]vendorBinding{
	subscription.VendorClaude: {
		upstream:       "https://api.anthropic.com",
		protocol:       llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES,
		placeholderEnv: "CLAUDE_CODE_OAUTH_TOKEN",
		shippable:      true,
	},
	// The variable a Codex CLI reads (OPENAI_API_KEY) selects its API-key mode,
	// and a Codex CLI in API-key mode addresses api.openai.com rather than the
	// chatgpt.com this row intercepts. Its subscription credential lives in
	// ~/.codex/auth.json, and the placeholder mechanism delivers environment
	// variables only. The row records the intended binding; nothing can create
	// a record that would produce a working workload.
	subscription.VendorCodex: {
		upstream:  "https://chatgpt.com/backend-api/codex",
		protocol:  llmv1.Protocol_PROTOCOL_RESPONSES,
		shippable: false,
	},
}

func parseVendor(value llmv1.Vendor) (subscription.Vendor, error) {
	switch value {
	case llmv1.Vendor_VENDOR_CLAUDE:
		return subscription.VendorClaude, nil
	case llmv1.Vendor_VENDOR_CODEX:
		return subscription.VendorCodex, nil
	default:
		return "", status.Error(codes.InvalidArgument, "vendor must be claude or codex")
	}
}

func toProtoVendor(value subscription.Vendor) llmv1.Vendor {
	switch value {
	case subscription.VendorClaude:
		return llmv1.Vendor_VENDOR_CLAUDE
	case subscription.VendorCodex:
		return llmv1.Vendor_VENDOR_CODEX
	default:
		return llmv1.Vendor_VENDOR_UNSPECIFIED
	}
}

func toProtoSubscription(sub subscription.Subscription) *llmv1.Subscription {
	return &llmv1.Subscription{
		Meta:           toProtoMeta(sub.ID, sub.CreatedAt, sub.UpdatedAt),
		Name:           sub.Name,
		Vendor:         toProtoVendor(sub.Vendor),
		SecretId:       sub.SecretID.String(),
		AccountId:      sub.AccountID,
		OrganizationId: sub.OrganizationID.String(),
	}
}

func toProtoAttachment(a subscription.Attachment) *llmv1.SubscriptionAttachment {
	proto := &llmv1.SubscriptionAttachment{
		Meta:           toProtoMeta(a.ID, a.CreatedAt, a.CreatedAt),
		SubscriptionId: a.SubscriptionID.String(),
		Vendor:         toProtoVendor(a.Vendor),
		PlaceholderEnv: vendorBindings[a.Vendor].placeholderEnv,
	}
	if a.AgentID != nil {
		proto.Target = &llmv1.SubscriptionAttachment_AgentId{AgentId: a.AgentID.String()}
	} else if a.EnvironmentID != nil {
		proto.Target = &llmv1.SubscriptionAttachment_EnvironmentId{EnvironmentId: a.EnvironmentID.String()}
	}
	return proto
}

func (s *Server) CreateSubscription(ctx context.Context, req *llmv1.CreateSubscriptionRequest) (*llmv1.CreateSubscriptionResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}
	if err := s.requireOrgOwner(ctx, caller.IdentityID, organizationID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	vendor, err := parseVendor(req.GetVendor())
	if err != nil {
		return nil, err
	}
	binding := vendorBindings[vendor]
	if !binding.shippable {
		return nil, status.Errorf(codes.Unimplemented,
			"vendor %s has no placeholder credential the platform can deliver to a workload, so a subscription for it cannot produce a working workload", vendor)
	}
	secretID, err := parseUUID(req.GetSecretId(), "secret_id")
	if err != nil {
		return nil, err
	}
	// Existence only -- the value is resolved at binding time and never stored
	// by this service.
	if err := s.requireSecretExists(ctx, secretID); err != nil {
		return nil, err
	}

	created, err := s.subscriptions.Create(ctx, subscription.CreateInput{
		OrganizationID: organizationID,
		Name:           name,
		Vendor:         vendor,
		SecretID:       secretID,
		AccountID:      strings.TrimSpace(req.GetAccountId()),
	})
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	s.publishSubscriptionUpdated(ctx, organizationID, created.ID, "created")
	return &llmv1.CreateSubscriptionResponse{Subscription: toProtoSubscription(created)}, nil
}

func (s *Server) GetSubscription(ctx context.Context, req *llmv1.GetSubscriptionRequest) (*llmv1.GetSubscriptionResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	sub, err := s.subscriptions.Get(ctx, id)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if err := s.requireOrgMember(ctx, caller.IdentityID, sub.OrganizationID); err != nil {
		return nil, err
	}
	return &llmv1.GetSubscriptionResponse{Subscription: toProtoSubscription(sub)}, nil
}

func (s *Server) UpdateSubscription(ctx context.Context, req *llmv1.UpdateSubscriptionRequest) (*llmv1.UpdateSubscriptionResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	existing, err := s.subscriptions.Get(ctx, id)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if err := s.requireOrgOwner(ctx, caller.IdentityID, existing.OrganizationID); err != nil {
		return nil, err
	}

	input := subscription.UpdateInput{ID: id}
	if req.Name != nil {
		name := strings.TrimSpace(req.GetName())
		if name == "" {
			return nil, status.Error(codes.InvalidArgument, "name must not be empty")
		}
		input.Name = &name
	}
	if req.SecretId != nil {
		secretID, err := parseUUID(req.GetSecretId(), "secret_id")
		if err != nil {
			return nil, err
		}
		if err := s.requireSecretExists(ctx, secretID); err != nil {
			return nil, err
		}
		input.SecretID = &secretID
	}
	if req.AccountId != nil {
		accountID := strings.TrimSpace(req.GetAccountId())
		input.AccountID = &accountID
	}

	updated, err := s.subscriptions.Update(ctx, input)
	if err != nil {
		if errors.Is(err, subscription.ErrNoFieldsToUpdate) {
			return nil, status.Error(codes.InvalidArgument, "at least one field must be provided")
		}
		return nil, toSubscriptionStatusError(err)
	}
	// A rotated secret must stop the next call on an already-open connection.
	s.publishSubscriptionUpdated(ctx, updated.OrganizationID, updated.ID, "updated")
	return &llmv1.UpdateSubscriptionResponse{Subscription: toProtoSubscription(updated)}, nil
}

func (s *Server) DeleteSubscription(ctx context.Context, req *llmv1.DeleteSubscriptionRequest) (*llmv1.DeleteSubscriptionResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	existing, err := s.subscriptions.Get(ctx, id)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if err := s.requireOrgOwner(ctx, caller.IdentityID, existing.OrganizationID); err != nil {
		return nil, err
	}

	// Named rather than counted: an operator who has to go find them has been
	// told nothing useful.
	attachments, err := s.subscriptions.ListAttachments(ctx, subscription.AttachmentFilter{
		OrganizationID: existing.OrganizationID,
		SubscriptionID: &id,
	}, 0, nil)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if len(attachments.Attachments) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subscription is attached to %d target(s): %s",
			len(attachments.Attachments), strings.Join(attachmentTargets(attachments.Attachments), ", "))
	}

	if err := s.subscriptions.Delete(ctx, id); err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	s.publishSubscriptionUpdated(ctx, existing.OrganizationID, id, "deleted")
	return &llmv1.DeleteSubscriptionResponse{}, nil
}

func attachmentTargets(attachments []subscription.Attachment) []string {
	targets := make([]string, 0, len(attachments))
	for _, a := range attachments {
		switch {
		case a.AgentID != nil:
			targets = append(targets, "agent:"+a.AgentID.String())
		case a.EnvironmentID != nil:
			targets = append(targets, "environment:"+a.EnvironmentID.String())
		}
	}
	return targets
}

func (s *Server) ListSubscriptions(ctx context.Context, req *llmv1.ListSubscriptionsRequest) (*llmv1.ListSubscriptionsResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	organizationID, err := parseUUID(req.GetOrganizationId(), "organization_id")
	if err != nil {
		return nil, err
	}
	if err := s.requireOrgMember(ctx, caller.IdentityID, organizationID); err != nil {
		return nil, err
	}

	var cursor *subscription.PageCursor
	if token := strings.TrimSpace(req.GetPageToken()); token != "" {
		decoded, err := subscription.DecodePageToken(token)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "page_token is invalid")
		}
		cursor = &decoded
	}

	result, err := s.subscriptions.List(ctx, organizationID, req.GetPageSize(), cursor)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}

	resp := &llmv1.ListSubscriptionsResponse{
		Subscriptions: make([]*llmv1.Subscription, 0, len(result.Subscriptions)),
	}
	for _, sub := range result.Subscriptions {
		resp.Subscriptions = append(resp.Subscriptions, toProtoSubscription(sub))
	}
	if result.NextCursor != nil {
		token, err := subscription.EncodePageToken(*result.NextCursor)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode page token")
		}
		resp.NextPageToken = token
	}
	return resp, nil
}

func toSubscriptionStatusError(err error) error {
	switch {
	case errors.Is(err, subscription.ErrSubscriptionNotFound):
		return status.Error(codes.NotFound, "subscription not found")
	case errors.Is(err, subscription.ErrAttachmentNotFound):
		return status.Error(codes.NotFound, "subscription attachment not found")
	case errors.Is(err, subscription.ErrNameTaken):
		return status.Error(codes.AlreadyExists, "a subscription with that name already exists")
	case errors.Is(err, subscription.ErrVendorAlreadyBound):
		return status.Error(codes.AlreadyExists, "target already has a subscription for this vendor")
	case errors.Is(err, subscription.ErrSubscriptionInUse):
		return status.Error(codes.FailedPrecondition, "subscription is attached")
	default:
		return status.Error(codes.Internal, fmt.Sprintf("subscription store: %v", err))
	}
}

func uuidPtr(value string, field string) (*uuid.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := parseUUID(value, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
