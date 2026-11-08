package update

import "testing"

func TestLinuxDebianArchitectures(t *testing.T) {
	for arch, want := range map[string]string{
		"amd64":   "kurumifrp_0.7.1.3_amd64.deb",
		"arm64":   "kurumifrp_0.7.1.3_arm64.deb",
		"arm":     "kurumifrp_0.7.1.3_armhf.deb",
		"riscv64": "kurumifrp_0.7.1.3_riscv64.deb",
	} {
		if got := packageNameForArch("v0.7.1.3", "linux-deb", arch); got != want {
			t.Errorf("architecture %s: got %s, want %s", arch, got, want)
		}
	}
}
