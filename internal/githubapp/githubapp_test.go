package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestJWTIsSignedAndHasTheRightClaims(t *testing.T) {
	k, pemBytes := testKey(t)
	parsed, err := ParseKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	tok, err := JWT("5912959", parsed, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT com %d partes", len(parts))
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if err := rsa.VerifyPKCS1v15(&k.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("assinatura inválida: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	_ = json.Unmarshal(raw, &claims)
	if claims.Iss != "5912959" || claims.Exp-claims.Iat > 600 || claims.Iat > now.Unix() {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestInstallationTokenAndPullRequest(t *testing.T) {
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Method+" "+r.URL.Path+" "+strings.Fields(r.Header.Get("Authorization"))[0])
		switch r.URL.Path {
		case "/app/installations/185577912/access_tokens":
			_, _ = w.Write([]byte(`{"token":"ghs_test"}`))
		case "/repos/o/r/pulls":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["head"] != "agent/x" || body["base"] != "main" {
				w.WriteHeader(422)
				return
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/o/r/pull/7"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	ctx := context.Background()
	tok, err := c.InstallationToken(ctx, "jwt", "185577912")
	if err != nil || tok != "ghs_test" {
		t.Fatalf("token: %q %v", tok, err)
	}
	pr, err := c.CreatePullRequest(ctx, tok, "o/r", "agent/x", "main", "t", "b")
	if err != nil || pr.Number != 7 {
		t.Fatalf("pr: %+v %v", pr, err)
	}
	want := []string{"POST /app/installations/185577912/access_tokens Bearer", "POST /repos/o/r/pulls token"}
	if strings.Join(gotAuth, "|") != strings.Join(want, "|") {
		t.Fatalf("auth = %v", gotAuth)
	}
}

func TestAPIErrorsCarryTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()
	_, err := Client{BaseURL: srv.URL}.InstallationToken(context.Background(), "jwt", "1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

func TestJobLogFollowsTheRedirectAndReadEndpoints(t *testing.T) {
	logs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("FAILED test_x\n"))
	}))
	defer logs.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/actions/jobs/42/logs":
			http.Redirect(w, r, logs.URL+"/signed", http.StatusFound)
		case "/repos/o/r/actions/jobs/43/logs":
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		case "/repos/o/r/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"state":"open","merged":false,"head":{"sha":"abc"}}`))
		case "/repos/o/r/commits/abc/check-runs":
			_, _ = w.Write([]byte(`{"check_runs":[{"id":42,"name":"test","status":"completed","conclusion":"failure"}]}`))
		case "/repos/o/r/pulls/4/reviews":
			_, _ = w.Write([]byte(`[{"id":9,"state":"CHANGES_REQUESTED","body":"b","user":{"login":"ana"}}]`))
		case "/repos/o/r/pulls/4/reviews/9/comments":
			_, _ = w.Write([]byte(`[{"path":"a.py","line":3,"body":"c"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c, ctx := Client{BaseURL: srv.URL}, context.Background()
	if l, err := c.JobLog(ctx, "tok", "o/r", 42); err != nil || l != "FAILED test_x\n" {
		t.Fatalf("log: %q %v", l, err)
	}
	if _, err := c.JobLog(ctx, "tok", "o/r", 43); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("sem permissão, o erro precisa dizer o status: %v", err)
	}
	pr, err := c.GetPullRequest(ctx, "tok", "o/r", 4)
	if err != nil || pr.Head.SHA != "abc" || pr.State != "open" {
		t.Fatalf("pr: %+v %v", pr, err)
	}
	checks, err := c.CheckRuns(ctx, "tok", "o/r", "abc")
	if err != nil || len(checks) != 1 || checks[0].Conclusion != "failure" {
		t.Fatalf("checks: %+v %v", checks, err)
	}
	reviews, err := c.Reviews(ctx, "tok", "o/r", 4)
	if err != nil || len(reviews) != 1 || reviews[0].User.Login != "ana" {
		t.Fatalf("reviews: %+v %v", reviews, err)
	}
	comments, err := c.ReviewComments(ctx, "tok", "o/r", 4, 9)
	if err != nil || len(comments) != 1 || *comments[0].Line != 3 {
		t.Fatalf("comentários: %+v %v", comments, err)
	}
}

func TestTokenSourceReusesAndRenewsTheToken(t *testing.T) {
	key, _ := testKey(t)
	issued, revoked := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			issued++
			_, _ = w.Write([]byte(`{"token":"ghs_` + strings.Repeat("x", issued) + `"}`))
		case r.Method == http.MethodDelete:
			revoked++
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	ts := &TokenSource{Client: Client{BaseURL: srv.URL}, AppID: "1", InstallationID: "2", Key: key, Now: func() time.Time { return now }}
	ctx := context.Background()
	a, _ := ts.Token(ctx)
	b, _ := ts.Token(ctx)
	if a != b || issued != 1 {
		t.Fatalf("dentro da validade, reaproveita: %q %q (%d emitidos)", a, b, issued)
	}
	now = now.Add(46 * time.Minute)
	c, _ := ts.Token(ctx)
	if c == a || issued != 2 || revoked != 1 {
		t.Fatalf("perto de expirar, troca e revoga o antigo: %q (%d emitidos, %d revogados)", c, issued, revoked)
	}
	ts.Revoke(ctx)
	if revoked != 2 {
		t.Fatalf("ao desligar, revoga: %d", revoked)
	}
}
