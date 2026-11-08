package core

import "testing"

func TestAdditionalOfficialPlatforms(t *testing.T) {
	platforms := []string{"linux_arm", "linux_arm_hf", "linux_riscv64", "linux_loong64", "linux_mips", "linux_mipsle", "linux_mips64", "linux_mips64le"}
	for _, platform := range platforms {
		if !supported(platform) {
			t.Fatalf("official platform %s is not supported", platform)
		}
		id := "v0.72.0-" + platform + "-0123456789abcdef"
		if !idRE.MatchString(id) {
			t.Fatalf("installed core ID rejected: %s", id)
		}
		name := "frp_0.72.0_" + platform + ".tar.gz"
		asset := Asset{Name: name, URL: "https://github.com/fatedier/frp/releases/download/v0.72.0/" + name}
		result := cleanRelease(Release{Tag: "v0.72.0", Assets: []Asset{asset}})
		if len(result.Assets) != 1 || result.Assets[0].Platform != platform {
			t.Fatalf("official archive rejected: %s", name)
		}
	}
}
