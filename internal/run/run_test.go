package run

import "testing"

func TestEveryDeclaredCodeIsValid(t *testing.T) {
	codes := []FailureCode{
		FailureHandoffInvalid, FailureAgentError, FailureAgentBlocked, FailureModelAPIError,
		FailureBudgetExceeded, FailureTimeout, FailureNoChanges, FailureForbiddenPath,
		FailureCardInvalid, FailureWorkspaceError, FailurePublishError,
	}
	for _, c := range codes {
		if !c.Valid() {
			t.Errorf("%q deveria ser válido", c)
		}
	}
	if FailureCode("unknown").Valid() {
		t.Error("código fora da lista não pode ser válido")
	}
}
