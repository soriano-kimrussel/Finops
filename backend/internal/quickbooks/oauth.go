package quickbooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const (
	AuthorizeURL = "https://appcenter.intuit.com/connect/oauth2"
	TokenURL     = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
	RevokeURL    = "https://developer.api.intuit.com/v2/oauth2/tokens/revoke"
	Scope        = "com.intuit.quickbooks.accounting"
)

type Token struct {
	AccessToken   string
	RefreshToken  string
	AccessExpiry  time.Time
	RefreshExpiry time.Time
}

type OAuth struct {
	Config    *oauth2.Config
	RevokeURL string
	HTTP      *http.Client
}

func NewOAuth(clientID, clientSecret, redirectURL string) *OAuth {
	return &OAuth{
		Config: &oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, Scopes: []string{Scope},
			Endpoint: oauth2.Endpoint{AuthURL: AuthorizeURL, TokenURL: TokenURL, AuthStyle: oauth2.AuthStyleInHeader},
		},
		RevokeURL: RevokeURL,
		HTTP:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (o *OAuth) AuthCodeURL(state string) string { return o.Config.AuthCodeURL(state) }

func (o *OAuth) ctx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, o.HTTP)
}

func toToken(t *oauth2.Token) Token {
	out := Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, AccessExpiry: t.Expiry}
	// Intuit returns the refresh token lifetime (about 100 days) in x_refresh_token_expires_in.
	switch v := t.Extra("x_refresh_token_expires_in").(type) {
	case float64:
		out.RefreshExpiry = time.Now().Add(time.Duration(v) * time.Second)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			out.RefreshExpiry = time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	if out.RefreshExpiry.IsZero() {
		out.RefreshExpiry = time.Now().Add(100 * 24 * time.Hour)
	}
	return out
}

func (o *OAuth) Exchange(ctx context.Context, code string) (Token, error) {
	t, err := o.Config.Exchange(o.ctx(ctx), code)
	if err != nil {
		return Token{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return toToken(t), nil
}

// Refresh trades a refresh token for a new access token. Intuit rotates refresh tokens, so the
// caller must persist the returned refresh token before using the access token.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	t, err := o.Config.TokenSource(o.ctx(ctx), &oauth2.Token{RefreshToken: refreshToken, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusBadRequest {
			return Token{}, ErrUnauthorized
		}
		return Token{}, &TransientError{Err: fmt.Errorf("refresh access token: %w", err)}
	}
	return toToken(t), nil
}

func (o *OAuth) Revoke(ctx context.Context, token string) error {
	body, _ := json.Marshal(map[string]string{"token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.RevokeURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(o.Config.ClientID, o.Config.ClientSecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke returned %d", res.StatusCode)
	}
	return nil
}

// VerifyWebhook checks the intuit-signature header: base64(HMAC-SHA256(body, verifier token)).
func VerifyWebhook(body []byte, signature, verifierToken string) bool {
	if verifierToken == "" || signature == "" {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(verifierToken))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

type WebhookPayload struct {
	EventNotifications []struct {
		RealmID         string `json:"realmId"`
		DataChangeEvent struct {
			Entities []struct {
				Name      string `json:"name"`
				ID        string `json:"id"`
				Operation string `json:"operation"`
			} `json:"entities"`
		} `json:"dataChangeEvent"`
	} `json:"eventNotifications"`
}
