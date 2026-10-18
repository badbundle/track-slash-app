package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/bradleymackey/track-slash/internal/store"
)

type Sender interface {
	Send(context.Context, store.PushNotificationDelivery, store.PushNotificationPayload) (int, error)
}

type WebPushSender struct {
	publicKey  string
	privateKey string
	subscriber string
	client     webpush.HTTPClient
}

// NewWebPushSender sends with client, or with newPushHTTPClient when client is
// nil.
func NewWebPushSender(publicKey, privateKey, subscriber string, client webpush.HTTPClient) (*WebPushSender, error) {
	if publicKey == "" || privateKey == "" || subscriber == "" {
		return nil, errors.New("web push sender requires VAPID public key, private key, and subscriber")
	}
	if client == nil {
		client = newPushHTTPClient()
	}
	return &WebPushSender{publicKey: publicKey, privateKey: privateKey, subscriber: subscriber, client: client}, nil
}

var errNonPublicPushAddress = errors.New("push endpoint is not a public address")

// newPushHTTPClient returns the client deliveries are sent with. A browser
// chooses its own push endpoint, so the client connects only to addresses
// store.PushEndpointAddressAllowed accepts, checked on the resolved address as
// each connection is made so that DNS cannot point it elsewhere, and returns a
// redirect as the response rather than following it. It connects directly:
// through a proxy, the address checked would be the proxy's.
func newPushHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: dialPublicPushAddress}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func dialPublicPushAddress(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil || !store.PushEndpointAddressAllowed(addrPort.Addr()) {
		return fmt.Errorf("%w: %s", errNonPublicPushAddress, address)
	}
	return nil
}

func (s *WebPushSender) Send(ctx context.Context, delivery store.PushNotificationDelivery, payload store.PushNotificationPayload) (int, error) {
	message, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	response, err := webpush.SendNotificationWithContext(ctx, message, &webpush.Subscription{
		Endpoint: delivery.Endpoint,
		Keys: webpush.Keys{
			P256dh: delivery.P256DH,
			Auth:   delivery.AuthSecret,
		},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subscriber,
		Topic:           payload.Tag,
		TTL:             3600,
		Urgency:         webpush.UrgencyNormal,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
	})
	if response != nil {
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	}
	if err != nil {
		return 0, err
	}
	if response == nil {
		return 0, errors.New("web push provider returned no response")
	}
	return response.StatusCode, nil
}

var _ Sender = (*WebPushSender)(nil)
var _ webpush.HTTPClient = (*http.Client)(nil)
