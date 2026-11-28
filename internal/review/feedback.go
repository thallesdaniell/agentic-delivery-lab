package review

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
)

// Limites da entrada que vai ao agente: o bastante para achar o erro, sem estourar o contexto.
const (
	maxFeedback    = 12000
	maxPerCheck    = 6000
	maxLogPerCheck = 4500
	logLines       = 150
)

var (
	timestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[\d:.]+Z ?`)
	ansi      = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
)

// CheckLog é o log de um check vermelho, ou o motivo de não haver log.
type CheckLog struct {
	Check githubapp.CheckRun
	Log   string
	Err   error
}

// CIFeedback monta a entrada de um run fix_ci: quais checks falharam e o fim do log de cada um.
func CIFeedback(pr int, sha string, logs []CheckLog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "O CI falhou no PR #%d (commit %s). Checks vermelhos:\n", pr, short(sha))
	for _, l := range logs {
		var sec strings.Builder
		fmt.Fprintf(&sec, "\n## %s (%s)\n", l.Check.Name, l.Check.Conclusion)
		if t := strings.TrimSpace(l.Check.Output.Title + "\n" + l.Check.Output.Summary); t != "" {
			fmt.Fprintf(&sec, "%s\n", t)
		}
		switch {
		case l.Err != nil:
			fmt.Fprintf(&sec, "Log indisponível (%v). Sem o log, rode os mesmos checks do repositório para achar a falha.\n", l.Err)
		case strings.TrimSpace(l.Log) != "":
			// O erro fica no fim do log: o corte tira o começo (instalação, setup), nunca o fim.
			fmt.Fprintf(&sec, "Fim do log:\n```\n%s\n```\n", clipStart(CleanLog(l.Log), maxLogPerCheck))
		}
		b.WriteString(clip(sec.String(), maxPerCheck))
	}
	return clip(b.String(), maxFeedback)
}

// CleanLog tira carimbo de hora, cores e a limpeza do runner, e fica com as últimas linhas.
func CleanLog(raw string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		l = ansi.ReplaceAllString(timestamp.ReplaceAllString(l, ""), "")
		if strings.HasPrefix(l, "Post job cleanup.") {
			break // daqui em diante é a limpeza do runner, sem nada do erro
		}
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > logLines {
		lines = lines[len(lines)-logLines:]
	}
	return strings.Join(lines, "\n")
}

// ReviewFeedback monta a entrada de um run address_review: o comentário geral e os de linha.
func ReviewFeedback(pr int, r githubapp.Review, comments []githubapp.ReviewComment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Uma pessoa revisou o PR #%d e pediu mudanças.\n", pr)
	if body := strings.TrimSpace(r.Body); body != "" {
		fmt.Fprintf(&b, "\n## Comentário geral\n%s\n", body)
	}
	if len(comments) > 0 {
		b.WriteString("\n## Comentários nas linhas\n")
		for _, c := range comments {
			where := c.Path
			if c.Line != nil {
				where = fmt.Sprintf("%s:%d", c.Path, *c.Line)
			}
			fmt.Fprintf(&b, "- %s: %s\n", where, strings.TrimSpace(c.Body))
		}
	}
	if strings.TrimSpace(r.Body) == "" && len(comments) == 0 {
		b.WriteString("\nA revisão não trouxe comentários. Releia o card e os critérios de aceite e confira o que pode faltar.\n")
	}
	return clip(b.String(), maxFeedback)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) { // não corta um caractere ao meio
		n--
	}
	return s[:n] + "\n[cortado]\n"
}

// clipStart fica com os últimos n bytes, começando numa linha inteira.
func clipStart(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 {
		cut = cut[i+1:]
	}
	return "[início do log cortado]\n" + cut
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
