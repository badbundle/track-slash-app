package notifications

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bradleymackey/track-slash/internal/store"
)

func testAPNsKey(t *testing.T) (string, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), &key.PublicKey
}

func TestNewAPNsSenderValidation(t *testing.T) {
	t.Parallel()
	pemText, _ := testAPNsKey(t)
	rsa := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}))
	for name, args := range map[string][]string{
		"missing":   {"", "K", "T", "app"},
		"not pem":   {"nope", "K", "T", "app"},
		"bad der":   {rsa, "K", "T", "app"},
		"no topic":  {pemText, "K", "T", ""},
		"no key id": {pemText, "", "T", "app"},
	} {
		if _, err := NewAPNsSender(args[0], args[1], args[2], args[3], false, nil); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := NewAPNsSender(pemText, "K", "T", "app", true, nil); err != nil {
		t.Fatalf("valid sender: %v", err)
	}
}

func TestAPNsSenderRequest(t *testing.T) {
	t.Parallel()
	pemText, pub := testAPNsKey(t)
	var gotPath, gotTopic, gotType, gotCollapse, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotTopic, gotType = r.URL.Path, r.Header.Get("apns-topic"), r.Header.Get("apns-push-type")
		gotCollapse, gotAuth = r.Header.Get("apns-collapse-id"), r.Header.Get("authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()
	sender, err := NewAPNsSender(pemText, "KEY1", "TEAM1", "com.example.app", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	sender.host = server.URL
	now := time.Unix(1_800_000_000, 0)
	sender.now = func() time.Time { return now }

	delivery := store.PushNotificationDelivery{Kind: "apns", Endpoint: "abcdef0123"}
	payload := store.PushNotificationPayload{Title: "Form", Body: "Answer me", URL: "/o/projects/K/forms/form-1", Tag: strings.Repeat("x", 100)}
	status, err := sender.Send(context.Background(), delivery, payload)
	if err != nil || status != http.StatusGone {
		t.Fatalf("send = %d, %v", status, err)
	}
	if gotPath != "/3/device/abcdef0123" || gotTopic != "com.example.app" || gotType != "alert" || len(gotCollapse) != 64 {
		t.Fatalf("headers path=%q topic=%q type=%q collapse=%q", gotPath, gotTopic, gotType, gotCollapse)
	}
	for _, want := range []string{`"title":"Form"`, `"body":"Answer me"`, `"url":"/o/projects/K/forms/form-1"`, `"sound":"default"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body missing %s: %s", want, gotBody)
		}
	}
	parsed, err := jwt.Parse(strings.TrimPrefix(gotAuth, "bearer "), func(*jwt.Token) (any, error) { return pub, nil }, jwt.WithValidMethods([]string{"ES256"}), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || parsed.Header["kid"] != "KEY1" {
		t.Fatalf("provider token %v header=%v", err, parsed)
	}
	if claims, _ := parsed.Claims.(jwt.MapClaims); claims["iss"] != "TEAM1" {
		t.Fatalf("claims = %v", claims)
	}

	// The provider token is reused until it nears Apple's one-hour limit.
	first := gotAuth
	now = now.Add(10 * time.Minute)
	_, _ = sender.Send(context.Background(), delivery, store.PushNotificationPayload{Title: "t"})
	if gotAuth != first {
		t.Fatal("token renewed too early")
	}
	if gotCollapse != "" {
		t.Fatalf("collapse id without a tag: %q", gotCollapse)
	}
	now = now.Add(time.Hour)
	_, _ = sender.Send(context.Background(), delivery, payload)
	if gotAuth == first {
		t.Fatal("token not renewed")
	}

	server.Close()
	if _, err := sender.Send(context.Background(), delivery, payload); err == nil {
		t.Fatal("want transport error")
	}
}

type recordingSender struct{ called int }

func (r *recordingSender) Send(context.Context, store.PushNotificationDelivery, store.PushNotificationPayload) (int, error) {
	r.called++
	return http.StatusCreated, nil
}

func TestRoutingSender(t *testing.T) {
	t.Parallel()
	web, apns := &recordingSender{}, &recordingSender{}
	router := RoutingSender{Web: web, APNs: apns}
	ctx := context.Background()
	_, _ = router.Send(ctx, store.PushNotificationDelivery{Kind: "web"}, store.PushNotificationPayload{})
	_, _ = router.Send(ctx, store.PushNotificationDelivery{Kind: "apns"}, store.PushNotificationPayload{})
	_, _ = router.Send(ctx, store.PushNotificationDelivery{Kind: "apns"}, store.PushNotificationPayload{})
	if web.called != 1 || apns.called != 2 {
		t.Fatalf("web=%d apns=%d", web.called, apns.called)
	}
	status, err := RoutingSender{}.Send(ctx, store.PushNotificationDelivery{Kind: "apns"}, store.PushNotificationPayload{})
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("unconfigured channel = %d, %v", status, err)
	}
}
