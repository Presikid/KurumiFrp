package web

import "embed"

//go:embed index.html app.js core.js style.css core.css kurumi.webp
var Files embed.FS
