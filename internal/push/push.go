// Package push sends Web Push notifications.
package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/elee1766/jcode-web/internal/store"
)

type Pusher struct {
	log     *slog.Logger
	store   *store.Store
	subject string
	pub     string
	priv    string
}

const (
	keyPub  = "vapid_public"
	keyPriv = "vapid_private"
)

// New loads VAPID keys from the store, generating them on first run.
func New(ctx context.Context, log *slog.Logger, st *store.Store, subject string) (*Pusher, error) {
	pub, err1 := st.Get(ctx, keyPub)
	priv, err2 := st.Get(ctx, keyPriv)
	if err1 != nil || err2 != nil {
		var err error
		priv, pub, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, err
		}
		if err := st.Set(ctx, keyPub, pub); err != nil {
			return nil, err
		}
		if err := st.Set(ctx, keyPriv, priv); err != nil {
			return nil, err
		}
		log.Info("generated VAPID keys")
	}
	return &Pusher{log: log, store: st, subject: subject, pub: pub, priv: priv}, nil
}

func (p *Pusher) PublicKey() string { return p.pub }

type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Notify sends to every subscription. Gone subscriptions are removed.
func (p *Pusher) Notify(ctx context.Context, title, body, tag, url string) {
	p.Send(ctx, title, body, tag, url)
}

// Send returns the number of successful deliveries.
func (p *Pusher) Send(ctx context.Context, title, body, tag, url string) int {
	subs, err := p.store.ListPush(ctx)
	if err != nil {
		p.log.Error("list push subscriptions", "error", err)
		return 0
	}
	msg, _ := json.Marshal(payload{Title: title, Body: body, Tag: tag, URL: url})
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for _, s := range subs {
		wg.Add(1)
		go func(s store.PushSubscription) {
			defer wg.Done()
			resp, err := webpush.SendNotificationWithContext(ctx, msg, &webpush.Subscription{
				Endpoint: s.Endpoint,
				Keys:     webpush.Keys{P256dh: s.P256dh, Auth: s.Auth},
			}, &webpush.Options{
				Subscriber:      p.subject,
				VAPIDPublicKey:  p.pub,
				VAPIDPrivateKey: p.priv,
				TTL:             3600,
				Topic:           topic(tag),
				Urgency:         webpush.UrgencyNormal,
			})
			if err != nil {
				p.log.Warn("push failed", "error", err)
				return
			}
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusGone, http.StatusNotFound:
				_ = p.store.DeletePush(context.Background(), s.Endpoint)
			case http.StatusOK, http.StatusCreated, http.StatusAccepted:
				mu.Lock()
				ok++
				mu.Unlock()
			default:
				p.log.Warn("push rejected", "status", resp.StatusCode)
			}
		}(s)
	}
	wg.Wait()
	return ok
}

// topic must be <= 32 url-safe chars. Same topic replaces pending pushes.
func topic(tag string) string {
	out := make([]byte, 0, 32)
	for i := len(tag) - 1; i >= 0 && len(out) < 32; i-- {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
