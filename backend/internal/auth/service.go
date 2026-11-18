package auth

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/store"
)

const (
	SessionIdleTimeout     = 7 * 24 * time.Hour
	SessionAbsoluteTimeout = 30 * 24 * time.Hour
	sessionTouchInterval   = 5 * time.Minute
	MinPasswordLength      = 12
	MaxPasswordLength      = 128
)

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

type Service struct {
	pool    *pgxpool.Pool
	limiter *attemptLimiter
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, limiter: newAttemptLimiter(10, 15*time.Minute), now: time.Now}
}

type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type ClientInfo struct {
	IP        string
	UserAgent string
}

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validatePassword(v *apperr.Validation, pw string) {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLength {
		v.Add("password", "must be at least 12 characters")
	} else if n > MaxPasswordLength {
		v.Add("password", "must be at most 128 characters")
	}
}

func ValidEmail(s string) bool {
	if len(s) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && strings.Contains(s[strings.LastIndex(s, "@"):], ".")
}

func (s *Service) Register(ctx context.Context, in RegisterInput, ci ClientInfo) (User, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = NormalizeEmail(in.Email)
	var v apperr.Validation
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 200 {
		v.Add("name", "is required (max 200 characters)")
	}
	if !ValidEmail(in.Email) {
		v.Add("email", "must be a valid email address")
	}
	validatePassword(&v, in.Password)
	if err := v.Err(); err != nil {
		return User{}, "", err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return User{}, "", err
	}
	q := store.New(s.pool)
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: in.Email, Name: in.Name, PasswordHash: hash})
	if apperr.IsUniqueViolation(err, "users_email_key") {
		return User{}, "", &apperr.Error{Status: http.StatusConflict, Code: "email_taken", Message: "an account with this email already exists",
			Fields: map[string]string{"email": "is already registered"}}
	}
	if err != nil {
		return User{}, "", err
	}
	token, err := s.createSession(ctx, q, u.ID, ci)
	if err != nil {
		return User{}, "", err
	}
	return toUser(u), token, nil
}

var errInvalidCredentials = apperr.New(http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")

func (s *Service) Login(ctx context.Context, email, password string, ci ClientInfo) (User, string, error) {
	email = NormalizeEmail(email)
	limiterKey := ci.IP + "|" + email
	if !s.limiter.Allow(limiterKey) {
		return User{}, "", apperr.New(http.StatusTooManyRequests, "too_many_attempts", "too many login attempts, try again later")
	}

	q := store.New(s.pool)
	u, err := q.GetUserByEmail(ctx, email)
	if err != nil && !apperr.IsNoRows(err) {
		return User{}, "", err
	}
	hash := dummyHash
	if err == nil {
		hash = u.PasswordHash
	}
	ok, verr := VerifyPassword(password, hash)
	if verr != nil {
		return User{}, "", verr
	}
	if err != nil || !ok || !u.IsActive {
		return User{}, "", errInvalidCredentials
	}
	s.limiter.Reset(limiterKey)

	token, err := s.createSession(ctx, q, u.ID, ci)
	if err != nil {
		return User{}, "", err
	}
	return toUser(u), token, nil
}

func (s *Service) createSession(ctx context.Context, q *store.Queries, userID uuid.UUID, ci ClientInfo) (string, error) {
	raw, hash, err := NewSessionToken()
	if err != nil {
		return "", err
	}
	ua := ci.UserAgent
	if len(ua) > 400 {
		ua = ua[:400]
	}
	_, err = q.CreateSession(ctx, store.CreateSessionParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: s.now().Add(SessionIdleTimeout),
		Ip:        strPtr(ci.IP),
		UserAgent: strPtr(ua),
	})
	return raw, err
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	return store.New(s.pool).DeleteSession(ctx, HashToken(rawToken))
}

var ErrNoSession = errors.New("no valid session")

// Authenticate resolves a session token to its user, sliding the idle expiry forward but never
// past the absolute lifetime of the session.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (User, error) {
	if rawToken == "" {
		return User{}, ErrNoSession
	}
	q := store.New(s.pool)
	row, err := q.GetSessionWithUser(ctx, HashToken(rawToken))
	if apperr.IsNoRows(err) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, err
	}
	now := s.now()
	if now.Sub(row.Session.LastSeenAt) > sessionTouchInterval {
		exp := now.Add(SessionIdleTimeout)
		if limit := row.Session.CreatedAt.Add(SessionAbsoluteTimeout); exp.After(limit) {
			exp = limit
		}
		if err := q.TouchSession(ctx, store.TouchSessionParams{ID: row.Session.ID, ExpiresAt: exp}); err != nil {
			return User{}, err
		}
	}
	return toUser(row.User), nil
}

func toUser(u store.User) User { return User{ID: u.ID, Email: u.Email, Name: u.Name} }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
