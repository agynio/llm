package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

const maxRequestBodySize int64 = 1 << 20

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

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Service struct {
	resolver *Resolver
	client   Doer
}

func NewService(resolver *Resolver, client Doer) *Service {
	return &Service{resolver: resolver, client: client}
}

func (s *Service) createResponse(ctx context.Context, modelID uuid.UUID, body []byte) (Response, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return Response{}, err
	}
	updated, err := rewriteRequestBody(body, resolved.Model.RemoteName, false)
	if err != nil {
		return Response{}, err
	}
	return s.doRequest(ctx, resolved, updated)
}

func (s *Service) createResponseStream(ctx context.Context, modelID uuid.UUID, body []byte) (StreamResponse, error) {
	resolved, err := s.resolver.Resolve(ctx, modelID)
	if err != nil {
		return StreamResponse{}, err
	}
	updated, err := rewriteRequestBody(body, resolved.Model.RemoteName, true)
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
	url := endpoint + "/responses"
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

type Handler struct {
	service *Service
}

func NewHandler(service *Service) http.Handler {
	return &Handler{service: service}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/responses" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	modelID, stream, err := parseRequestPayload(body)
	if err != nil {
		writeProxyError(w, err)
		return
	}

	if stream {
		resp, err := h.service.createResponseStream(r.Context(), modelID, body)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		defer resp.Body.Close()

		copyHeaders(w.Header(), resp.Header, map[string]struct{}{"Content-Length": {}})
		w.WriteHeader(resp.StatusCode)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_, _ = io.Copy(w, resp.Body)
			return
		}
		if err := streamToClient(r.Context(), w, resp.Body); err != nil {
			log.Printf("stream response failed: %v", err)
		}
		return
	}

	resp, err := h.service.createResponse(r.Context(), modelID, body)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	copyHeaders(w.Header(), resp.Header, nil)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

func parseRequestPayload(body []byte) (uuid.UUID, bool, error) {
	if len(body) == 0 {
		return uuid.UUID{}, false, fmt.Errorf("%w: body is empty", ErrInvalidBody)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return uuid.UUID{}, false, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}

	rawModel, ok := payload["model"]
	if !ok {
		return uuid.UUID{}, false, ErrMissingModel
	}
	modelStr, ok := rawModel.(string)
	if !ok || strings.TrimSpace(modelStr) == "" {
		return uuid.UUID{}, false, fmt.Errorf("%w: model must be a string", ErrInvalidBody)
	}
	modelID, err := uuid.Parse(modelStr)
	if err != nil {
		return uuid.UUID{}, false, fmt.Errorf("%w: model must be a UUID", ErrInvalidBody)
	}

	stream := false
	if rawStream, ok := payload["stream"]; ok {
		value, ok := rawStream.(bool)
		if !ok {
			return uuid.UUID{}, false, fmt.Errorf("%w: stream must be a boolean", ErrInvalidBody)
		}
		stream = value
	}

	return modelID, stream, nil
}

func rewriteRequestBody(body []byte, remoteName string, forceStream bool) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: body is empty", ErrInvalidBody)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
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

func writeProxyError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, ErrInvalidBody), errors.Is(err, ErrMissingModel):
		status = http.StatusBadRequest
	case errors.Is(err, provider.ErrProviderNotFound), errors.Is(err, model.ErrModelNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrUnsupportedAuthMethod), errors.Is(err, ErrMissingToken):
		status = http.StatusBadRequest
	}
	http.Error(w, err.Error(), status)
}

func copyHeaders(dst, src http.Header, skip map[string]struct{}) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if skip != nil {
			if _, ok := skip[canonical]; ok {
				continue
			}
		}
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
}

func streamToClient(ctx context.Context, w http.ResponseWriter, body io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported")
	}
	buffer := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
