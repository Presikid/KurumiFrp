package web

import "embed"

//go:embed index.html app.js core.js health.js style.css core.css health.css kurumi.webp
var Files embed.FS
