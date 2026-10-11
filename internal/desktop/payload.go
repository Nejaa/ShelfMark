//go:build desktop

package desktop

import _ "embed"

// payload contains the packaged native helper runtime assembled for release builds.
//
//go:embed payload.tar.gz
var payload []byte
