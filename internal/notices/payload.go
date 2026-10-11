//go:build notices

package notices

import _ "embed"

// payload contains the packaged notices archive generated for release artifacts.
//
//go:embed payload.tar.gz
var payload []byte
