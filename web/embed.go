package web

import "embed"

// Files embeds the Tokisaki Kurumi WebUI static assets into the Go binary.
//
//go:embed index.html style.css app.js assets/k_*.webp assets/s_*.webp
var Files embed.FS
