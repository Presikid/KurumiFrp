package web

import "embed"

//go:embed index.html workspace.js workspace-reporting-nodes.js workspace-batch-copy.js workspace-pages.js workspace-config.js workspace-admin.js workspace-accounts.js workspace-update.js app.js oidc-return.js webhook-targets.js accounts.js core.js shell.js protocols.js protocol-editor.js toml-import.js style.css core.css kurumi.webp kurumi-welcome.webp overview.js overview.css instances.js instances.css schedules.js rules.js rules.css records-config.js records-config.css nodes.js nodes.css firewall.js assets/*.webp
var Files embed.FS
