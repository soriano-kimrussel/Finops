// Package apitest runs the real HTTP router against a throwaway database for integration tests.
package apitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"finopshq/internal/config"
	"finopshq/internal/server"
	"finopshq/internal/testdb"
)

const baseURL = "http://finops.test"

type Env struct {
	T       testing.TB
	DB      *testdb.DB
	Handler http.Handler
	Config  config.Config
}

func New(t testing.TB) *Env {
	return NewWithConfig(t, nil)
}

// NewWithConfig lets a test adjust the configuration before the router is built.
func NewWithConfig(t testing.TB, adjust func(*config.Config)) *Env {
	t.Helper()
	db := testdb.New(t)
	cfg := config.Config{
		Env:                "test",
		FrontendOrigin:     "http://localhost:5173",
		TokenEncryptionKey: bytes.Repeat([]byte{7}, 32),
		QuickBooks:         config.QuickBooks{Mode: "simulated", Environment: "sandbox"},
	}
	if adjust != nil {
		adjust(&cfg)
	}
	return &Env{T: t, DB: db, Handler: server.New(server.Deps{Config: cfg, Pool: db.App}), Config: cfg}
}

// handlerTransport serves requests in memory. It avoids loopback sockets, whose close can
// hang on some Windows network stacks, and keeps tests fast.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	req.RemoteAddr = "127.0.0.1:50000"
	t.h.ServeHTTP(rec, req)
	res := rec.Result()
	res.Request = req
	return res, nil
}

type Client struct {
	env  *Env
	http *http.Client
}

func (e *Env) Client() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{env: e, http: &http.Client{Jar: jar, Transport: handlerTransport{e.Handler}}}
}

type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r Response) JSON(t testing.TB, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, dst); err != nil {
		t.Fatalf("decode response %q: %v", r.Body, err)
	}
}

func (r Response) Map(t testing.TB) map[string]any {
	var m map[string]any
	r.JSON(t, &m)
	return m
}

func (r Response) ErrorCode(t testing.TB) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.JSON(t, &body)
	return body.Error.Code
}

func (c *Client) Do(method, path string, body any, headers ...string) Response {
	c.env.T.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.env.T.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		c.env.T.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "finops")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.env.T.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Response{Status: res.StatusCode, Body: b, Header: res.Header}
}

func (c *Client) Get(path string) Response { return c.Do(http.MethodGet, path, nil) }

func (c *Client) Post(path string, body any, headers ...string) Response {
	return c.Do(http.MethodPost, path, body, headers...)
}

func (c *Client) Patch(path string, body any) Response { return c.Do(http.MethodPatch, path, body) }

func (c *Client) Delete(path string) Response { return c.Do(http.MethodDelete, path, nil) }

// Must asserts the status code and returns the response.
func (c *Client) Must(status int, r Response) Response {
	c.env.T.Helper()
	if r.Status != status {
		c.env.T.Fatalf("expected status %d, got %d: %s", status, r.Status, r.Body)
	}
	return r
}

var counter int

// Register creates a new user and keeps the session cookie on the client.
func (c *Client) Register(name string) (email string) {
	c.env.T.Helper()
	counter++
	email = fmt.Sprintf("%s.%d@example.test", strings.ToLower(strings.ReplaceAll(name, " ", ".")), counter)
	c.Must(http.StatusCreated, c.Post("/api/auth/register", map[string]string{
		"name": name, "email": email, "password": "correct horse battery staple",
	}))
	return email
}

// CreateOrg creates an organization owned by the client's user and returns its id.
func (c *Client) CreateOrg(name, currency string) string {
	c.env.T.Helper()
	res := c.Must(http.StatusCreated, c.Post("/api/orgs", map[string]string{
		"name": name, "base_currency": currency, "timezone": "America/New_York",
	}))
	var body struct {
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	res.JSON(c.env.T, &body)
	return body.Organization.ID
}

// AddMember adds a registered email to the org with a role, using this client's session.
func (c *Client) AddMember(orgID, email, role string) string {
	c.env.T.Helper()
	res := c.Must(http.StatusCreated, c.Post("/api/orgs/"+orgID+"/members", map[string]string{"email": email, "role": role}))
	var body struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	res.JSON(c.env.T, &body)
	return body.Member.ID
}
