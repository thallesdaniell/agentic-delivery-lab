package dockerapi

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestDemux(t *testing.T) {
	raw := append(frame(1, "linha do stdout\n"), frame(2, "linha do stderr\n")...)
	if got := Demux(raw); got != "linha do stdout\nlinha do stderr\n" {
		t.Fatalf("demux = %q", got)
	}
}

func tarOf(t *testing.T, files map[string]string, link bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	if link {
		_ = tw.WriteHeader(&tar.Header{Name: "out/evil", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	}
	_ = tw.Close()
	return buf.Bytes()
}

func TestExtractTarKeepsOnlyRegularFilesInsideDir(t *testing.T) {
	dir := t.TempDir()
	raw := tarOf(t, map[string]string{"out/result.json": "{}", "out/../../escape.txt": "x"}, true)
	if err := ExtractTar(bytes.NewReader(raw), dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "result.json")); err != nil {
		t.Fatal("result.json deveria ter sido extraído")
	}
	if _, err := os.Lstat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("symlink não pode ser extraído")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "escape.txt")); err == nil {
		t.Fatal("arquivo escapou do diretório")
	}
}

func TestExtractTarEnforcesTheSizeLimit(t *testing.T) {
	raw := tarOf(t, map[string]string{"out/big": "0123456789"}, false)
	if err := ExtractTar(bytes.NewReader(raw), t.TempDir(), 5); err == nil {
		t.Fatal("deveria recusar saída maior que o limite")
	}
}
