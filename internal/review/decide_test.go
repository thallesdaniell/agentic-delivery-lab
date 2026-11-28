package review

import (
	"testing"

	"github.com/thallesdaniell/agentic-delivery-lab/internal/githubapp"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/run"
	"github.com/thallesdaniell/agentic-delivery-lab/internal/store"
)

func check(name, status, conclusion string) githubapp.CheckRun {
	return githubapp.CheckRun{ID: 1, Name: name, Status: status, Conclusion: conclusion}
}

func rev(id int64, user, state string) githubapp.Review {
	r := githubapp.Review{ID: id, State: state}
	r.User.Login = user
	return r
}

func green() []githubapp.CheckRun {
	return []githubapp.CheckRun{check("lint", "completed", "success"), check("test", "completed", "success")}
}

func open() githubapp.PRState { return githubapp.PRState{Number: 4, State: "open"} }

func ptr(n int64) *int64 { return &n }

func TestDecide(t *testing.T) {
	cases := map[string]struct {
		snap   Snapshot
		action Action
		phase  store.ReviewPhase
		code   run.FailureCode
		review int64 // pedido de mudanças pronto para o botão
	}{
		"merge conclui":               {Snapshot{PR: githubapp.PRState{State: "closed", Merged: true}}, ActComplete, "", "", 0},
		"fechado sem merge falha":     {Snapshot{PR: githubapp.PRState{Number: 4, State: "closed"}}, ActFail, "", run.FailurePRClosed, 0},
		"sem checks ainda é pendente": {Snapshot{PR: open()}, ActPhase, store.PhaseCIPending, "", 0},
		"check rodando é pendente": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "completed", "success"), check("test", "in_progress", "")}},
			ActPhase, store.PhaseCIPending, "", 0},
		"vermelho fica em revisão com a etiqueta": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "completed", "success"), check("test", "completed", "failure")}},
			ActPhase, store.PhaseCIFailed, "", 0},
		"vermelho mesmo com outro rodando": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "in_progress", ""), check("test", "completed", "failure")}},
			ActPhase, store.PhaseCIFailed, "", 0},
		"vermelho depois do limite continua só etiqueta": {Snapshot{PR: open(), Card: store.Reviewing{CIAttempts: 2}, Checks: []githubapp.CheckRun{check("test", "completed", "timed_out")}},
			ActPhase, store.PhaseCIFailed, "", 0},
		"verde sem revisão aguarda": {Snapshot{PR: open(), Checks: green()}, ActPhase, store.PhaseAwaitingReview, "", 0},
		"comentário não conta": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "COMMENTED")}},
			ActPhase, store.PhaseAwaitingReview, "", 0},
		"aprovado": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "APPROVED")}},
			ActPhase, store.PhaseApproved, "", 0},
		"aprovação descartada volta a aguardar": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "APPROVED"), rev(2, "ana", "DISMISSED")}},
			ActPhase, store.PhaseAwaitingReview, "", 0},
		"pedido de mudanças fica pronto para o botão": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}},
			ActPhase, store.PhaseChangesRequested, "", 5},
		"pedido já atendido aguarda nova revisão": {Snapshot{PR: open(), Checks: green(), Card: store.Reviewing{LastReviewID: ptr(5), ReviewRounds: 1},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}}, ActPhase, store.PhaseAwaitingReview, "", 0},
		"novo pedido depois de atendido": {Snapshot{PR: open(), Checks: green(), Card: store.Reviewing{LastReviewID: ptr(5), ReviewRounds: 1},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED"), rev(9, "ana", "CHANGES_REQUESTED")}}, ActPhase, store.PhaseChangesRequested, "", 9},
		"pedido depois aprovado pelo mesmo revisor": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED"), rev(6, "ana", "APPROVED")}},
			ActPhase, store.PhaseApproved, "", 0},
		"pedido de um revisor segura a aprovação de outro": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "APPROVED"), rev(6, "bia", "CHANGES_REQUESTED")}},
			ActPhase, store.PhaseChangesRequested, "", 6},
		"CI vermelho vem antes da revisão": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("test", "completed", "failure")},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}}, ActPhase, store.PhaseCIFailed, "", 0},
	}
	for name, tc := range cases {
		d := Decide(tc.snap)
		if d.Action != tc.action || d.Phase != tc.phase || d.Code != tc.code {
			t.Errorf("%s: %+v, esperava %s/%s/%s", name, d, tc.action, tc.phase, tc.code)
		}
		if got := int64(0); d.Review != nil {
			got = d.Review.ID
			if got != tc.review {
				t.Errorf("%s: revisão pronta %d, esperava %d", name, got, tc.review)
			}
		} else if tc.review != 0 {
			t.Errorf("%s: faltou a revisão pronta %d", name, tc.review)
		}
		if d.Action == ActFail && d.Detail == "" {
			t.Errorf("%s: falha sem detalhe", name)
		}
	}
}
