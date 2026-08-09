package grpcserver

import (
	"context"
	"net/http"
	"testing"

	agentsv1 "github.com/agynio/llm/.gen/go/agynio/api/agents/v1"
	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	notificationsv1 "github.com/agynio/llm/.gen/go/agynio/api/notifications/v1"
	secretsv1 "github.com/agynio/llm/.gen/go/agynio/api/secrets/v1"
	"github.com/agynio/llm/internal/subscription"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeSubscriptionStore struct {
	subscriptions map[uuid.UUID]subscription.Subscription
	attachments   []subscription.Attachment
	attachErr     error
	created       *subscription.CreateInput
	deleted       []uuid.UUID
}

func newFakeSubscriptionStore() *fakeSubscriptionStore {
	return &fakeSubscriptionStore{subscriptions: map[uuid.UUID]subscription.Subscription{}}
}

func (f *fakeSubscriptionStore) Create(_ context.Context, input subscription.CreateInput) (subscription.Subscription, error) {
	f.created = &input
	sub := subscription.Subscription{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		Name:           input.Name,
		Vendor:         input.Vendor,
		SecretID:       input.SecretID,
		AccountID:      input.AccountID,
	}
	f.subscriptions[sub.ID] = sub
	return sub, nil
}

func (f *fakeSubscriptionStore) Get(_ context.Context, id uuid.UUID) (subscription.Subscription, error) {
	sub, ok := f.subscriptions[id]
	if !ok {
		return subscription.Subscription{}, subscription.ErrSubscriptionNotFound
	}
	return sub, nil
}

func (f *fakeSubscriptionStore) Update(_ context.Context, input subscription.UpdateInput) (subscription.Subscription, error) {
	sub, ok := f.subscriptions[input.ID]
	if !ok {
		return subscription.Subscription{}, subscription.ErrSubscriptionNotFound
	}
	if input.Name != nil {
		sub.Name = *input.Name
	}
	if input.SecretID != nil {
		sub.SecretID = *input.SecretID
	}
	if input.AccountID != nil {
		sub.AccountID = *input.AccountID
	}
	f.subscriptions[sub.ID] = sub
	return sub, nil
}

func (f *fakeSubscriptionStore) Delete(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	delete(f.subscriptions, id)
	return nil
}

func (f *fakeSubscriptionStore) List(context.Context, uuid.UUID, int32, *subscription.PageCursor) (subscription.ListResult, error) {
	return subscription.ListResult{}, nil
}

func (f *fakeSubscriptionStore) Attach(_ context.Context, input subscription.AttachInput) (subscription.Attachment, error) {
	if f.attachErr != nil {
		return subscription.Attachment{}, f.attachErr
	}
	sub := f.subscriptions[input.SubscriptionID]
	attachment := subscription.Attachment{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		SubscriptionID: input.SubscriptionID,
		Vendor:         sub.Vendor,
		AgentID:        input.AgentID,
		EnvironmentID:  input.EnvironmentID,
	}
	f.attachments = append(f.attachments, attachment)
	return attachment, nil
}

func (f *fakeSubscriptionStore) Detach(context.Context, uuid.UUID) error { return nil }

func (f *fakeSubscriptionStore) GetAttachment(_ context.Context, id uuid.UUID) (subscription.Attachment, error) {
	for _, a := range f.attachments {
		if a.ID == id {
			return a, nil
		}
	}
	return subscription.Attachment{}, subscription.ErrAttachmentNotFound
}

func (f *fakeSubscriptionStore) ListAttachments(context.Context, subscription.AttachmentFilter, int32, *subscription.PageCursor) (subscription.AttachmentListResult, error) {
	return subscription.AttachmentListResult{Attachments: f.attachments}, nil
}

func (f *fakeSubscriptionStore) Resolve(_ context.Context, agentID *uuid.UUID, environmentID uuid.UUID, vendor subscription.Vendor) (subscription.Subscription, error) {
	// Agent scope shadows environment scope for the same vendor.
	for _, scope := range []struct {
		match func(subscription.Attachment) bool
		on    bool
	}{
		{func(a subscription.Attachment) bool {
			return agentID != nil && a.AgentID != nil && *a.AgentID == *agentID
		}, agentID != nil},
		{func(a subscription.Attachment) bool {
			return a.EnvironmentID != nil && *a.EnvironmentID == environmentID
		}, true},
	} {
		if !scope.on {
			continue
		}
		for _, a := range f.attachments {
			if a.Vendor == vendor && scope.match(a) {
				return f.subscriptions[a.SubscriptionID], nil
			}
		}
	}
	return subscription.Subscription{}, subscription.ErrSubscriptionNotFound
}

func (f *fakeSubscriptionStore) CountReferencingSecret(_ context.Context, secretID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for id, sub := range f.subscriptions {
		if sub.SecretID == secretID {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type fakeSecretsClient struct {
	exists bool
	value  string
}

func (f *fakeSecretsClient) ResolveSecret(context.Context, *secretsv1.ResolveSecretRequest, ...grpc.CallOption) (*secretsv1.ResolveSecretResponse, error) {
	return &secretsv1.ResolveSecretResponse{Value: f.value}, nil
}

func (f *fakeSecretsClient) ResolveSecretExists(context.Context, *secretsv1.ResolveSecretExistsRequest, ...grpc.CallOption) (*secretsv1.ResolveSecretExistsResponse, error) {
	return &secretsv1.ResolveSecretExistsResponse{Exists: f.exists}, nil
}

type fakeAgentsClient struct{ allowedModels []string }

func (f *fakeAgentsClient) GetEnvironment(context.Context, *agentsv1.GetEnvironmentRequest, ...grpc.CallOption) (*agentsv1.GetEnvironmentResponse, error) {
	return &agentsv1.GetEnvironmentResponse{
		Environment: &agentsv1.Environment{LlmAllowedModels: f.allowedModels},
	}, nil
}

type fakeNotificationsClient struct {
	published []*notificationsv1.PublishRequest
}

func (f *fakeNotificationsClient) Publish(_ context.Context, req *notificationsv1.PublishRequest, _ ...grpc.CallOption) (*notificationsv1.PublishResponse, error) {
	f.published = append(f.published, req)
	return &notificationsv1.PublishResponse{}, nil
}

func newSubscriptionServer(store *fakeSubscriptionStore, secrets *fakeSecretsClient, agents *fakeAgentsClient, notifications *fakeNotificationsClient) *Server {
	return New(&fakeProviderStore{}, &fakeModelStore{}, &fakeAuthorizationClient{}, http.DefaultClient).
		WithSubscriptions(SubscriptionDeps{Store: store, Secrets: secrets, Agents: agents, Notifications: notifications})
}

// Both vendors are creatable. OpenAI was refused only because the placeholder
// mechanism delivered environment variables and Codex reads a file; with the
// file kind there is nothing left to refuse.
func TestCreateSubscriptionAcceptsBothVendors(t *testing.T) {
	for _, vendor := range []llmv1.Vendor{llmv1.Vendor_VENDOR_ANTHROPIC, llmv1.Vendor_VENDOR_OPENAI} {
		server := newSubscriptionServer(newFakeSubscriptionStore(), &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})
		resp, err := server.CreateSubscription(contextWithIdentity(), &llmv1.CreateSubscriptionRequest{
			OrganizationId: uuid.New().String(),
			Name:           "team-" + vendor.String(),
			Vendor:         vendor,
			SecretId:       uuid.New().String(),
		})
		if err != nil {
			t.Fatalf("create %v subscription: %v", vendor, err)
		}
		if resp.GetSubscription().GetVendor() != vendor {
			t.Fatalf("created vendor = %v, want %v", resp.GetSubscription().GetVendor(), vendor)
		}
	}
}

// The old enum names are aliases on the same numbers, so a caller compiled
// before the rename resolves to the same vendor rather than being rejected.
func TestCreateSubscriptionAcceptsTheDeprecatedVendorNames(t *testing.T) {
	server := newSubscriptionServer(newFakeSubscriptionStore(), &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})
	resp, err := server.CreateSubscription(contextWithIdentity(), &llmv1.CreateSubscriptionRequest{
		OrganizationId: uuid.New().String(),
		Name:           "legacy-caller",
		Vendor:         llmv1.Vendor_VENDOR_CLAUDE,
		SecretId:       uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("create with the deprecated name: %v", err)
	}
	if resp.GetSubscription().GetVendor() != llmv1.Vendor_VENDOR_ANTHROPIC {
		t.Fatalf("vendor = %v, want anthropic", resp.GetSubscription().GetVendor())
	}
}

// Only the environment-variable kind is reported, and only because the
// orchestrator sets it on the container spec without knowing the CLI. A
// file-reading CLI is agynd's to serve outright.
func TestAttachmentReportsOnlyTheVariableItsWriterNeeds(t *testing.T) {
	anthropic := toProtoAttachment(subscription.Attachment{Vendor: subscription.VendorAnthropic})
	if anthropic.GetPlaceholderKind() != llmv1.PlaceholderKind_PLACEHOLDER_KIND_ENV {
		t.Fatalf("anthropic kind = %v", anthropic.GetPlaceholderKind())
	}
	if anthropic.GetPlaceholderEnv() == "" {
		t.Fatal("anthropic placeholder_env is empty")
	}

	// Nothing about the Codex CLI's auth.json is this service's to declare.
	openai := toProtoAttachment(subscription.Attachment{Vendor: subscription.VendorOpenAI})
	if openai.GetPlaceholderPath() != "" || openai.GetPlaceholderContents() != "" {
		t.Fatalf("openai still declares a file: path=%q contents=%q",
			openai.GetPlaceholderPath(), openai.GetPlaceholderContents())
	}
}

func TestCreateSubscriptionRejectsMissingSecret(t *testing.T) {
	server := newSubscriptionServer(newFakeSubscriptionStore(), &fakeSecretsClient{exists: false}, &fakeAgentsClient{}, &fakeNotificationsClient{})

	_, err := server.CreateSubscription(contextWithIdentity(), &llmv1.CreateSubscriptionRequest{
		OrganizationId: uuid.New().String(),
		Name:           "team-claude",
		Vendor:         llmv1.Vendor_VENDOR_ANTHROPIC,
		SecretId:       uuid.New().String(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

// The proxy drops cached bindings on these events, so a detach that publishes
// nothing leaves a revoked credential live until the connection closes.
func TestSubscriptionWritesPublishToBothRooms(t *testing.T) {
	store := newFakeSubscriptionStore()
	notifications := &fakeNotificationsClient{}
	server := newSubscriptionServer(store, &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, notifications)
	organizationID := uuid.New()

	if _, err := server.CreateSubscription(contextWithIdentity(), &llmv1.CreateSubscriptionRequest{
		OrganizationId: organizationID.String(),
		Name:           "team-claude",
		Vendor:         llmv1.Vendor_VENDOR_ANTHROPIC,
		SecretId:       uuid.New().String(),
	}); err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	if len(notifications.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(notifications.published))
	}
	event := notifications.published[0]
	if event.GetEvent() != subscriptionUpdatedEvent {
		t.Fatalf("expected %s, got %s", subscriptionUpdatedEvent, event.GetEvent())
	}
	// The flat room is what the proxy can hold a fixed subscription to; the
	// organization room is for clients that know their org.
	wantRooms := map[string]bool{organizationRoom(organizationID): false, subscriptionsRoom: false}
	for _, room := range event.GetRooms() {
		if _, ok := wantRooms[room]; ok {
			wantRooms[room] = true
		}
	}
	for room, seen := range wantRooms {
		if !seen {
			t.Fatalf("expected event on room %s, got %v", room, event.GetRooms())
		}
	}
}

func TestDeleteSubscriptionNamesItsAttachments(t *testing.T) {
	store := newFakeSubscriptionStore()
	server := newSubscriptionServer(store, &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})
	organizationID := uuid.New()
	environmentID := uuid.New()

	sub, _ := store.Create(context.Background(), subscription.CreateInput{
		OrganizationID: organizationID, Name: "team-claude", Vendor: subscription.VendorAnthropic, SecretID: uuid.New(),
	})
	store.attachments = append(store.attachments, subscription.Attachment{
		ID: uuid.New(), OrganizationID: organizationID, SubscriptionID: sub.ID,
		Vendor: subscription.VendorAnthropic, EnvironmentID: &environmentID,
	})

	_, err := server.DeleteSubscription(contextWithIdentity(), &llmv1.DeleteSubscriptionRequest{Id: sub.ID.String()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
	}
	if !contains(err.Error(), environmentID.String()) {
		t.Fatalf("expected the error to name the environment, got %q", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected no delete, got %v", store.deleted)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// Everything the proxy needs must be on this response, so it performs no
// configuration lookups of its own.
func TestResolveSubscriptionReturnsBindingAndAllowlist(t *testing.T) {
	store := newFakeSubscriptionStore()
	server := newSubscriptionServer(store,
		&fakeSecretsClient{exists: true, value: "sk-ant-oat01-real"},
		&fakeAgentsClient{allowedModels: []string{"claude-sonnet-4-6"}},
		&fakeNotificationsClient{})
	organizationID := uuid.New()
	environmentID := uuid.New()

	sub, _ := store.Create(context.Background(), subscription.CreateInput{
		OrganizationID: organizationID, Name: "team-claude", Vendor: subscription.VendorAnthropic, SecretID: uuid.New(),
	})
	store.attachments = append(store.attachments, subscription.Attachment{
		ID: uuid.New(), OrganizationID: organizationID, SubscriptionID: sub.ID,
		Vendor: subscription.VendorAnthropic, EnvironmentID: &environmentID,
	})

	resp, err := server.ResolveSubscription(context.Background(), &llmv1.ResolveSubscriptionRequest{
		EnvironmentId: environmentID.String(),
		Vendor:        llmv1.Vendor_VENDOR_ANTHROPIC,
	})
	if err != nil {
		t.Fatalf("resolve subscription: %v", err)
	}
	if resp.GetSubscriptionId() != sub.ID.String() {
		t.Fatalf("expected subscription id %s, got %s", sub.ID, resp.GetSubscriptionId())
	}
	if resp.GetToken() != "sk-ant-oat01-real" {
		t.Fatalf("expected the resolved secret value, got %q", resp.GetToken())
	}
	if resp.GetUpstreamEndpoint() != "https://api.anthropic.com" {
		t.Fatalf("unexpected upstream %q", resp.GetUpstreamEndpoint())
	}
	if resp.GetProtocol() != llmv1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES {
		t.Fatalf("unexpected protocol %v", resp.GetProtocol())
	}
	if len(resp.GetAllowedModels()) != 1 || resp.GetAllowedModels()[0] != "claude-sonnet-4-6" {
		t.Fatalf("expected the environment's allowlist, got %v", resp.GetAllowedModels())
	}
}

// A sandbox has no agent, so resolution falls to the environment scope.
func TestResolveSubscriptionNotFoundForUnattachedVendor(t *testing.T) {
	server := newSubscriptionServer(newFakeSubscriptionStore(), &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})

	_, err := server.ResolveSubscription(context.Background(), &llmv1.ResolveSubscriptionRequest{
		EnvironmentId: uuid.New().String(),
		Vendor:        llmv1.Vendor_VENDOR_ANTHROPIC,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

// The attachment listing is where the orchestrator learns the placeholder
// variable name, so it holds no vendor table of its own.
func TestListAttachmentsCarriesVendorAndPlaceholder(t *testing.T) {
	store := newFakeSubscriptionStore()
	server := newSubscriptionServer(store, &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})
	organizationID := uuid.New()
	environmentID := uuid.New()

	sub, _ := store.Create(context.Background(), subscription.CreateInput{
		OrganizationID: organizationID, Name: "team-claude", Vendor: subscription.VendorAnthropic, SecretID: uuid.New(),
	})
	store.attachments = append(store.attachments, subscription.Attachment{
		ID: uuid.New(), OrganizationID: organizationID, SubscriptionID: sub.ID,
		Vendor: subscription.VendorAnthropic, EnvironmentID: &environmentID,
	})

	resp, err := server.ListSubscriptionAttachments(contextWithIdentity(), &llmv1.ListSubscriptionAttachmentsRequest{
		OrganizationId: organizationID.String(),
	})
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(resp.GetSubscriptionAttachments()) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(resp.GetSubscriptionAttachments()))
	}
	got := resp.GetSubscriptionAttachments()[0]
	if got.GetVendor() != llmv1.Vendor_VENDOR_ANTHROPIC {
		t.Fatalf("unexpected vendor %v", got.GetVendor())
	}
	if got.GetPlaceholderEnv() != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("expected the claude placeholder, got %q", got.GetPlaceholderEnv())
	}
	if got.GetEnvironmentId() != environmentID.String() {
		t.Fatalf("unexpected target %v", got.GetTarget())
	}
}

// The Orchestrator lists attachments at workload assembly, acting for the
// platform rather than for a principal, so it sends no caller identity -- the
// same way it calls Runners and Agents. Requiring one failed every native-mode
// sandbox with "identity id is required".
func TestListSubscriptionAttachmentsServesAPlatformCall(t *testing.T) {
	store := newFakeSubscriptionStore()
	server := newSubscriptionServer(store, &fakeSecretsClient{exists: true}, &fakeAgentsClient{}, &fakeNotificationsClient{})

	_, err := server.ListSubscriptionAttachments(context.Background(), &llmv1.ListSubscriptionAttachmentsRequest{
		OrganizationId: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("list without a caller identity: %v", err)
	}
}
