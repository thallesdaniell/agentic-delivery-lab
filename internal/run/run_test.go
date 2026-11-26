package run

import "testing"

func TestEveryDeclaredCodeIsValid(t *testing.T) {
	for _, c := range AllFailureCodes {
		if !c.Valid() {
			t.Errorf("%q deveria ser válido", c)
		}
	}
	if FailureCode("unknown").Valid() {
		t.Error("código fora da lista não pode ser válido")
	}
}
