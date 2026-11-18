package qbsync

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/config"
	"finopshq/internal/quickbooks"
	"finopshq/internal/secrets"
)

// Build wires the service and worker from configuration. Live QuickBooks is only enabled when
// QUICKBOOKS_MODE=live and credentials are present; otherwise the simulated provider is used.
func Build(cfg config.Config, pool *pgxpool.Pool) (*Service, *Worker, error) {
	box, err := secrets.New(cfg.TokenEncryptionKey)
	if err != nil {
		return nil, nil, err
	}
	svc := &Service{Pool: pool, Mode: cfg.QuickBooks.Mode, Environment: cfg.QuickBooks.Environment, Box: box,
		WebhookSecret: cfg.QuickBooks.WebhookVerifierToken}
	w := &Worker{Pool: pool, Simulated: &quickbooks.Simulated{Snapshot: SnapshotFunc(pool)}}
	if cfg.QuickBooks.Mode == quickbooks.ModeLive {
		oauth := quickbooks.NewOAuth(cfg.QuickBooks.ClientID, cfg.QuickBooks.ClientSecret, cfg.QuickBooks.RedirectURL)
		svc.OAuth = oauth
		w.Live = quickbooks.NewClient(cfg.QuickBooks.Environment)
		w.Tokens = &TokenManager{Pool: pool, Box: box, Refresher: oauth}
	}
	svc.Kick = w.Notify
	return svc, w, nil
}
