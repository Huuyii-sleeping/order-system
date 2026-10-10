package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/domain/identity"
)

func TestIdentityStorePruneExpiredCredentialsInBoundedBatches(t *testing.T) {
	store := NewIdentityStore()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	expiredSession, err := identity.NewSession(
		strings.Repeat("a", 64), "user-1", now.Add(-2*time.Hour), now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	activeSession, err := identity.NewSession(
		strings.Repeat("b", 64), "user-1", now, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("create active session: %v", err)
	}
	expiredRefresh, err := identity.NewRefreshToken(
		strings.Repeat("c", 64), "user-1", "family-1", now.Add(-2*time.Hour), now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf("create expired refresh token: %v", err)
	}
	activeRefresh, err := identity.NewRefreshToken(
		strings.Repeat("d", 64), "user-1", "family-2", now, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("create active refresh token: %v", err)
	}
	for _, session := range []identity.Session{expiredSession, activeSession} {
		if err := store.CreateSession(context.Background(), session); err != nil {
			t.Fatalf("CreateSession() error = %v", err)
		}
	}
	for _, token := range []identity.RefreshToken{expiredRefresh, activeRefresh} {
		if err := store.CreateRefreshToken(context.Background(), token); err != nil {
			t.Fatalf("CreateRefreshToken() error = %v", err)
		}
	}

	sessionsDeleted, refreshTokensDeleted, err := store.PruneExpiredCredentials(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("first PruneExpiredCredentials() error = %v", err)
	}
	if sessionsDeleted != 1 || refreshTokensDeleted != 1 {
		t.Fatalf("first deleted counts = %d, %d; want one of each", sessionsDeleted, refreshTokensDeleted)
	}
	secondSessionsDeleted, secondRefreshTokensDeleted, err := store.PruneExpiredCredentials(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("second PruneExpiredCredentials() error = %v", err)
	}
	if secondSessionsDeleted != 0 || secondRefreshTokensDeleted != 0 {
		t.Fatalf("second deleted counts = %d, %d; want zero", secondSessionsDeleted, secondRefreshTokensDeleted)
	}

	if _, err := store.GetSessionByTokenHash(context.Background(), expiredSession.TokenHash(), now); !errors.Is(err, identity.ErrSessionNotFound) {
		t.Fatalf("expired session lookup error = %v, want not found", err)
	}
	if _, err := store.GetSessionByTokenHash(context.Background(), activeSession.TokenHash(), now); err != nil {
		t.Fatalf("active session lookup error = %v", err)
	}
	if _, err := store.GetRefreshTokenByHash(context.Background(), expiredRefresh.TokenHash()); !errors.Is(err, identity.ErrRefreshTokenNotFound) {
		t.Fatalf("expired refresh lookup error = %v, want not found", err)
	}
	if _, err := store.GetRefreshTokenByHash(context.Background(), activeRefresh.TokenHash()); err != nil {
		t.Fatalf("active refresh lookup error = %v", err)
	}
}
