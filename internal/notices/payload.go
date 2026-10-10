//go:build notices

package notices

import _ "embed"

//go:embed payload.tar.gz
var payload []byte
