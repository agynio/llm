package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/google/uuid"
)

var (
	ErrInvalidBody           = errors.New("invalid body")
	ErrMissingModel          = errors.New("model is required")
	ErrUnsupportedAuthMethod = errors.New("unsupported auth method")
	ErrMissingToken          = errors.New("provider token is required")
)

type ProviderStore interface {
	GetWithToken(ctx context.Context, id uuid.UUID) (provider.ProviderWithToken, error)
}

type ModelStore interface {
	Get(ctx context.Context, id uuid.UUID) (model.Model, error)
}

type Resolver struct {
	providers ProviderStore
	models    ModelStore
}

type ResolvedModel struct {
	Model    model.Model
	Provider provider.ProviderWithToken
}

func NewResolver(providers ProviderStore, models ModelStore) *Resolver {
	return &Resolver{providers: providers, models: models}
}

func (r *Resolver) Resolve(ctx context.Context, modelID uuid.UUID) (ResolvedModel, error) {
	mdl, err := r.models.Get(ctx, modelID)
	if err != nil {
		return ResolvedModel{}, err
	}
	prov, err := r.providers.GetWithToken(ctx, mdl.ProviderID)
	if err != nil {
		return ResolvedModel{}, err
	}
	return ResolvedModel{Model: mdl, Provider: prov}, nil
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type StreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type Service struct {
	resolver *Resolver
	client   *http.Client
}

func NewService(resolver *Resolver) *Service {
	return &Service{resolver: resolver, client: &http.Client{}}
}

func (s *Service) CreateResponse(ctx context.Context, modelID uuid.UUID, body []byte) (Response, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return Response{}, err
	}
	updated, err := updateRequestBody(body, resolved.Model.RemoteName, false)
	if err != nil {
		return Response{}, err
	}
	return s.doRequest(ctx, resolved, updated)
}

func (s *Service) CreateResponseStream(ctx context.Context, modelID uuid.UUID, body []byte) (StreamResponse, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return StreamResponse{}, err
	}
	updated, err := updateRequestBody(body, resolved.Model.RemoteName, true)
	if err != nil {
		return StreamResponse{}, err
	}
	return s.doStreamRequest(ctx, resolved, updated)
}

func (s *Service) createResponseFromPayload(ctx context.Context, modelID uuid.UUID, payload map[string]any) (Response, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return Response{}, err
	}
	updated, err := updateRequestPayload(payload, resolved.Model.RemoteName, false)
	if err != nil {
		return Response{}, err
	}
	return s.doRequest(ctx, resolved, updated)
}

func (s *Service) createResponseStreamFromPayload(ctx context.Context, modelID uuid.UUID, payload map[string]any) (StreamResponse, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return StreamResponse{}, err
	}
	updated, err := updateRequestPayload(payload, resolved.Model.RemoteName, true)
	if err != nil {
		return StreamResponse{}, err
	}
	return s.doStreamRequest(ctx, resolved, updated)
}

func (s *Service) doRequest(ctx context.Context, resolved ResolvedModel, body []byte) (Response, error) {
	req, err := s.buildRequest(ctx, resolved, body, false)
	if err != nil {
		return Response{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}

	return Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: respBody}, nil
}

func (s *Service) doStreamRequest(ctx context.Context, resolved ResolvedModel, body []byte) (StreamResponse, error) {
	req, err := s.buildRequest(ctx, resolved, body, true)
	if err != nil {
		return StreamResponse{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return StreamResponse{}, fmt.Errorf("send request: %w", err)
	}
	return StreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

func (s *Service) buildRequest(ctx context.Context, resolved ResolvedModel, body []byte, stream bool) (*http.Request, error) {
	endpoint := strings.TrimRight(resolved.Provider.Endpoint, "/")
	if endpoint == "" {
		panic("provider endpoint is empty")
	}
	url := endpoint + "/v1/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	method := resolved.Provider.AuthMethod
	if method != provider.AuthMethodBearer {
		return nil, ErrUnsupportedAuthMethod
	}
	token := strings.TrimSpace(resolved.Provider.Token)
	if token == "" {
		return nil, ErrMissingToken
	}
	req.Header.Set("Authorization", "Bearer "+token)

	return req, nil
}

type Event struct {
	EventType string
	Data      []byte
}

func ReadSSE(ctx context.Context, reader io.Reader, handle func(Event) error) error {
	bufReader := bufio.NewReader(reader)
	var (
		eventType string
		dataLines []string
	)

	emit := func() error {
		if len(dataLines) == 0 && eventType == "" {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		return handle(Event{EventType: eventType, Data: []byte(data)})
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := bufReader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read sse: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		switch {
		case line == "":
			if err := emit(); err != nil {
				return err
			}
			eventType = ""
			dataLines = dataLines[:0]
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimLeft(line[len("data:"):], " ")
			dataLines = append(dataLines, data)
		default:
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if err := emit(); err != nil {
		return err
	}
	return nil
}

func updateRequestBody(body []byte, remoteName string, forceStream bool) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: body is empty", ErrInvalidBody)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return updateRequestPayload(payload, remoteName, forceStream)
}

func updateRequestPayload(payload map[string]any, remoteName string, forceStream bool) ([]byte, error) {
	payload["model"] = remoteName
	if forceStream {
		payload["stream"] = true
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return updated, nil
}
