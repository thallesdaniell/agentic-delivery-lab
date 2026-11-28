// Package gitops faz as operações de git do worker e do publisher.
// O token do GitHub nunca é gravado em .git/config: ele vai só no ambiente do processo git do push.
package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ForbiddenPrefixes são caminhos que o agente não pode alterar.
var ForbiddenPrefixes = []string{".github/"}

// Git roda git num diretório com um ambiente mínimo.
type Git struct {
	Dir   string
	extra []string
}

func (g Git) run(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"GIT_TERMINAL_PROMPT=0",
	}, g.extra...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Clone faz um clone raso de um repositório público.
func Clone(ctx context.Context, repo, dir string) (Git, error) {
	g := Git{Dir: "/"}
	if _, err := g.run(ctx, nil, "clone", "--depth", "1", "https://github.com/"+repo+".git", dir); err != nil {
		return Git{}, err
	}
	return Git{Dir: dir}, nil
}

// CloneBranch faz um clone raso de um branch de um repositório público: o run de correção
// trabalha em cima do que já está no PR.
func CloneBranch(ctx context.Context, repo, branch, dir string) (Git, error) {
	g := Git{Dir: "/"}
	if _, err := g.run(ctx, nil, "clone", "--depth", "1", "--branch", branch, "https://github.com/"+repo+".git", dir); err != nil {
		return Git{}, err
	}
	return Git{Dir: dir}, nil
}

// StagedPatch adiciona tudo e devolve o patch binário e os arquivos alterados.
func (g Git) StagedPatch(ctx context.Context) ([]byte, []string, error) {
	if _, err := g.run(ctx, nil, "add", "-A"); err != nil {
		return nil, nil, err
	}
	names, err := g.run(ctx, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, nil, err
	}
	patch, err := g.run(ctx, nil, "diff", "--cached", "--binary")
	if err != nil {
		return nil, nil, err
	}
	return []byte(patch), splitLines(names), nil
}

// Apply aplica um patch e devolve os arquivos alterados.
func (g Git) Apply(ctx context.Context, patch []byte) ([]string, error) {
	if _, err := g.run(ctx, patch, "apply", "--index", "--whitespace=nowarn", "-"); err != nil {
		return nil, err
	}
	names, err := g.run(ctx, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	return splitLines(names), nil
}

// Commit cria a branch e o commit com a identidade informada.
func (g Git) Commit(ctx context.Context, branch, name, email, message string) error {
	if _, err := g.run(ctx, nil, "checkout", "-b", branch); err != nil {
		return err
	}
	return g.CommitHere(ctx, name, email, message)
}

// CommitHere faz o commit no branch atual (o do PR, num run de correção).
func (g Git) CommitHere(ctx context.Context, name, email, message string) error {
	_, err := g.run(ctx, nil, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-m", message)
	return err
}

// Push envia a branch sem force: num PR que já existe, só avança se ninguém mexeu nele no meio.
func (g Git) Push(ctx context.Context, branch, token string) error {
	return g.push(ctx, "HEAD:refs/heads/"+branch, token)
}

// ForcePush substitui a branch. Só para reimplementar um card cujo PR foi fechado sem merge:
// a branch é do agente (agent/<card>) e o PR antigo não volta.
func (g Git) ForcePush(ctx context.Context, branch, token string) error {
	return g.push(ctx, "+HEAD:refs/heads/"+branch, token)
}

// push usa o token só no ambiente deste processo git.
func (g Git) push(ctx context.Context, refspec, token string) error {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	withToken := Git{Dir: g.Dir, extra: []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic " + basic,
	}}
	_, err := withToken.run(ctx, nil, "push", "origin", refspec)
	if err != nil {
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), basic, "***"))
	}
	return nil
}

// ForbiddenPaths devolve os arquivos alterados que o agente não podia tocar.
func ForbiddenPaths(files []string) []string {
	var bad []string
	for _, f := range files {
		for _, p := range ForbiddenPrefixes {
			if strings.HasPrefix(f, p) {
				bad = append(bad, f)
			}
		}
	}
	return bad
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
