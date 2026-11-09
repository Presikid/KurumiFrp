//go:build kurumi_frp_patched && kurumi_embedded_runtime

package main

import "github.com/Presikid/KurumiFrp/internal/manager"

// The in-process backend is selected only in explicit development builds.
func runtimeBackend() manager.RuntimeBackend { return manager.NewEmbeddedRuntime() }
