//go:build !kurumi_frp_patched || !kurumi_embedded_runtime

package main

import "github.com/Presikid/KurumiFrp/internal/manager"

func runtimeBackend() manager.RuntimeBackend { return nil }
