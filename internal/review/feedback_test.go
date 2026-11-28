package review

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
)

const actionsLog = "2026-11-24T23:52:42.6867515Z \x1b[31mFAILED\x1b[0m tests/test_tasks.py::test_x\r\n" +
	"2026-11-24T23:52:42.6871052Z 1 failed, 4 passed in 0.78s\r\n" +
	"2026-11-24T23:52:42.7437063Z ##[error]Process completed with exit code 1.\r\n" +
	"2026-11-24T23:52:42.7587485Z Post job cleanup.\r\n" +
	"2026-11-24T23:52:42.8478597Z [command]/usr/bin/git version\r\n"

func TestCleanLogKeepsTheErrorAndDropsTheNoise(t *testing.T) {
	got := CleanLog(actionsLog)
	want := "FAILED tests/test_tasks.py::test_x\n1 failed, 4 passed in 0.78s\n##[error]Process completed with exit code 1."
	if got != want {
		t.Fatalf("log limpo:\n%q\nesperava\n%q", got, want)
	}
}

func TestCIFeedbackNamesTheChecksAndExplainsAMissingLog(t *testing.T) {
	fb := CIFeedback(4, "0a5060d2c541", []CheckLog{
		{Check: githubapp.CheckRun{Name: "test", Conclusion: "failure"}, Log: actionsLog},
		{Check: githubapp.CheckRun{Name: "lint", Conclusion: "failure"}, Err: errors.New("HTTP 403")},
	})
	for _, want := range []string{"PR #4", "0a5060d", "## test (failure)", "1 failed, 4 passed", "## lint (failure)", "Log indisponível (HTTP 403)"} {
		if !strings.Contains(fb, want) {
			t.Errorf("faltou %q:\n%s", want, fb)
		}
	}
}

func TestFeedbackIsCappedWithoutBreakingCharacters(t *testing.T) {
	long := strings.Repeat("ção ", 5000)
	fb := CIFeedback(1, "abc", []CheckLog{{Check: githubapp.CheckRun{Name: "a", Conclusion: "failure"}, Log: long},
		{Check: githubapp.CheckRun{Name: "b", Conclusion: "failure"}, Log: long}, {Check: githubapp.CheckRun{Name: "c", Conclusion: "failure"}, Log: long}})
	if len(fb) > maxFeedback+20 || !strings.Contains(fb, "[cortado]") {
		t.Fatalf("feedback sem teto: %d bytes", len(fb))
	}
	if !utf8.ValidString(fb) {
		t.Fatal("o corte quebrou um caractere")
	}
}

func TestReviewFeedbackCarriesBodyAndLineComments(t *testing.T) {
	line := 12
	r := githubapp.Review{ID: 9, Body: "Faltou validar o título vazio."}
	fb := ReviewFeedback(4, r, []githubapp.ReviewComment{{Path: "src/app.py", Line: &line, Body: "Use 422 aqui."}})
	for _, want := range []string{"PR #4", "Faltou validar o título vazio.", "src/app.py:12: Use 422 aqui."} {
		if !strings.Contains(fb, want) {
			t.Errorf("faltou %q:\n%s", want, fb)
		}
	}
	if !strings.Contains(ReviewFeedback(4, githubapp.Review{}, nil), "não trouxe comentários") {
		t.Fatal("revisão vazia precisa de uma orientação")
	}
}

// Um log longo de instalação não pode empurrar o erro para fora da entrada do agente.
func TestLongLogKeepsTheFailureAtTheEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 140; i++ {
		b.WriteString("2026-11-28T00:19:10.0000000Z  + pacote-com-nome-bem-comprido-para-encher-a-linha==1.0.0 (instalado)\n")
	}
	b.WriteString("2026-11-28T00:19:12.0000000Z FAILED tests/test_health.py::test_health_returns_ok - assert 'up' == 'ok'\n")
	b.WriteString("2026-11-28T00:19:12.0000000Z 1 failed, 14 passed in 0.31s\n")
	fb := CIFeedback(5, "42a696c", []CheckLog{{Check: githubapp.CheckRun{Name: "test", Conclusion: "failure"}, Log: b.String()}})
	for _, want := range []string{"assert 'up' == 'ok'", "1 failed, 14 passed", "[início do log cortado]"} {
		if !strings.Contains(fb, want) {
			t.Errorf("faltou %q na entrada", want)
		}
	}
	if strings.Contains(fb, "[cortado]\n") && !strings.HasSuffix(strings.TrimSpace(fb), "```") {
		t.Fatal("o fim da entrada não pode ser cortado")
	}
}
