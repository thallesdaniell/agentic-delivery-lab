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
)

// Valid diz se o código pertence à lista fechada.
func (c FailureCode) Valid() bool {
	switch c {
	case FailureHandoffInvalid, FailureAgentError, FailureAgentBlocked, FailureModelAPIError,
		FailureBudgetExceeded, FailureTimeout, FailureNoChanges, FailureForbiddenPath,
		FailureCardInvalid, FailureWorkspaceError, FailurePublishError:
		return true
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
