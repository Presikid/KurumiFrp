package core

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltPortableExecutableArchitectures(t *testing.T) {
	if os.Getenv("KURUMI_PORTABLE_TESTS") == "" {
		t.Skip("portable archives are not built in the regular unit test job")
	}
	for _, arch := range []string{"arm", "arm_hf", "riscv64", "loong64", "mips", "mipsle", "mips64", "mips64le"} {
		t.Run(arch, func(t *testing.T) {
			matches, err := filepath.Glob(filepath.Join("..", "..", "dist-native", "KurumiFRP-*-linux-"+arch+".tar.gz"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("portable archive: %v, %v", matches, err)
			}
			f, err := os.Open(matches[0])
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			gz, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			tr := tar.NewReader(gz)
			h, err := tr.Next()
			if err != nil || h.Name != "kurumifrp" {
				t.Fatalf("expected executable as first archive member: %v", err)
			}
			p := filepath.Join(t.TempDir(), "kurumifrp")
			out, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.Copy(out, tr); err != nil {
				out.Close()
				t.Fatal(err)
			}
			if err = out.Close(); err != nil {
				t.Fatal(err)
			}
			if err = checkExecutable(p, "linux_"+arch); err != nil {
				t.Fatalf("valid executable rejected: %v", err)
			}
			wrong := "linux_amd64"
			if strings.HasPrefix(arch, "mips64") {
				wrong = "linux_mips64le"
				if arch == "mips64le" {
					wrong = "linux_mips64"
				}
			} else if strings.HasPrefix(arch, "mips") {
				wrong = "linux_mipsle"
				if arch == "mipsle" {
					wrong = "linux_mips"
				}
			}
			if err = checkExecutable(p, wrong); err == nil {
				t.Fatalf("incorrect architecture accepted: %s", wrong)
			}
		})
	}
}
