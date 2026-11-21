package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func TestAPNsDeviceRegistration(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	suffix := strings.ToLower(uniqueProjectKey(t))
	first, err := env.store.CreateUserProfile(env.ctx, "apnsone"+suffix, "apns-one@example.com", "Apns One")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.store.CreateUserProfile(env.ctx, "apnstwo"+suffix, "apns-two@example.com", "Apns Two")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("ab", 32)

	device, err := env.store.UpsertAPNsDevice(env.ctx, first.ID, " "+strings.ToUpper(token)+" ", "Bradley's iPhone")
	if err != nil || device.Endpoint != token || device.UserID != first.ID || device.DisabledAt != nil {
		t.Fatalf("register = %+v, %v", device, err)
	}
	if active, err := env.store.PushSubscriptionActive(env.ctx, first.ID, token); err != nil || !active {
		t.Fatalf("active = %v, %v", active, err)
	}

	// The token follows the person who registered it last.
	moved, err := env.store.UpsertAPNsDevice(env.ctx, second.ID, token, "")
	if err != nil || moved.ID != device.ID || moved.UserID != second.ID {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	if active, _ := env.store.PushSubscriptionActive(env.ctx, first.ID, token); active {
		t.Fatal("first user still holds the token")
	}

	// Disabling then registering again revives the row.
	if err := env.store.DisablePushSubscription(env.ctx, second.ID, token); err != nil {
		t.Fatal(err)
	}
	if revived, err := env.store.UpsertAPNsDevice(env.ctx, second.ID, token, ""); err != nil || revived.DisabledAt != nil {
		t.Fatalf("revived = %+v, %v", revived, err)
	}

	for name, args := range map[string]string{"short": "abc", "not hex": strings.Repeat("zz", 32), "empty": ""} {
		if _, err := env.store.UpsertAPNsDevice(env.ctx, first.ID, args, ""); !errors.Is(err, store.ErrConflict) {
			t.Errorf("%s token err = %v", name, err)
		}
	}
	if _, err := env.store.UpsertAPNsDevice(env.ctx, first.ID, token, strings.Repeat("n", 501)); !errors.Is(err, store.ErrConflict) {
		t.Errorf("long name err = %v", err)
	}
	if _, err := env.store.UpsertAPNsDevice(env.ctx, uuid.New(), strings.Repeat("cd", 32), ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown user err = %v", err)
	}
}
