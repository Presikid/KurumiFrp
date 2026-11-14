package web

import "embed"

// Files embeds the Tokisaki Kurumi WebUI static assets into the Go binary.
//
//go:embed index.html style.css app.js assets/*.webp
var Files embed.FS
