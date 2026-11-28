// Package run define o resultado de um run e os códigos de falha.
// Todo run que falha carrega um FailureCode: nunca existe falha sem código.
package run

// Status é o estado terminal de uma etapa do run.
type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// FailureCode explica por que um run falhou. A lista é fechada.
type FailureCode string

const (
	FailureHandoffInvalid FailureCode = "handoff_invalid"
	FailureAgentError     FailureCode = "agent_error"
	FailureAgentBlocked   FailureCode = "agent_blocked"
	FailureModelAPIError  FailureCode = "model_api_error"
	FailureBudgetExceeded FailureCode = "budget_exceeded"
	FailureTimeout        FailureCode = "timeout"
	FailureNoChanges      FailureCode = "no_changes"
	FailureForbiddenPath  FailureCode = "forbidden_path"
	FailureCardInvalid    FailureCode = "card_invalid"
	FailureWorkspaceError FailureCode = "workspace_error"
	FailurePublishError   FailureCode = "publish_error"
	FailureInterrupted    FailureCode = "interrupted"
	// Códigos do card no ciclo de revisão (M4): o run deu certo, mas o PR não chegou ao merge.
	FailureCIFailedAfterRetries FailureCode = "ci_failed_after_retries"
	FailureReviewLimitReached   FailureCode = "review_limit_reached"
	FailurePRClosed             FailureCode = "pr_closed"
)

// AllFailureCodes é a lista fechada, na ordem em que aparece na documentação.
var AllFailureCodes = []FailureCode{
	FailureCardInvalid, FailureWorkspaceError, FailureHandoffInvalid, FailureAgentBlocked, FailureAgentError,
	FailureModelAPIError, FailureBudgetExceeded, FailureTimeout, FailureNoChanges, FailureForbiddenPath,
	FailurePublishError, FailureInterrupted, FailureCIFailedAfterRetries, FailureReviewLimitReached, FailurePRClosed,
}

// Valid diz se o código pertence à lista fechada.
func (c FailureCode) Valid() bool {
	for _, known := range AllFailureCodes {
		if c == known {
			return true
		}
	}
	return false
}

// Error é uma falha de run com código. Toda função que pode encerrar o run devolve *Error.
type Error struct {
	Code   FailureCode
	Detail string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Detail }

// Fail cria uma falha com código.
func Fail(code FailureCode, detail string) *Error { return &Error{Code: code, Detail: detail} }
