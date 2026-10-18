package notifications

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/bradleymackey/track-slash/internal/store"
)

func newTestWebPushSender(t *testing.T) *WebPushSender {
	t.Helper()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}
	sender, err := NewWebPushSender(publicKey, privateKey, "mailto:ops@example.com", nil)
	if err != nil {
		t.Fatalf("NewWebPushSender: %v", err)
	}
	return sender
}

func testPushDelivery(t *testing.T, endpoint string) store.PushNotificationDelivery {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return store.PushNotificationDelivery{
		Endpoint:   endpoint,
		P256DH:     base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		AuthSecret: base64.RawURLEncoding.EncodeToString(auth),
	}
}

// A subscription's endpoint is whatever its browser said, so the sender must
// not connect to this host or its network, whatever the name resolves to.
func TestWebPushSenderRefusesNonPublicAddresses(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()
	defer listener.Close()

	sender := newTestWebPushSender(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, endpoint := range []string{
		"https://" + listener.Addr().String() + "/push",
		"https://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + "/push",
	} {
		status, err := sender.Send(ctx, testPushDelivery(t, endpoint), store.PushNotificationPayload{Title: "t"})
		if !errors.Is(err, errNonPublicPushAddress) || status != 0 {
			t.Fatalf("Send %s = %d, %v; want errNonPublicPushAddress", endpoint, status, err)
		}
	}
	if n := accepted.Load(); n != 0 {
		t.Fatalf("loopback listener accepted %d connections", n)
	}
}

func TestDialPublicPushAddress(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"8.8.8.8:443", "[2a00:1450:4009:81d::200a]:443"} {
		if err := dialPublicPushAddress("tcp", address, nil); err != nil {
			t.Fatalf("dial %s refused: %v", address, err)
		}
	}
	for _, address := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.1:443", "169.254.169.254:80", "100.64.0.1:443", "0.0.0.0:443", "not an address"} {
		if err := dialPublicPushAddress("tcp", address, nil); !errors.Is(err, errNonPublicPushAddress) {
			t.Fatalf("dial %s = %v, want errNonPublicPushAddress", address, err)
		}
	}
}

// A redirect is the push service's answer, not somewhere to send the
// notification next: following one would reach whatever it names.
func TestWebPushSenderDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer target.Close()
	pushService := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/internal", http.StatusTemporaryRedirect)
	}))
	defer pushService.Close()

	sender := newTestWebPushSender(t)
	client, ok := sender.client.(*http.Client)
	if !ok {
		t.Fatalf("default client = %T, want *http.Client", sender.client)
	}
	// The test servers listen on loopback, which the dialer refuses, so this
	// borrows a transport that trusts the test certificate and keeps the
	// client's own redirect policy.
	client.Transport = pushService.Client().Transport

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := sender.Send(ctx, testPushDelivery(t, pushService.URL+"/push"), store.PushNotificationPayload{Title: "t"})
	if err != nil || status != http.StatusTemporaryRedirect {
		t.Fatalf("Send = %d, %v; want %d", status, err, http.StatusTemporaryRedirect)
	}
	if n := followed.Load(); n != 0 {
		t.Fatalf("redirect target reached %d times", n)
	}
}
