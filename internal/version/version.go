// Package version holds the current application version and optional build variant metadata.
package version

// Current is the three-segment Semantic Versioning (SemVer) release version.
const Current = "0.9.10"

// Distribution is set by the native packager to "windows-installed" or "linux-system".
var Distribution = "source"

// BuildVariant is optionally injected via -ldflags "-X github.com/Presikid/KurumiFrp/internal/version.BuildVariant=..."
// during multi-arch packaging to distinguish arm (ARMv5 softfloat) vs arm_hf (ARMv7 hardfloat).
var BuildVariant = ""
