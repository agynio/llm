package grpcserver

import (
	"context"
	"strings"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/subscription"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) CreateSubscriptionAttachment(ctx context.Context, req *llmv1.CreateSubscriptionAttachmentRequest) (*llmv1.CreateSubscriptionAttachmentResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	subscriptionID, err := parseUUID(req.GetSubscriptionId(), "subscription_id")
	if err != nil {
		return nil, err
	}
	sub, err := s.subscriptions.Get(ctx, subscriptionID)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if err := s.requireOrgOwner(ctx, caller.IdentityID, sub.OrganizationID); err != nil {
		return nil, err
	}

	agentID, err := uuidPtr(req.GetAgentId(), "agent_id")
	if err != nil {
		return nil, err
	}
	environmentID, err := uuidPtr(req.GetEnvironmentId(), "environment_id")
	if err != nil {
		return nil, err
	}
	if (agentID == nil) == (environmentID == nil) {
		return nil, status.Error(codes.InvalidArgument, "exactly one of agent_id or environment_id is required")
	}

	// Attaching requires edit rights on the target, and in native mode the
	// attachment is the authorization -- there is no second check at call time.
	if err := s.requireTargetEditable(ctx, caller.IdentityID, sub.OrganizationID, agentID, environmentID); err != nil {
		return nil, err
	}

	created, err := s.subscriptions.Attach(ctx, subscription.AttachInput{
		OrganizationID: sub.OrganizationID,
		SubscriptionID: subscriptionID,
		AgentID:        agentID,
		EnvironmentID:  environmentID,
	})
	if err != nil {
		return nil, s.describeVendorConflict(ctx, err, sub, agentID, environmentID)
	}
	s.publishAttachmentUpdated(ctx, sub.OrganizationID, subscriptionID, created.ID, agentID, environmentID, "created")
	return &llmv1.CreateSubscriptionAttachmentResponse{SubscriptionAttachment: toProtoAttachment(created)}, nil
}

// describeVendorConflict turns the unique-index violation into an error naming
// the subscription already bound, which is the thing the operator needs.
func (s *Server) describeVendorConflict(ctx context.Context, err error, sub subscription.Subscription, agentID, environmentID *uuid.UUID) error {
	if !isVendorAlreadyBound(err) {
		return toSubscriptionStatusError(err)
	}
	existing, resolveErr := s.subscriptions.Resolve(ctx, agentID, derefOrNil(environmentID), sub.Vendor)
	if resolveErr != nil {
		return toSubscriptionStatusError(err)
	}
	return status.Errorf(codes.AlreadyExists,
		"target already has a %s subscription attached: %s (%s)", sub.Vendor, existing.Name, existing.ID)
}

func derefOrNil(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func (s *Server) DeleteSubscriptionAttachment(ctx context.Context, req *llmv1.DeleteSubscriptionAttachmentRequest) (*llmv1.DeleteSubscriptionAttachmentResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetId(), "id")
	if err != nil {
		return nil, err
	}
	attachment, err := s.subscriptions.GetAttachment(ctx, id)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	if err := s.requireOrgOwner(ctx, caller.IdentityID, attachment.OrganizationID); err != nil {
		return nil, err
	}
	if err := s.requireTargetEditable(ctx, caller.IdentityID, attachment.OrganizationID, attachment.AgentID, attachment.EnvironmentID); err != nil {
		return nil, err
	}

	if err := s.subscriptions.Detach(ctx, id); err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	// A detached subscription must stop the next call on an already-open
	// connection, not wait for it to close.
	s.publishAttachmentUpdated(ctx, attachment.OrganizationID, attachment.SubscriptionID, id, attachment.AgentID, attachment.EnvironmentID, "deleted")
	return &llmv1.DeleteSubscriptionAttachmentResponse{}, nil
}

func (s *Server) ListSubscriptionAttachments(ctx context.Context, req *llmv1.ListSubscriptionAttachmentsRequest) (*llmv1.ListSubscriptionAttachmentsResponse, error) {
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

	filter := subscription.AttachmentFilter{OrganizationID: organizationID}
	if filter.SubscriptionID, err = uuidPtr(req.GetSubscriptionId(), "subscription_id"); err != nil {
		return nil, err
	}
	if filter.AgentID, err = uuidPtr(req.GetAgentId(), "agent_id"); err != nil {
		return nil, err
	}
	if filter.EnvironmentID, err = uuidPtr(req.GetEnvironmentId(), "environment_id"); err != nil {
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

	result, err := s.subscriptions.ListAttachments(ctx, filter, req.GetPageSize(), cursor)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}

	resp := &llmv1.ListSubscriptionAttachmentsResponse{
		SubscriptionAttachments: make([]*llmv1.SubscriptionAttachment, 0, len(result.Attachments)),
	}
	for _, attachment := range result.Attachments {
		resp.SubscriptionAttachments = append(resp.SubscriptionAttachments, toProtoAttachment(attachment))
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

// ResolveSubscription answers the native-mode question. Internal only -- called
// by the LLM Proxy over Istio, with identifiers that came from the caller's
// OpenZiti identity rather than from anything the workload asserted.
func (s *Server) ResolveSubscription(ctx context.Context, req *llmv1.ResolveSubscriptionRequest) (*llmv1.ResolveSubscriptionResponse, error) {
	environmentID, err := parseUUID(req.GetEnvironmentId(), "environment_id")
	if err != nil {
		return nil, err
	}
	// Empty for a sandbox, which runs no agent class.
	agentID, err := uuidPtr(req.GetAgentId(), "agent_id")
	if err != nil {
		return nil, err
	}
	vendor, err := parseVendor(req.GetVendor())
	if err != nil {
		return nil, err
	}

	sub, err := s.subscriptions.Resolve(ctx, agentID, environmentID, vendor)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}

	token, err := s.resolveSecretValue(ctx, sub.SecretID)
	if err != nil {
		return nil, err
	}
	// The environment owns the allowlist, and reading it here is what keeps the
	// proxy free of an Agents dependency on its connection path.
	allowedModels, err := s.environmentAllowedModels(ctx, environmentID)
	if err != nil {
		return nil, err
	}

	binding := vendorBindings[sub.Vendor]
	return &llmv1.ResolveSubscriptionResponse{
		SubscriptionId:   sub.ID.String(),
		Token:            token,
		AccountId:        sub.AccountID,
		UpstreamEndpoint: binding.upstream,
		Protocol:         binding.protocol,
		AllowedModels:    allowedModels,
		OrganizationId:   sub.OrganizationID.String(),
	}, nil
}

// CountSubscriptionsReferencingSecret is called by the Secrets service before
// deleting a secret. Internal only.
func (s *Server) CountSubscriptionsReferencingSecret(ctx context.Context, req *llmv1.CountSubscriptionsReferencingSecretRequest) (*llmv1.CountSubscriptionsReferencingSecretResponse, error) {
	secretID, err := parseUUID(req.GetSecretId(), "secret_id")
	if err != nil {
		return nil, err
	}
	ids, err := s.subscriptions.CountReferencingSecret(ctx, secretID)
	if err != nil {
		return nil, toSubscriptionStatusError(err)
	}
	resp := &llmv1.CountSubscriptionsReferencingSecretResponse{
		Count:           int32(len(ids)),
		SubscriptionIds: make([]string, 0, len(ids)),
	}
	for _, id := range ids {
		resp.SubscriptionIds = append(resp.SubscriptionIds, id.String())
	}
	return resp, nil
}
