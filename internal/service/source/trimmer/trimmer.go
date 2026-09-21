package trimmer

type Trimmer[T any] interface {
	GenerateRuleset([]byte) T
	Trim([]byte) []byte
}
