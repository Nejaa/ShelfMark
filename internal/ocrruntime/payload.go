//go:build ocr

package ocrruntime

import _ "embed"

// payload contains the packaged OCR engine and bundled language data.
//
//go:embed payload.tar.gz
var payload []byte
