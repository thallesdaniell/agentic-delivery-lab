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
	}{
		"merge conclui": {Snapshot{PR: githubapp.PRState{State: "closed", Merged: true}}, ActComplete, "", ""},
		"fechado sem merge falha": {Snapshot{PR: githubapp.PRState{Number: 4, State: "closed"}},
			ActFail, "", run.FailurePRClosed},
		"sem checks ainda é pendente": {Snapshot{PR: open()}, ActPhase, store.PhaseCIPending, ""},
		"check rodando é pendente": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "completed", "success"), check("test", "in_progress", "")}},
			ActPhase, store.PhaseCIPending, ""},
		"vermelho volta ao agente": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "completed", "success"), check("test", "completed", "failure")}},
			ActFixCI, store.PhaseCIFailed, ""},
		"vermelho mesmo com outro rodando": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("lint", "in_progress", ""), check("test", "completed", "failure")}},
			ActFixCI, store.PhaseCIFailed, ""},
		"vermelho depois de 2 tentativas falha": {Snapshot{PR: open(), Card: store.Reviewing{CIAttempts: 2}, Checks: []githubapp.CheckRun{check("test", "completed", "timed_out")}},
			ActFail, "", run.FailureCIFailedAfterRetries},
		"verde sem revisão aguarda": {Snapshot{PR: open(), Checks: green()}, ActPhase, store.PhaseAwaitingReview, ""},
		"comentário não conta": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "COMMENTED")}},
			ActPhase, store.PhaseAwaitingReview, ""},
		"aprovado": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "APPROVED")}},
			ActPhase, store.PhaseApproved, ""},
		"aprovação descartada volta a aguardar": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(1, "ana", "APPROVED"), rev(2, "ana", "DISMISSED")}},
			ActPhase, store.PhaseAwaitingReview, ""},
		"pedido de mudanças volta ao agente": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}},
			ActAddressReview, store.PhaseChangesRequested, ""},
		"pedido já atendido aguarda nova revisão": {Snapshot{PR: open(), Checks: green(), Card: store.Reviewing{LastReviewID: ptr(5), ReviewRounds: 1},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}}, ActPhase, store.PhaseAwaitingReview, ""},
		"novo pedido depois de atendido volta ao agente": {Snapshot{PR: open(), Checks: green(), Card: store.Reviewing{LastReviewID: ptr(5), ReviewRounds: 1},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED"), rev(9, "ana", "CHANGES_REQUESTED")}}, ActAddressReview, store.PhaseChangesRequested, ""},
		"pedido depois aprovado pelo mesmo revisor": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED"), rev(6, "ana", "APPROVED")}},
			ActPhase, store.PhaseApproved, ""},
		"pedido de um revisor segura a aprovação de outro": {Snapshot{PR: open(), Checks: green(), Reviews: []githubapp.Review{rev(5, "ana", "APPROVED"), rev(6, "bia", "CHANGES_REQUESTED")}},
			ActAddressReview, store.PhaseChangesRequested, ""},
		"pedido além do limite de rodadas falha": {Snapshot{PR: open(), Checks: green(), Card: store.Reviewing{LastReviewID: ptr(5), ReviewRounds: 3},
			Reviews: []githubapp.Review{rev(9, "ana", "CHANGES_REQUESTED")}}, ActFail, "", run.FailureReviewLimitReached},
		"CI vermelho vem antes da revisão": {Snapshot{PR: open(), Checks: []githubapp.CheckRun{check("test", "completed", "failure")},
			Reviews: []githubapp.Review{rev(5, "ana", "CHANGES_REQUESTED")}}, ActFixCI, store.PhaseCIFailed, ""},
	}
	for name, tc := range cases {
		d := Decide(tc.snap)
		if d.Action != tc.action || d.Phase != tc.phase || d.Code != tc.code {
			t.Errorf("%s: %+v, esperava %s/%s/%s", name, d, tc.action, tc.phase, tc.code)
		}
		if d.Action == ActFail && d.Detail == "" {
			t.Errorf("%s: falha sem detalhe", name)
		}
	}
}
