package board

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// O worker loga em logfmt (slog.TextHandler). O board lê esse log e monta a linha do
// tempo do run: cada etapa com o momento em que começou, quanto durou e os atributos.
// Linha que não é logfmt (o result.json que o worker imprime no fim) vai para Extra.

type logAttr struct{ Key, Label, Value string }

type logStep struct {
	Offset  time.Duration // desde a primeira linha
	Took    time.Duration // até a próxima etapa (ou até agora, na etapa atual)
	Current bool          // última etapa de um run que ainda roda
	Level   string
	Msg     string
	Tone    string // agent, ok, fail, warn ou vazio
	Attrs   []logAttr
}

// logSegment é um trecho da barra de tempo, em porcentagem da duração total.
type logSegment struct {
	Label string
	Tone  string
	X, W  float64
	Took  time.Duration
}

type runLog struct {
	Steps     []logStep
	Segments  []logSegment
	Total     time.Duration
	BudgetUSD *float64
	Extra     string
}

func parseRunLog(raw string, active bool, now time.Time) runLog {
	var l runLog
	var times []time.Time
	var extra []string
	for _, line := range strings.Split(raw, "\n") {
		at, step, ok := parseLogLine(line)
		if !ok {
			if strings.TrimSpace(line) != "" || len(extra) > 0 {
				extra = append(extra, line)
			}
			continue
		}
		if b, ok := attrFloat(step.Attrs, "budget_usd"); ok {
			l.BudgetUSD = &b
		}
		times = append(times, at)
		l.Steps = append(l.Steps, step)
	}
	l.Extra = strings.TrimSpace(strings.Join(extra, "\n"))
	if len(l.Steps) == 0 {
		return l
	}
	for i := range l.Steps {
		l.Steps[i].Offset = times[i].Sub(times[0])
		switch {
		case i+1 < len(l.Steps):
			l.Steps[i].Took = times[i+1].Sub(times[i])
		case active && !now.IsZero() && now.After(times[i]):
			l.Steps[i].Took = now.Sub(times[i]).Round(time.Second)
			l.Steps[i].Current = true
		}
	}
	last := l.Steps[len(l.Steps)-1]
	l.Total = last.Offset + last.Took
	l.Segments = segments(l.Steps, l.Total)
	for i := range l.Steps {
		if l.Steps[i].Took < 50*time.Millisecond { // etapa instantânea não mostra duração
			l.Steps[i].Took = 0
		}
	}
	return l
}

func segments(steps []logStep, total time.Duration) []logSegment {
	if total <= 0 {
		return nil
	}
	var out []logSegment
	for _, s := range steps {
		if s.Took < total/200 { // menos de 0,5% não aparece na barra
			continue
		}
		out = append(out, logSegment{
			Label: segmentLabel(s.Msg),
			Tone:  s.Tone,
			X:     pct(s.Offset, total),
			W:     pct(s.Took, total),
			Took:  s.Took,
		})
	}
	return out
}

func pct(d, total time.Duration) float64 { return float64(d) / float64(total) * 100 }

var segmentLabels = map[string]string{
	"agente":                            "preparo",
	"clonando":                          "clone",
	"instalando dependências (uv sync)": "dependências",
	"agente trabalhando":                "agente",
	"agente terminou":                   "patch",
}

func segmentLabel(msg string) string {
	if l, ok := segmentLabels[msg]; ok {
		return l
	}
	return msg
}

// parseLogLine lê uma linha do slog.TextHandler: time=… level=… msg=… chave=valor…
func parseLogLine(line string) (time.Time, logStep, bool) {
	pairs, ok := logfmt(line)
	if !ok || len(pairs) < 3 || pairs[0][0] != "time" || pairs[1][0] != "level" || pairs[2][0] != "msg" {
		return time.Time{}, logStep{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, pairs[0][1])
	if err != nil {
		return time.Time{}, logStep{}, false
	}
	s := logStep{Level: pairs[1][1], Msg: pairs[2][1]}
	for _, p := range pairs[3:] {
		if p[1] == "" {
			continue
		}
		s.Attrs = append(s.Attrs, logAttr{Key: p[0], Label: attrLabel(p[0]), Value: attrValue(p[0], p[1])})
	}
	s.Tone = tone(s, pairs)
	return at, s, true
}

func tone(s logStep, pairs [][2]string) string {
	switch {
	case s.Level == "ERROR":
		return "fail"
	case s.Level == "WARN":
		return "warn"
	case s.Msg == "agente trabalhando":
		return "agent"
	}
	for _, p := range pairs {
		if p[0] == "status" {
			switch p[1] {
			case "succeeded":
				return "ok"
			case "failed":
				return "fail"
			}
		}
	}
	return ""
}

// logfmt quebra a linha em pares chave=valor; valores com espaço vêm entre aspas (strconv.Quote).
func logfmt(line string) ([][2]string, bool) {
	var out [][2]string
	rest := strings.TrimSpace(line)
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 || strings.ContainsAny(rest[:eq], " \"") {
			return nil, false
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			q, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return nil, false
			}
			if val, err = strconv.Unquote(q); err != nil {
				return nil, false
			}
			rest = rest[len(q):]
		} else {
			end := strings.IndexByte(rest, ' ')
			if end < 0 {
				end = len(rest)
			}
			val, rest = rest[:end], rest[end:]
		}
		out = append(out, [2]string{key, val})
		rest = strings.TrimLeft(rest, " ")
	}
	return out, true
}

var attrLabels = map[string]string{
	"agent": "agente", "agent_version": "versão", "skill_version": "versão da skill", "model": "modelo",
	"budget_usd": "teto", "timeout": "limite", "turns": "turnos", "cost_usd": "custo", "denials": "negadas",
	"files": "arquivos", "failure_code": "código", "err": "erro",
}

func attrLabel(k string) string {
	if l, ok := attrLabels[k]; ok {
		return l
	}
	return k
}

func attrValue(k, v string) string {
	switch k {
	case "cost_usd", "budget_usd":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return fmt.Sprintf("US$ %.2f", f)
		}
	case "timeout":
		if d, err := time.ParseDuration(v); err == nil {
			return fmtDur(d)
		}
	}
	return v
}

func attrFloat(attrs []logAttr, key string) (float64, bool) {
	for _, a := range attrs {
		if a.Key == key {
			f, err := strconv.ParseFloat(strings.TrimPrefix(a.Value, "US$ "), 64)
			return f, err == nil
		}
	}
	return 0, false
}

// fmtDur escreve a duração como uma pessoa leria: 0,4 s, 14 s, 2 min 05 s, 20 min.
func fmtDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	}
	d = d.Round(time.Second)
	m, s := int(d.Minutes()), int(d.Seconds())%60
	if s == 0 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d min %02d s", m, s)
}

// fmtOffset escreve o momento relativo ao início do run: 0:00, 0:08, 12:30.
func fmtOffset(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
