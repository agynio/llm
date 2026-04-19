package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/identity"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	testModelPrompt                     = "Hello, world"
	testModelTimeout                    = 30 * time.Second
	testModelAnthropicHeader            = "2023-06-01"
	testModelAnthropicMaxTokens         = 256
	testModelMaxResponseBodyBytes int64 = 64 * 1024
	testModelMaxErrorBodyBytes          = 1024
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type testModelError struct {
	code    codes.Code
	message string
}

func (e *testModelError) Error() string {
	return e.message
}

func newTestModelError(code codes.Code, message string) *testModelError {
	return &testModelError{code: code, message: message}
}

type testModelInput struct {
	endpoint   string
	token      string
	remoteName string
	protocol   provider.Protocol
	authMethod provider.AuthMethod
}

type responsesRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesResponse struct {
	Output []responsesOutput `json:"output"`
}

type responsesOutput struct {
	Content []responsesContent `json:"content"`
}

type responsesContent struct {
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Text string `json:"text"`
}

func (s *Server) TestModel(ctx context.Context, req *llmv1.TestModelRequest) (*llmv1.TestModelResponse, error) {
	caller, err := identity.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	modelID, err := parseUUID(req.GetModelId(), "model_id")
	if err != nil {
		return nil, err
	}

	mdl, err := s.models.Get(ctx, modelID)
	if err != nil {
		return nil, toStatusError(err)
	}
	if err := s.requireOrgMember(ctx, caller.IdentityID, mdl.OrganizationID); err != nil {
		return nil, err
	}

	prov, err := s.providers.GetWithToken(ctx, mdl.ProviderID)
	if err != nil {
		return nil, toStatusError(err)
	}

	input, err := newTestModelInput(mdl, prov)
	if err != nil {
		return nil, statusErrorForTestModel(err)
	}

	outputText, err := testModel(ctx, s.httpClient, input)
	if err != nil {
		return nil, statusErrorForTestModel(err)
	}

	return &llmv1.TestModelResponse{OutputText: outputText}, nil
}

func newTestModelInput(mdl model.Model, prov provider.ProviderWithToken) (testModelInput, error) {
	endpoint := strings.TrimSpace(prov.Endpoint)
	if endpoint == "" {
		return testModelInput{}, newTestModelError(codes.FailedPrecondition, "model endpoint missing")
	}

	remoteName := strings.TrimSpace(mdl.RemoteName)
	if remoteName == "" {
		return testModelInput{}, newTestModelError(codes.FailedPrecondition, "model remote name missing")
	}

	protocol := prov.Protocol
	switch protocol {
	case provider.ProtocolResponses, provider.ProtocolAnthropicMessages:
	default:
		return testModelInput{}, newTestModelError(codes.FailedPrecondition, "unsupported model protocol")
	}

	authMethod := prov.AuthMethod
	switch authMethod {
	case provider.AuthMethodBearer, provider.AuthMethodXAPIKey:
	default:
		return testModelInput{}, newTestModelError(codes.FailedPrecondition, "unsupported auth method")
	}

	token := strings.TrimSpace(prov.Token)
	if token == "" {
		return testModelInput{}, newTestModelError(codes.FailedPrecondition, "model token missing")
	}

	return testModelInput{
		endpoint:   endpoint,
		token:      token,
		remoteName: remoteName,
		protocol:   protocol,
		authMethod: authMethod,
	}, nil
}

func testModel(ctx context.Context, httpClient HTTPClient, input testModelInput) (string, error) {
	body, headers, err := buildTestModelRequest(input)
	if err != nil {
		return "", err
	}

	requestCtx, cancel := context.WithTimeout(ctx, testModelTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, input.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", newTestModelError(codes.FailedPrecondition, fmt.Sprintf("invalid request: %v", err))
	}
	request.Header = headers

	response, err := httpClient.Do(request)
	if err != nil {
		return "", newTestModelError(codes.Unavailable, fmt.Sprintf("request failed: %v", err))
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, testModelMaxResponseBodyBytes))
	if err != nil {
		return "", newTestModelError(codes.Unavailable, fmt.Sprintf("failed to read response: %v", err))
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", newTestModelError(codes.Unavailable, formatUpstreamError(response.StatusCode, responseBody))
	}

	outputText, err := parseTestModelOutput(input.protocol, responseBody)
	if err != nil {
		return "", newTestModelError(codes.Unavailable, err.Error())
	}

	return outputText, nil
}

func buildTestModelRequest(input testModelInput) ([]byte, http.Header, error) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")

	if input.protocol == provider.ProtocolAnthropicMessages {
		headers.Set("anthropic-version", testModelAnthropicHeader)
	}

	switch input.authMethod {
	case provider.AuthMethodBearer:
		headers.Set("Authorization", fmt.Sprintf("Bearer %s", input.token))
	case provider.AuthMethodXAPIKey:
		headers.Set("x-api-key", input.token)
	default:
		panic("unreachable auth method")
	}

	var payload any
	switch input.protocol {
	case provider.ProtocolResponses:
		payload = responsesRequest{Model: input.remoteName, Input: testModelPrompt}
	case provider.ProtocolAnthropicMessages:
		payload = anthropicRequest{
			Model:     input.remoteName,
			MaxTokens: testModelAnthropicMaxTokens,
			Messages: []anthropicMessage{
				{Role: "user", Content: testModelPrompt},
			},
		}
	default:
		panic("unreachable model protocol")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, newTestModelError(codes.Internal, fmt.Sprintf("failed to encode request: %v", err))
	}

	return body, headers, nil
}

func statusErrorForTestModel(err error) error {
	if err == nil {
		panic("test model error is nil")
	}
	var testErr *testModelError
	if errors.As(err, &testErr) {
		return status.Error(testErr.code, testErr.message)
	}
	return status.Error(codes.Internal, err.Error())
}

func parseTestModelOutput(protocol provider.Protocol, responseBody []byte) (string, error) {
	switch protocol {
	case provider.ProtocolResponses:
		return parseResponsesOutput(responseBody)
	case provider.ProtocolAnthropicMessages:
		return parseAnthropicOutput(responseBody)
	default:
		panic("unreachable model protocol")
	}
}

func parseResponsesOutput(responseBody []byte) (string, error) {
	var payload responsesResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", fmt.Errorf("failed to parse responses output: %w", err)
	}

	for _, output := range payload.Output {
		for _, content := range output.Content {
			text := strings.TrimSpace(content.Text)
			if text != "" {
				return text, nil
			}
		}
	}

	return "", errors.New("response output text missing")
}

func parseAnthropicOutput(responseBody []byte) (string, error) {
	var payload anthropicResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", fmt.Errorf("failed to parse anthropic output: %w", err)
	}

	for _, content := range payload.Content {
		text := strings.TrimSpace(content.Text)
		if text != "" {
			return text, nil
		}
	}

	return "", errors.New("response output text missing")
}

func formatUpstreamError(statusCode int, responseBody []byte) string {
	trimmed := strings.TrimSpace(string(responseBody))
	if trimmed == "" {
		return fmt.Sprintf("request failed with status %d", statusCode)
	}
	if len(trimmed) > testModelMaxErrorBodyBytes {
		trimmed = truncateUTF8(trimmed, testModelMaxErrorBodyBytes) + "..."
	}
	return fmt.Sprintf("request failed with status %d: %s", statusCode, trimmed)
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || value == "" {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cutoff := 0
	for i := range value {
		if i > maxBytes {
			break
		}
		cutoff = i
	}
	return value[:cutoff]
}
