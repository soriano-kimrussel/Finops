package qbsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/quickbooks"
	"finopshq/internal/secrets"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

// Refresher is the subset of quickbooks.OAuth the token manager needs; tests substitute a fake.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (quickbooks.Token, error)
}

type TokenManager struct {
	Pool      *pgxpool.Pool
	Box       *secrets.Box
	Refresher Refresher
	Now       func() time.Time
}

const refreshMargin = 2 * time.Minute

func (m *TokenManager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *TokenManager) seal(orgID uuid.UUID, v string) ([]byte, error) {
	return m.Box.Seal([]byte(v), orgID[:])
}

func (m *TokenManager) open(orgID uuid.UUID, ct []byte) (string, error) {
	if len(ct) == 0 {
		return "", errors.New("no stored token")
	}
	b, err := m.Box.Open(ct, orgID[:])
	return string(b), err
}

// AccessToken returns a valid access token, refreshing it first when it is about to expire or
// when force is set (after a 401). The connection row is locked during the refresh so two
// workers never refresh at once: Intuit rotates refresh tokens and a lost update would strand
// the connection.
func (m *TokenManager) AccessToken(ctx context.Context, orgID, connectionID uuid.UUID, force bool) (string, error) {
	var access string
	var needsReauth bool
	err := tenant.WithTx(ctx, m.Pool, tenant.Scope{OrgID: orgID}, func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.GetConnectionForUpdate(ctx, connectionID)
		if err != nil {
			return err
		}
		if !force && c.AccessTokenExpiresAt != nil && c.AccessTokenExpiresAt.After(m.now().Add(refreshMargin)) {
			access, err = m.open(orgID, c.AccessTokenEnc)
			return err
		}
		refresh, err := m.open(orgID, c.RefreshTokenEnc)
		if err != nil {
			return fmt.Errorf("decrypt refresh token: %w", err)
		}
		tok, err := m.Refresher.Refresh(ctx, refresh)
		if errors.Is(err, quickbooks.ErrUnauthorized) {
			needsReauth = true
			return err
		}
		if err != nil {
			return err
		}
		accessEnc, err := m.seal(orgID, tok.AccessToken)
		if err != nil {
			return err
		}
		refreshEnc, err := m.seal(orgID, tok.RefreshToken)
		if err != nil {
			return err
		}
		if err := q.UpdateConnectionTokens(ctx, store.UpdateConnectionTokensParams{ID: connectionID, AccessTokenEnc: accessEnc, RefreshTokenEnc: refreshEnc,
			AccessTokenExpiresAt: &tok.AccessExpiry, RefreshTokenExpiresAt: &tok.RefreshExpiry}); err != nil {
			return err
		}
		access = tok.AccessToken
		return nil
	})
	if needsReauth {
		markNeedsReauth(ctx, m.Pool, orgID, connectionID, "QuickBooks refused the refresh token; reconnect the integration")
	}
	return access, err
}

func markNeedsReauth(ctx context.Context, pool *pgxpool.Pool, orgID, connectionID uuid.UUID, msg string) {
	_ = tenant.WithTx(ctx, pool, tenant.Scope{OrgID: orgID}, func(tx pgx.Tx) error {
		return store.New(tx).SetConnectionStatus(ctx, store.SetConnectionStatusParams{ID: connectionID, Status: "needs_reauth", LastError: &msg})
	})
}
