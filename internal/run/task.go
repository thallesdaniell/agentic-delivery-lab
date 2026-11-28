package run

// Kind diz o que o run faz: implementar o card ou corrigir um PR que já existe.
type Kind string

const (
	KindImplement     Kind = "implement"      // primeiro run: abre o PR
	KindFixCI         Kind = "fix_ci"         // o CI ficou vermelho: corrige no mesmo PR
	KindAddressReview Kind = "address_review" // a revisão pediu mudanças: atende no mesmo PR
)

// Limites do ciclo de revisão. Atingido o limite, o botão de correção some e o card espera uma pessoa.
const (
	MaxCIAttempts   = 2
	MaxReviewRounds = 3
)

// Valid diz se o tipo é conhecido.
func (k Kind) Valid() bool {
	switch k {
	case KindImplement, KindFixCI, KindAddressReview:
		return true
	}
	return false
}

// Rework diz se o run trabalha sobre um PR que já existe.
func (k Kind) Rework() bool { return k == KindFixCI || k == KindAddressReview }

// Task é o que o run precisa fazer além do card. Num run de correção, Feedback traz o log do CI
// ou os comentários da revisão: texto de terceiros, que o agente trata como dado.
type Task struct {
	Kind     Kind   `json:"kind"`
	Feedback string `json:"feedback,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
	Attempt  int    `json:"attempt,omitempty"` // 1 ou 2 no fix_ci; rodada da revisão no address_review
	// Note é a orientação de quem pediu a correção no board, se houver.
	Note string `json:"note,omitempty"`
}
