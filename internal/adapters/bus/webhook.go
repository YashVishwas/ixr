// Package bus contains concrete bus implementations for ixr.
package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// WebhookBus POSTs each CallEvent as JSON to one or more URLs.
// Deliveries are asynchronous and failures are logged but not retried.
type WebhookBus struct {
	urls    []string
	client  *http.Client
	headers map[string]string
	ch      chan *schema.CallEvent
	subs    []plugin.EventConsumer
	mu      sync.RWMutex
}

// NewWebhookBus creates a bus that fans out events to all targets.
// headers are added to every outbound request (e.g. "Authorization": "Bearer …").
func NewWebhookBus(urls []string, headers map[string]string) *WebhookBus {
	return &WebhookBus{
		urls:    urls,
		headers: headers,
		client:  &http.Client{Timeout: 5 * time.Second},
		ch:      make(chan *schema.CallEvent, 512),
	}
}

// Subscribe registers c to receive local (in-process) event delivery alongside webhooks.
func (b *WebhookBus) Subscribe(c plugin.EventConsumer) {
	b.mu.Lock()
	b.subs = append(b.subs, c)
	b.mu.Unlock()
}

// Publish enqueues ev for async delivery.
func (b *WebhookBus) Publish(_ context.Context, ev *schema.CallEvent) error {
	select {
	case b.ch <- ev:
	default:
		slog.Warn("webhook bus: buffer full, dropping event")
	}
	return nil
}

// Start launches the dispatch loop; call in a goroutine.
func (b *WebhookBus) Start(ctx context.Context) {
	for {
		select {
		case ev := <-b.ch:
			b.deliver(ctx, ev)
		case <-ctx.Done():
			for {
				select {
				case ev := <-b.ch:
					b.deliver(ctx, ev)
				default:
					return
				}
			}
		}
	}
}

func (b *WebhookBus) deliver(ctx context.Context, ev *schema.CallEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		slog.Error("webhook bus: marshal error", "err", err)
		return
	}

	for _, url := range b.urls {
		url := url
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range b.headers {
				req.Header.Set(k, v)
			}
			resp, err := b.client.Do(req)
			if err != nil {
				slog.Warn("webhook delivery failed", "url", url, "err", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				slog.Warn("webhook non-2xx response", "url", url, "status", resp.StatusCode)
			}
		}()
	}

	// Also dispatch to in-process subscribers.
	b.mu.RLock()
	subs := b.subs
	b.mu.RUnlock()
	for _, s := range subs {
		if err := s.OnEvent(ctx, ev); err != nil {
			slog.Error("webhook bus local subscriber error", "plugin", s.Name(), "err", fmt.Sprintf("%v", err))
		}
	}
}
