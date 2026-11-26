// Package dockerapi é um cliente mínimo da API do Docker Engine pelo socket unix, sem dependências.
package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const apiVersion = "v1.47"

// Client fala com o Docker Engine.
type Client struct{ http *http.Client }

// New cria um cliente para o socket informado (padrão /var/run/docker.sock).
func New(socket string) *Client {
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: tr}}
}

// Spec descreve o container de um run.
type Spec struct {
	Name   string
	Image  string
	Cmd    []string
	Env    []string
	Labels map[string]string
	Memory int64 // bytes
	Pids   int64
}

// Create cria o container endurecido: sem capabilities, sem novos privilégios, com limites.
func (c *Client) Create(ctx context.Context, s Spec) (string, error) {
	body := map[string]any{
		"Image":  s.Image,
		"Cmd":    s.Cmd,
		"Env":    s.Env,
		"Labels": s.Labels,
		"HostConfig": map[string]any{
			"CapDrop":     []string{"ALL"},
			"SecurityOpt": []string{"no-new-privileges"},
			"Memory":      s.Memory,
			"PidsLimit":   s.Pids,
			"NetworkMode": "bridge",
		},
	}
	var out struct {
		ID string `json:"Id"`
	}
	err := c.do(ctx, http.MethodPost, "/containers/create?name="+s.Name, body, &out)
	return out.ID, err
}

// Start inicia o container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil)
}

// Wait bloqueia até o container parar e devolve o código de saída.
func (c *Client) Wait(ctx context.Context, id string) (int, error) {
	var out struct {
		StatusCode int `json:"StatusCode"`
	}
	err := c.do(ctx, http.MethodPost, "/containers/"+id+"/wait?condition=not-running", nil, &out)
	return out.StatusCode, err
}

// Kill mata o container.
func (c *Client) Kill(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+id+"/kill", nil, nil)
}

// Remove apaga o container.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/containers/"+id+"?force=1", nil, nil)
}

// Logs devolve stdout e stderr do container (até tail linhas).
func (c *Client) Logs(ctx context.Context, id string, tail int) (string, error) {
	raw, err := c.raw(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&tail=%d", id, tail))
	if err != nil {
		return "", err
	}
	return Demux(raw), nil
}

// CopyOut copia um diretório do container para dir local, só arquivos regulares e sem escapar de dir.
func (c *Client) CopyOut(ctx context.Context, id, src, dir string) error {
	raw, err := c.raw(ctx, http.MethodGet, "/containers/"+id+"/archive?path="+src)
	if err != nil {
		return err
	}
	return ExtractTar(bytes.NewReader(raw), dir, 10<<20)
}

// Demux separa o fluxo multiplexado de logs (cabeçalho de 8 bytes por quadro) num texto só.
func Demux(raw []byte) string {
	var b strings.Builder
	for len(raw) >= 8 {
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		raw = raw[8:]
		if size > len(raw) {
			size = len(raw)
		}
		b.Write(raw[:size])
		raw = raw[size:]
	}
	return b.String()
}

// ExtractTar grava os arquivos regulares do tar em dir pelo nome base, com limite total de bytes.
func ExtractTar(r io.Reader, dir string, limit int64) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Base(h.Name)
		if name == "." || name == "/" || strings.HasPrefix(name, ".") {
			continue
		}
		total += h.Size
		if total > limit {
			return fmt.Errorf("saída do container passa de %d bytes", limit)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		f.Close()
		if err != nil {
			return err
		}
	}
}

func (c *Client) do(ctx context.Context, method, p string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+p, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("docker %s %s: HTTP %d: %s", method, p, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) raw(ctx context.Context, method, p string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+p, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("docker %s %s: HTTP %d: %s", method, p, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
