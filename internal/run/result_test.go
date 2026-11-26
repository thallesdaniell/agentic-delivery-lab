package run

import (
	"path/filepath"
	"testing"
)

func TestResultInvariant(t *testing.T) {
	if err := (Result{Status: StatusFailed}).Validate(); err == nil {
		t.Error("falha sem código deveria ser recusada")
	}
	if err := (Result{Status: StatusSucceeded, FailureCode: FailureTimeout}).Validate(); err == nil {
		t.Error("sucesso com código deveria ser recusado")
	}
	if err := (Result{Status: "running"}).Validate(); err == nil {
		t.Error("status desconhecido deveria ser recusado")
	}
}

func TestResultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	r := Result{CardID: "ADL-1"}
	r.Failed(Fail(FailureNoChanges, "nada mudou"))
	if err := r.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResult(path)
	if err != nil || got.FailureCode != FailureNoChanges {
		t.Fatalf("got %+v %v", got, err)
	}
}
