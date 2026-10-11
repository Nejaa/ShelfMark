//go:build !ocr

package ocrruntime

// payload is empty in source builds that do not embed the packaged OCR runtime.
var payload []byte
