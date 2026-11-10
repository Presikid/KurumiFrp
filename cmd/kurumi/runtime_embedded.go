package main

import "github.com/Presikid/KurumiFrp/internal/manager"

// Normal builds run the bundled frp implementation in this process.
func runtimeBackend() manager.RuntimeBackend { return manager.NewEmbeddedRuntime() }
