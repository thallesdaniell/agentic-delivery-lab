package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestForbiddenPaths(t *testing.T) {
	got := ForbiddenPaths([]string{"src/app.py", ".github/workflows/ci.yml", "tests/test_x.py"})
	if len(got) != 1 || got[0] != ".github/workflows/ci.yml" {
		t.Fatalf("got %v", got)
	}
}

// Testa o ciclo patch -> apply -> commit num repositório local, sem rede.
func TestPatchRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	origin := t.TempDir()
	mustGit(t, origin, "init", "-q", "-b", "main")
	write(t, origin, "app.py", "print('a')\n")
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")

	work := t.TempDir()
	mustGit(t, "/", "clone", "-q", origin, work)
	write(t, work, "app.py", "print('b')\n")
	write(t, work, "new.py", "x = 1\n")
	patch, files, err := Git{Dir: work}.StagedPatch(ctx)
	if err != nil || len(files) != 2 {
		t.Fatalf("patch: %v %v", files, err)
	}

	pub := t.TempDir()
	mustGit(t, "/", "clone", "-q", origin, pub)
	g := Git{Dir: pub}
	applied, err := g.Apply(ctx, patch)
	if err != nil || len(applied) != 2 {
		t.Fatalf("apply: %v %v", applied, err)
	}
	if err := g.Commit(ctx, "agent/x", "bot", "bot@example.com", "msg"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(pub, "app.py")); string(b) != "print('b')\n" {
		t.Fatalf("conteúdo: %q", b)
	}
}

// Correção avança o branch do PR sem force; um push com o branch desatualizado é recusado,
// e só o ForcePush (card reimplementado depois de o PR fechar) substitui o branch.
func TestPushNeverRewritesThePRBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	origin := t.TempDir()
	mustGit(t, origin, "init", "-q", "--bare", "-b", "main")
	seed := t.TempDir()
	mustGit(t, "/", "clone", "-q", origin, seed)
	write(t, seed, "app.py", "a\n")
	mustGit(t, seed, "add", ".")
	mustGit(t, seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/agent/x")

	fix := t.TempDir()
	mustGit(t, "/", "clone", "-q", "--branch", "agent/x", origin, fix)
	stale := t.TempDir()
	mustGit(t, "/", "clone", "-q", "--branch", "agent/x", origin, stale)

	write(t, fix, "app.py", "b\n")
	g := Git{Dir: fix}
	mustGit(t, fix, "add", ".")
	if err := g.CommitHere(ctx, "bot", "bot@example.com", "fix"); err != nil {
		t.Fatal(err)
	}
	if err := g.Push(ctx, "agent/x", "tok"); err != nil {
		t.Fatalf("push da correção: %v", err)
	}

	write(t, stale, "app.py", "c\n")
	s := Git{Dir: stale}
	mustGit(t, stale, "add", ".")
	if err := s.CommitHere(ctx, "bot", "bot@example.com", "outra"); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(ctx, "agent/x", "tok"); err == nil {
		t.Fatal("push sem force com o branch desatualizado deveria ser recusado")
	}
	if err := s.ForcePush(ctx, "agent/x", "tok"); err != nil {
		t.Fatalf("force push: %v", err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
