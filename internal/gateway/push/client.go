package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrTransport          = errors.New("push transport failure")
	ErrClientResponse     = errors.New("push returned a client error")
	ErrServerResponse     = errors.New("push returned a server error")
	ErrUnexpectedResponse = errors.New("push returned an unexpected status")
)

type Client struct {
	endpoint  string
	http      *http.Client
	transport *http.Transport
}

type requestPosition struct {
	RequestID   uuid.UUID            `json:"request_id"`
	RecipientID uuid.UUID            `json:"recipient_id"`
	Kind        string               `json:"kind"`
	Data        map[string]uuid.UUID `json:"data"`
}

func New(baseURL string, timeout time.Duration, tracerProvider trace.TracerProvider) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" ||
		base.User != nil || base.ForceQuery || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("PUSH_HTTP_URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	if timeout <= 0 {
		return nil, errors.New("PUSH_REQUEST_TIMEOUT must be positive")
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       timeout,
	}
	instrumented := otelhttp.NewTransport(transport,
		otelhttp.WithTracerProvider(tracerProvider),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return "POST /api/v1/push" }),
	)
	return &Client{
		endpoint: base.JoinPath("api", "v1", "push").String(),
		http: &http.Client{
			Transport:     instrumented,
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		transport: transport,
	}, nil
}

func (c *Client) RequestPosition(ctx context.Context, tripID, driverID uuid.UUID) error {
	body, err := json.Marshal(requestPosition{
		RequestID:   uuid.New(),
		RecipientID: driverID,
		Kind:        "REQUEST_POSITION",
		Data:        map[string]uuid.UUID{"trip_id": tripID},
	})
	if err != nil {
		return fmt.Errorf("encode request position command: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request position command: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransport, err)
	}

	// The status is sufficient. Drain a small response to reuse the connection.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	switch {
	case response.StatusCode == http.StatusAccepted:
		return nil
	case response.StatusCode >= 400 && response.StatusCode < 500:
		return fmt.Errorf("%w: HTTP %d", ErrClientResponse, response.StatusCode)
	case response.StatusCode >= 500 && response.StatusCode < 600:
		return fmt.Errorf("%w: HTTP %d", ErrServerResponse, response.StatusCode)
	default:
		return fmt.Errorf("%w: HTTP %d", ErrUnexpectedResponse, response.StatusCode)
	}
}

func (c *Client) CloseIdleConnections() {
	c.transport.CloseIdleConnections()
}
