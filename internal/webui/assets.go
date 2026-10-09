package webui

import "embed"

// Files contains the SPA shell and its styles and JavaScript modules.
//
//go:embed index.html css js
var Files embed.FS
