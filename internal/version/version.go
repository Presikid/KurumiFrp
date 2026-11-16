// Package version holds the current application version and optional build variant.
package version

// Current is the three-segment Semantic Versioning (SemVer) release version.
const Current = "0.9.6"

// BuildVariant may be injected via -ldflags "-X github.com/Presikid/KurumiFrp/internal/version.BuildVariant=arm" to distinguish ARMv5 vs ARMv7.
var BuildVariant = ""
