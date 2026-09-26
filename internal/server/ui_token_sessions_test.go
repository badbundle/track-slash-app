package server

import (
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestUIPartitionAuthTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	all := []model.AuthToken{
		{Kind: model.AuthTokenKindAPI, Name: "deploy bot"},
		{Kind: model.AuthTokenKindSession, Name: "live", ExpiresAt: &future},
		{Kind: model.AuthTokenKindSession, Name: "never expires"},
		{Kind: model.AuthTokenKindSession, Name: "expired", ExpiresAt: &past},
		{Kind: model.AuthTokenKindSession, Name: "revoked", ExpiresAt: &future, RevokedAt: &past},
		{Kind: model.AuthTokenKindAPI, Name: "revoked bot", RevokedAt: &past},
		{Kind: model.AuthTokenKindOAuth, Name: "connector", ExpiresAt: &future},
		{Kind: model.AuthTokenKindOAuth, Name: "connector expired", ExpiresAt: &past},
		{Kind: model.AuthTokenKindOAuth, Name: "connector revoked", ExpiresAt: &future, RevokedAt: &past},
	}

	tokens, activeSessions, connectedApps := uiPartitionAuthTokens(all, now)

	if activeSessions != 2 {
		t.Fatalf("activeSessions = %d, want 2", activeSessions)
	}
	// Connector access tokens are counted, never listed: they are reissued on
	// the connector's own schedule and would otherwise churn through this list.
	if connectedApps != 1 {
		t.Fatalf("connectedApps = %d, want 1", connectedApps)
	}
	// Revoked API tokens can never be used again, so they drop out of the list.
	if len(tokens) != 1 || tokens[0].Name != "deploy bot" {
		t.Fatalf("tokens = %+v, want only deploy bot", tokens)
	}
}

func TestUIPartitionAuthTokensWithoutSessions(t *testing.T) {
	t.Parallel()

	_, activeSessions, connectedApps := uiPartitionAuthTokens(nil, time.Now())
	if activeSessions != 0 {
		t.Fatalf("activeSessions = %d, want 0", activeSessions)
	}
	if connectedApps != 0 {
		t.Fatalf("connectedApps = %d, want 0", connectedApps)
	}
}
