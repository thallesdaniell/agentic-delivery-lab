package store

// AllowedMoves é a regra única dos movimentos que uma pessoa faz no board, arrastando ou com botão,
// e na linha de comando. O board marca com ela as colunas que aceitam o card, e Move recusa o resto.
//
// O card só anda para frente. Pronto é "refinado, esperando alguém puxar" e não gasta nada;
// entrar em Executando enfileira o run. Em revisão e Concluído mudam só pelo PR (CI, revisão, merge),
// e as correções acontecem com o card parado em Em revisão.
func AllowedMoves(status CardStatus, hasAgent bool) []CardStatus {
	switch status {
	case CardBacklog:
		if hasAgent {
			return []CardStatus{CardReady, CardRunning}
		}
	case CardReady:
		return []CardStatus{CardBacklog, CardRunning}
	case CardFailed:
		if hasAgent {
			return []CardStatus{CardReady, CardRunning}
		}
	}
	return nil
}

// CanMove diz se o movimento está na regra.
func CanMove(status CardStatus, hasAgent bool, to CardStatus) bool {
	for _, m := range AllowedMoves(status, hasAgent) {
		if m == to {
			return true
		}
	}
	return false
}
