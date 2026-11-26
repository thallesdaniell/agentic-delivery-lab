// Package githubapp autentica como GitHub App e fala com a API do GitHub.
// Sem dependências externas: o JWT RS256 é assinado com a biblioteca padrão.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client fala com a API do GitHub.
type Client struct {
	BaseURL string // padrão https://api.github.com
	HTTP    *http.Client
}

func (c Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.github.com"
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ParseKey lê uma chave privada RSA em PEM (PKCS#1 ou PKCS#8).
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("chave privada: PEM inválido")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("chave privada: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("chave privada: não é RSA")
	}
	return rk, nil
}

// JWT gera o token do App, válido por 9 minutos (o GitHub aceita até 10).
func JWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// InstallationToken troca o JWT por um token de instalação (expira em 1 hora).
func (c Client) InstallationToken(ctx context.Context, jwt, installationID string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/app/installations/"+installationID+"/access_tokens", "Bearer "+jwt, nil, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("o GitHub não devolveu token de instalação")
	}
	return out.Token, nil
}

// RevokeToken invalida o token de instalação antes de ele expirar.
func (c Client) RevokeToken(ctx context.Context, token string) error {
	return c.do(ctx, http.MethodDelete, "/installation/token", "token "+token, nil, nil)
}

// BotIdentity devolve o nome e o e-mail noreply do usuário bot do App, para assinar os commits.
func (c Client) BotIdentity(ctx context.Context, jwt string) (name, email string, err error) {
	var app struct {
		Slug string `json:"slug"`
	}
	if err := c.do(ctx, http.MethodGet, "/app", "Bearer "+jwt, nil, &app); err != nil {
		return "", "", err
	}
	name = app.Slug + "[bot]"
	var user struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "/users/"+name, "", nil, &user); err != nil {
		return "", "", err
	}
	return name, strconv.FormatInt(user.ID, 10) + "+" + name + "@users.noreply.github.com", nil
}

// PullRequest é o que o publisher precisa saber do PR criado.
type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

// CreatePullRequest abre o PR da branch do card contra a branch padrão.
func (c Client) CreatePullRequest(ctx context.Context, token, repo, head, base, title, body string) (PullRequest, error) {
	var pr PullRequest
	req := map[string]any{"title": title, "head": head, "base": base, "body": body}
	err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls", "token "+token, req, &pr)
	return pr, err
}

// DefaultBranch devolve a branch padrão do repositório.
func (c Client) DefaultBranch(ctx context.Context, token, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo, "token "+token, nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

func (c Client) do(ctx context.Context, method, path, auth string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agentic-delivery-lab")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
