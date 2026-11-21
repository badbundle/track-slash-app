package notifications

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	apnsProductionHost = "https://api.push.apple.com"
	apnsSandboxHost    = "https://api.sandbox.push.apple.com"
	// Apple rejects a provider token older than an hour and asks that it is
	// not renewed more often than every 20 minutes.
	apnsTokenLifetime = 45 * time.Minute
)

// APNsSender delivers a push to an iOS device through Apple's HTTP/2 provider
// API, authenticated with a .p8 auth key.
type APNsSender struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string
	topic  string
	host   string
	client *http.Client

	mu        sync.Mutex
	token     string
	tokenTime time.Time
	now       func() time.Time
}

// NewAPNsSender reads the PEM text of the .p8 key. topic is the app's bundle
// ID; sandbox selects Apple's development servers, which only accept tokens
// from builds installed from Xcode.
func NewAPNsSender(keyPEM, keyID, teamID, topic string, sandbox bool, client *http.Client) (*APNsSender, error) {
	if keyPEM == "" || keyID == "" || teamID == "" || topic == "" {
		return nil, errors.New("APNs sender requires an auth key, key ID, team ID and topic")
	}
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("APNs auth key is not PEM text")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("APNs auth key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APNs auth key must be an ECDSA key")
	}
	host := apnsProductionHost
	if sandbox {
		host = apnsSandboxHost
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &APNsSender{key: key, keyID: keyID, teamID: teamID, topic: topic, host: host, client: client, now: time.Now}, nil
}

func (s *APNsSender) providerToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Sub(s.tokenTime) < apnsTokenLifetime {
		return s.token, nil
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": s.teamID, "iat": now.Unix()})
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", err
	}
	s.token, s.tokenTime = signed, now
	return signed, nil
}

type apnsAlert struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type apnsAPS struct {
	Alert    apnsAlert `json:"alert"`
	Sound    string    `json:"sound"`
	ThreadID string    `json:"thread-id,omitempty"`
}

type apnsPayload struct {
	APS apnsAPS `json:"aps"`
	// URL is the page the notification is about; the app reads a form's
	// project and ref from it.
	URL string `json:"url"`
}

func (s *APNsSender) Send(ctx context.Context, delivery store.PushNotificationDelivery, payload store.PushNotificationPayload) (int, error) {
	body, err := json.Marshal(apnsPayload{
		APS: apnsAPS{Alert: apnsAlert{Title: payload.Title, Body: payload.Body}, Sound: "default", ThreadID: payload.Tag},
		URL: payload.URL,
	})
	if err != nil {
		return 0, err
	}
	token, err := s.providerToken()
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+"/3/device/"+delivery.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("authorization", "bearer "+token)
	request.Header.Set("apns-topic", s.topic)
	request.Header.Set("apns-push-type", "alert")
	request.Header.Set("apns-priority", "10")
	if payload.Tag != "" {
		request.Header.Set("apns-collapse-id", truncateBytes(payload.Tag, 64))
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}

func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

// RoutingSender sends each delivery through the sender for its kind of
// subscription. Either may be nil when that channel is not configured.
type RoutingSender struct {
	Web  Sender
	APNs Sender
}

func (r RoutingSender) Send(ctx context.Context, delivery store.PushNotificationDelivery, payload store.PushNotificationPayload) (int, error) {
	sender := r.Web
	if delivery.Kind == "apns" {
		sender = r.APNs
	}
	if sender == nil {
		// 404 makes the worker disable the subscription, which is right for
		// a channel that has been switched off.
		return http.StatusNotFound, nil
	}
	return sender.Send(ctx, delivery, payload)
}

var (
	_ Sender = (*APNsSender)(nil)
	_ Sender = RoutingSender{}
)
