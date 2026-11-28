// Package review acompanha o PR de cada card em revisão: CI, revisões e merge.
// Decide é pura, e o Watcher busca no GitHub o que ela precisa e aplica a decisão no banco.
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
	ActPhase         Action = "phase"          // só atualizar a fase do PR
	ActComplete      Action = "complete"       // merge: card concluído
	ActFail          Action = "fail"           // fim do ciclo com código
	ActFixCI         Action = "fix_ci"         // devolver ao agente com o log do CI
	ActAddressReview Action = "address_review" // devolver ao agente com os comentários da revisão
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
	Failed []githubapp.CheckRun // checks vermelhos, para o log que vai ao agente
	Review *githubapp.Review    // revisão com pedido de mudanças, para os comentários
}

// Decide escolhe o próximo passo. A ordem importa: merge e fechamento primeiro, depois o CI,
// e só com o CI verde a revisão. Um pedido de mudanças só dispara uma vez por revisão.
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
		if s.Card.CIAttempts >= run.MaxCIAttempts {
			return Decision{Action: ActFail, Code: run.FailureCIFailedAfterRetries, Failed: failed,
				Detail: fmt.Sprintf("o CI continuou vermelho depois de %d tentativas de correção (%s)", s.Card.CIAttempts, names(failed))}
		}
		return Decision{Action: ActFixCI, Phase: store.PhaseCIFailed, Failed: failed}
	}

	changes, approved := reviewState(s.Reviews)
	if changes != nil {
		handled := s.Card.LastReviewID != nil && changes.ID <= *s.Card.LastReviewID
		switch {
		case handled:
			// O agente já atendeu esta revisão; falta a pessoa revisar de novo.
			return Decision{Action: ActPhase, Phase: store.PhaseAwaitingReview}
		case s.Card.ReviewRounds >= run.MaxReviewRounds:
			return Decision{Action: ActFail, Code: run.FailureReviewLimitReached, Review: changes,
				Detail: fmt.Sprintf("a revisão pediu mudanças de novo depois de %d rodadas com o agente", s.Card.ReviewRounds)}
		}
		return Decision{Action: ActAddressReview, Phase: store.PhaseChangesRequested, Review: changes}
	}
	if approved {
		return Decision{Action: ActPhase, Phase: store.PhaseApproved}
	}
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
