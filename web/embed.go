package web

import "embed"

//go:embed index.html app.js core.js shell.js protocols.js protocol-editor.js toml-import.js style.css core.css kurumi.webp kurumi-welcome.webp overview.js overview.css instances.js instances.css rules.js rules.css records-config.js records-config.css assets/*.webp
var Files embed.FS
