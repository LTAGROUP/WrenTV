// Package webhook delivers signed JSON event notifications to configured URLs.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/LTAGROUP/haven/internal/config"
)

type Sender struct {
	hooks  func() []config.WebhookConfig
	client *http.Client
}

func New(hooks func() []config.WebhookConfig) *Sender {
	return &Sender{hooks: hooks, client: &http.Client{Timeout: 10 * time.Second}}
}

// Envelope is the common payload shape.
type Envelope struct {
	Event     string `json:"event"`
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Server    Server `json:"server"`
	Data      any    `json:"data"`
}

type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Send delivers asynchronously with retries (1s, 5s, 30s, 2m).
func (s *Sender) Send(env Envelope) {
	env.Timestamp = time.Now().Unix()
	body, err := json.Marshal(env)
	if err != nil {
		return
	}
	for _, h := range s.hooks() {
		if h.URL == "" || (len(h.Events) > 0 && !slices.Contains(h.Events, env.Event)) {
			continue
		}
		go s.deliver(h, env.Event, body)
	}
}

func (s *Sender) deliver(h config.WebhookConfig, event string, body []byte) {
	backoff := []time.Duration{0, time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute}
	for i, wait := range backoff {
		time.Sleep(wait)
		err := s.post(h, event, body)
		if err == nil {
			slog.Info("webhook delivered", "event", event, "url", h.URL)
			return
		}
		slog.Warn("webhook delivery failed", "event", event, "url", h.URL, "attempt", i+1, "err", err)
	}
}

func (s *Sender) post(h config.WebhookConfig, event string, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Haven-Server")
	req.Header.Set("X-Haven-Event", event)
	if h.Secret != "" {
		mac := hmac.New(sha256.New, []byte(h.Secret))
		mac.Write(body)
		req.Header.Set("X-Haven-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
