//go:build kurumi_external_runtime

package main

import "github.com/Presikid/KurumiFrp/internal/manager"

func runtimeBackend() manager.RuntimeBackend { return nil }
