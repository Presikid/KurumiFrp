package web

import "embed"

//go:embed index.html workspace.js workspace-reporting-nodes.js workspace-batch-copy.js workspace-pages.js workspace-config.js workspace-admin.js workspace-update.js protocols.js protocol-editor.js style.css core.css kurumi.webp kurumi-welcome.webp overview.css instances.css schedules.js rules.css records-config.css nodes.css assets/*.webp
//go:embed theme-skins.css
var Files embed.FS
