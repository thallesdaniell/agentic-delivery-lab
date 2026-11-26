package board

import (
	"strings"
	"testing"
	"time"
)

const run3Log = `time=2026-11-26T21:33:53.362Z level=INFO msg=agente agent=python-dev agent_version=aff212b25629 skill=python-feature skill_version=d0bff7c3aae6 model=claude-sonnet-5-5 budget_usd=1
time=2026-11-26T21:33:53.362Z level=INFO msg=clonando repo=thallesdaniell/agentic-delivery-target
time=2026-11-26T21:33:54.340Z level=INFO msg="instalando dependências (uv sync)"
time=2026-11-26T21:34:01.354Z level=INFO msg="agente trabalhando" timeout=20m0s
time=2026-11-26T21:34:15.508Z level=INFO msg="agente terminou" turns=10 cost_usd=0.0350066 denials=0
time=2026-11-26T21:34:15.512Z level=INFO msg="patch gravado" files=3
time=2026-11-26T21:34:15.512Z level=INFO msg="worker terminou" status=succeeded failure_code="" cost_usd=0.0350066
{
  "card_id": "ADL-3"
}`

func TestParseRunLogBuildsTheTimeline(t *testing.T) {
	l := parseRunLog(run3Log, false, time.Time{})

	if len(l.Steps) != 7 {
		t.Fatalf("esperava 7 etapas, veio %d", len(l.Steps))
	}
	if l.BudgetUSD == nil || *l.BudgetUSD != 1 {
		t.Fatalf("teto não veio do log: %v", l.BudgetUSD)
	}
	if got := l.Total.Round(time.Second); got != 22*time.Second {
		t.Fatalf("duração total = %s", got)
	}
	work := l.Steps[3]
	if work.Msg != "agente trabalhando" || work.Tone != "agent" || work.Took.Round(time.Second) != 14*time.Second {
		t.Fatalf("etapa do agente errada: %+v", work)
	}
	if work.Offset.Round(time.Second) != 8*time.Second {
		t.Fatalf("offset do agente = %s", work.Offset)
	}
	if l.Steps[4].Took != 0 {
		t.Fatalf("etapa de 4 ms não deveria mostrar duração: %s", l.Steps[4].Took)
	}
	if l.Steps[6].Tone != "ok" {
		t.Fatalf("fim com sucesso deveria ser ok: %+v", l.Steps[6])
	}
	if !strings.Contains(l.Extra, `"card_id"`) {
		t.Fatalf("linhas fora do logfmt deveriam ir para Extra: %q", l.Extra)
	}
}

func TestParseRunLogFormatsAttributes(t *testing.T) {
	l := parseRunLog(run3Log, false, time.Time{})

	want := map[string]string{"teto": "US$ 1.00", "limite": "20 min", "custo": "US$ 0.04", "turnos": "10"}
	got := map[string]string{}
	for _, s := range l.Steps {
		for _, a := range s.Attrs {
			got[a.Label] = a.Value
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, esperava %q", k, got[k], v)
		}
	}
	for _, a := range l.Steps[6].Attrs {
		if a.Key == "failure_code" {
			t.Fatalf("atributo vazio não deveria aparecer: %+v", a)
		}
	}
}

func TestParseRunLogSegmentsCoverTheRun(t *testing.T) {
	l := parseRunLog(run3Log, false, time.Time{})

	var sum float64
	for _, s := range l.Segments {
		sum += s.W
	}
	if sum < 99.9 || sum > 100.1 {
		t.Fatalf("os segmentos deveriam cobrir 100%%, cobrem %.2f", sum)
	}
	if l.Segments[len(l.Segments)-1].Label == "" {
		t.Fatal("segmento sem rótulo")
	}
}

func TestParseRunLogMarksTheCurrentStepOfAnActiveRun(t *testing.T) {
	raw := strings.Join(strings.Split(run3Log, "\n")[:4], "\n")
	now := time.Date(2026, 11, 26, 21, 34, 11, 354e6, time.UTC)

	l := parseRunLog(raw, true, now)

	last := l.Steps[len(l.Steps)-1]
	if !last.Current || last.Took != 10*time.Second {
		t.Fatalf("etapa atual errada: %+v", last)
	}
}

func TestParseRunLogMarksFailures(t *testing.T) {
	raw := `time=2026-11-26T21:33:53Z level=INFO msg=clonando repo=x/y
time=2026-11-26T21:33:55Z level=ERROR msg="falha no clone" err="exit status 128"
time=2026-11-26T21:33:55Z level=INFO msg="worker terminou" status=failed failure_code=workspace_error cost_usd=0`

	l := parseRunLog(raw, false, time.Time{})

	if l.Steps[1].Tone != "fail" || l.Steps[2].Tone != "fail" {
		t.Fatalf("falhas sem tom: %+v", l.Steps)
	}
	if l.Steps[1].Attrs[0].Value != "exit status 128" {
		t.Fatalf("valor entre aspas mal lido: %+v", l.Steps[1].Attrs)
	}
}

func TestParseRunLogToleratesGarbage(t *testing.T) {
	l := parseRunLog("algo que não é logfmt\n\ntime=quebrado level=INFO msg=x", false, time.Time{})

	if len(l.Steps) != 0 || l.Extra == "" {
		t.Fatalf("lixo deveria ir para Extra: %+v", l)
	}
}
