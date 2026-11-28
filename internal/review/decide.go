// Package review acompanha o PR de cada card em revisão: CI, revisões e merge.
// Decide é pura, e o Watcher busca no GitHub o que ela precisa e aplica a decisão no banco.
//
// No fluxo puxado o observador não devolve o card ao agente: ele atualiza a etiqueta e deixa pronta
// a correção (log do CI ou comentários da revisão) para uma pessoa disparar com o botão.
package review

import (
	"fmt"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

// Action é o que fazer com o card.
type Action string

const (
	ActPhase    Action = "phase"    // atualizar a etiqueta (e a correção pronta, se houver)
	ActComplete Action = "complete" // merge: card concluído
	ActFail     Action = "fail"     // PR fechado sem merge
)

// Snapshot é o que o GitHub diz sobre o PR de um card agora.
type Snapshot struct {
	Card    store.Reviewing
	PR      githubapp.PRState
	Checks  []githubapp.CheckRun
	Reviews []githubapp.Review
}

// Decision é o próximo passo do card.
type Decision struct {
	Action Action
	Phase  store.ReviewPhase
	Code   run.FailureCode
	Detail string
	Failed []githubapp.CheckRun // checks vermelhos: o log vira a correção pronta do CI
	Review *githubapp.Review    // pedido de mudanças ainda não mandado ao agente: vira a correção pronta da revisão
}

// Decide escolhe o próximo passo. A ordem importa: merge e fechamento primeiro, depois o CI,
// e só com o CI verde a revisão. Um pedido de mudanças já mandado ao agente não volta a ficar pendente.
func Decide(s Snapshot) Decision {
	switch {
	case s.PR.Merged:
		return Decision{Action: ActComplete}
	case s.PR.State == "closed":
		return Decision{Action: ActFail, Code: run.FailurePRClosed, Detail: fmt.Sprintf("o PR #%d foi fechado sem merge", s.PR.Number)}
	}
	ci, failed := CIState(s.Checks)
	switch ci {
	case CIPending:
		return Decision{Action: ActPhase, Phase: store.PhaseCIPending}
	case CIFailed:
		return Decision{Action: ActPhase, Phase: store.PhaseCIFailed, Failed: failed}
	}
	changes, approved := reviewState(s.Reviews)
	if changes != nil && (s.Card.LastReviewID == nil || changes.ID > *s.Card.LastReviewID) {
		return Decision{Action: ActPhase, Phase: store.PhaseChangesRequested, Review: changes}
	}
	if changes == nil && approved {
		return Decision{Action: ActPhase, Phase: store.PhaseApproved}
	}
	// Sem revisão, ou com o pedido de mudanças já atendido pelo agente: falta uma pessoa revisar.
	return Decision{Action: ActPhase, Phase: store.PhaseAwaitingReview}
}

// CI é o estado agregado dos checks do commit.
type CI string

const (
	CIPending CI = "pending"
	CIFailed  CI = "failed"
	CIGreen   CI = "green"
)

// CIState junta os checks: algum vermelho é vermelho, algum rodando é pendente, nenhum check ainda é pendente.
func CIState(checks []githubapp.CheckRun) (CI, []githubapp.CheckRun) {
	if len(checks) == 0 {
		return CIPending, nil
	}
	var failed []githubapp.CheckRun
	pending := false
	for _, c := range checks {
		if c.Status != "completed" {
			pending = true
			continue
		}
		switch c.Conclusion {
		case "success", "neutral", "skipped":
		default:
			failed = append(failed, c)
		}
	}
	switch {
	case len(failed) > 0:
		return CIFailed, failed
	case pending:
		return CIPending, nil
	}
	return CIGreen, nil
}

// reviewState aplica as revisões em ordem e guarda o estado mais recente de cada revisor:
// comentário não muda o estado, e revisão descartada apaga o estado daquele revisor.
func reviewState(reviews []githubapp.Review) (changes *githubapp.Review, approved bool) {
	latest := map[string]githubapp.Review{}
	var order []string
	for _, r := range reviews {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED":
			if _, seen := latest[r.User.Login]; !seen {
				order = append(order, r.User.Login)
			}
			latest[r.User.Login] = r
		case "DISMISSED":
			delete(latest, r.User.Login)
		}
	}
	for _, login := range order {
		r, ok := latest[login]
		if !ok {
			continue
		}
		switch r.State {
		case "CHANGES_REQUESTED":
			if changes == nil || r.ID > changes.ID {
				rr := r
				changes = &rr
			}
		case "APPROVED":
			approved = true
		}
	}
	return changes, approved
}

func names(checks []githubapp.CheckRun) string {
	out := ""
	for i, c := range checks {
		if i > 0 {
			out += ", "
		}
		out += c.Name
	}
	return out
}
