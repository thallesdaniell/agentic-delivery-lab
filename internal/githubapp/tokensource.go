package githubapp

import (
	"context"
	"crypto/rsa"
	"sync"
	"time"
)

// TokenSource guarda um token de instalação e troca por um novo antes de ele expirar.
// O token do GitHub vale 1 hora; aqui ele é renovado depois de 45 minutos.
type TokenSource struct {
	Client         Client
	AppID          string
	InstallationID string
	Key            *rsa.PrivateKey
	Now            func() time.Time

	mu      sync.Mutex
	token   string
	created time.Time
}

const tokenTTL = 45 * time.Minute

func (s *TokenSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Token devolve um token válido, pedindo um novo quando o atual está perto de expirar.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.now().Sub(s.created) < tokenTTL {
		return s.token, nil
	}
	if s.token != "" {
		_ = s.Client.RevokeToken(ctx, s.token)
		s.token = ""
	}
	jwt, err := JWT(s.AppID, s.Key, s.now())
	if err != nil {
		return "", err
	}
	tok, err := s.Client.InstallationToken(ctx, jwt, s.InstallationID)
	if err != nil {
		return "", err
	}
	s.token, s.created = tok, s.now()
	return tok, nil
}

// Revoke invalida o token guardado, se houver. Chamado ao desligar.
func (s *TokenSource) Revoke(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		_ = s.Client.RevokeToken(ctx, s.token)
		s.token = ""
	}
}
