//go:build !desktop

package desktop

// payload is empty in plain source builds that do not embed the packaged desktop runtime.
var payload []byte
