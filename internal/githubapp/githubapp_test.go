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
