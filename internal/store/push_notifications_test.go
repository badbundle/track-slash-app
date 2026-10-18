package store

import (
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
)

func TestDefaultPushNotificationPreferences(t *testing.T) {
	t.Parallel()
	got := DefaultPushNotificationPreferences()
	if !got.Mentions || !got.Assignments || got.Comments || got.StatusChanges || got.DueDateChanges {
		t.Fatalf("default preferences = %+v", got)
	}
}

func TestNormalizePushSubscription(t *testing.T) {
	t.Parallel()
	valid := UpsertPushSubscriptionParams{
		UserID: uuid.New(), Endpoint: " https://push.example.test/subscription?id=1 ",
		P256DH:     base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
		AuthSecret: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), UserAgent: " Browser ",
	}
	endpoint, _, _, userAgent, err := normalizePushSubscription(valid)
	if err != nil || endpoint != "https://push.example.test/subscription?id=1" || userAgent != "Browser" {
		t.Fatalf("normalize valid = %q, %q, %v", endpoint, userAgent, err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*UpsertPushSubscriptionParams)
	}{
		{name: "HTTP endpoint", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "http://push.example.test" }},
		{name: "missing host", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://" }},
		{name: "credentials", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://user@push.example.test" }},
		{name: "localhost", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://LOCALHOST./push" }},
		{name: "localhost subdomain", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://push.localhost/push" }},
		{name: "port only", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://:8443/push" }},
		{name: "loopback", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://127.0.0.1:8443/push" }},
		{name: "IPv6 loopback", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://[::1]/push" }},
		{name: "private", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://10.0.0.8/push" }},
		{name: "metadata", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://169.254.169.254/latest" }},
		{name: "unique local", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://[fd00::1]/push" }},
		{name: "mapped private", mutate: func(p *UpsertPushSubscriptionParams) { p.Endpoint = "https://[::ffff:192.168.1.1]/push" }},
		{name: "long endpoint", mutate: func(p *UpsertPushSubscriptionParams) {
			p.Endpoint = "https://push.example.test/" + strings.Repeat("x", 4096)
		}},
		{name: "bad public key", mutate: func(p *UpsertPushSubscriptionParams) { p.P256DH = "invalid" }},
		{name: "short public key", mutate: func(p *UpsertPushSubscriptionParams) {
			p.P256DH = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
		}},
		{name: "bad auth", mutate: func(p *UpsertPushSubscriptionParams) { p.AuthSecret = "invalid" }},
		{name: "short auth", mutate: func(p *UpsertPushSubscriptionParams) {
			p.AuthSecret = base64.RawURLEncoding.EncodeToString(make([]byte, 15))
		}},
		{name: "long user agent", mutate: func(p *UpsertPushSubscriptionParams) { p.UserAgent = strings.Repeat("é", 501) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, _, _, _, err := normalizePushSubscription(candidate); err == nil {
				t.Fatal("normalizePushSubscription returned nil error")
			}
		})
	}
}

func TestPushEndpointAddressPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		addr string
		want bool
	}{
		{"8.8.8.8", true},
		{"142.250.180.10", true},
		{"2a00:1450:4009:81d::200a", true},
		{"::ffff:8.8.8.8", true},
		{"127.0.0.1", false},
		{"127.10.0.1", false},
		{"::1", false},
		{"::ffff:127.0.0.1", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"::", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.0.1", false},
		{"fd12:3456::1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fe80::1%eth0", false},
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"255.255.255.255", false},
		{"240.0.0.1", false},
	} {
		addr := netip.MustParseAddr(test.addr)
		if got := PushEndpointAddressAllowed(addr); got != test.want {
			t.Errorf("PushEndpointAddressAllowed(%s) = %v, want %v", test.addr, got, test.want)
		}
	}
	if PushEndpointAddressAllowed(netip.Addr{}) {
		t.Fatal("zero address allowed")
	}
	for host, want := range map[string]bool{
		"push.example.test": true, "8.8.8.8": true, "": false, "localhost": false,
		"Localhost.": false, "app.localhost": false, "192.168.1.1": false,
	} {
		if got := pushEndpointHostAllowed(host); got != want {
			t.Errorf("pushEndpointHostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestPushMentionUsernames(t *testing.T) {
	t.Parallel()
	got := pushMentionUsernames("@Ada please ask (@grace_hopper), @ada and mail ada@example.com; ignore @xy and x@hidden_user")
	if strings.Join(got, ",") != "ada,grace_hopper" {
		t.Fatalf("mentions = %v", got)
	}
	if got := pushMentionUsernames("none"); len(got) != 0 {
		t.Fatalf("empty mentions = %v", got)
	}
}

func TestPreferredPushNotificationCategory(t *testing.T) {
	t.Parallel()
	categories := map[PushNotificationCategory]bool{
		PushNotificationMentions:      true,
		PushNotificationAssignments:   true,
		PushNotificationComments:      true,
		PushNotificationStatusChanges: true,
	}
	if got, ok := preferredPushNotificationCategory(DefaultPushNotificationPreferences(), categories); !ok || got != PushNotificationMentions {
		t.Fatalf("default category = %q, %v", got, ok)
	}
	preferences := model.PushNotificationPreferences{Comments: true, StatusChanges: true}
	if got, ok := preferredPushNotificationCategory(preferences, categories); !ok || got != PushNotificationComments {
		t.Fatalf("broader category = %q, %v", got, ok)
	}
	if got, ok := preferredPushNotificationCategory(model.PushNotificationPreferences{}, categories); ok || got != "" {
		t.Fatalf("disabled category = %q, %v", got, ok)
	}
	if got, ok := preferredPushNotificationCategory(model.PushNotificationPreferences{DueDateChanges: true}, map[PushNotificationCategory]bool{PushNotificationDueDateChanges: true}); !ok || got != PushNotificationDueDateChanges {
		t.Fatalf("due date category = %q, %v", got, ok)
	}
}

func TestPushNotificationBodyAndTruncation(t *testing.T) {
	t.Parallel()
	details := model.ProjectChangelogDetails{Changes: []model.ProjectChangelogChange{
		{Field: "status", To: "Done"},
		{Field: "due_date", To: "2026-07-21"},
	}}
	for _, test := range []struct {
		category PushNotificationCategory
		actor    string
		want     string
	}{
		{PushNotificationMentions, "ada", "@ada mentioned you"},
		{PushNotificationAssignments, "ada", "@ada assigned this issue to you"},
		{PushNotificationComments, "", "Someone commented on this issue"},
		{PushNotificationStatusChanges, "", "Status changed to Done"},
		{PushNotificationDueDateChanges, "", "Due date changed to 2026-07-21"},
		{"other", "", "Issue updated"},
	} {
		if got := pushNotificationBody(test.category, test.actor, details); got != test.want {
			t.Fatalf("body %q = %q, want %q", test.category, got, test.want)
		}
	}
	if got := pushNotificationBody(PushNotificationStatusChanges, "", model.ProjectChangelogDetails{}); got != "Issue status changed" {
		t.Fatalf("status fallback = %q", got)
	}
	if got := pushNotificationBody(PushNotificationDueDateChanges, "", model.ProjectChangelogDetails{}); got != "Issue due date changed" {
		t.Fatalf("due fallback = %q", got)
	}
	if got := truncatePushNotificationText("  short   message ", 20); got != "short message" {
		t.Fatalf("short truncation = %q", got)
	}
	if got := truncatePushNotificationText(strings.Repeat("界", 5), 4); got != "界界界…" {
		t.Fatalf("unicode truncation = %q", got)
	}
}
