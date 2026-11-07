package web

import "embed"

//go:embed index.html app.js core.js style.css core.css
var Files embed.FS
