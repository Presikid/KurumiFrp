package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Never use an archive member's name as a filesystem destination. Only the two
// expected executable basenames and LICENSE can be written to a fresh directory.
func extract(archive, dir, asset, platform, version string) (map[string]string, error) {
	prefix := "frp_" + version + "_" + platform
	hashes := map[string]string{}
	count := 0
	var total int64
	member := func(name string, size int64, mode os.FileMode) (string, error) {
		count++
		total += size
		if count > 1024 || size < 0 || size > 120<<20 || total > 300<<20 {
			return "", fmt.Errorf("archive exceeds extraction limits")
		}
		clean := strings.TrimSuffix(name, "/")
		if strings.Contains(clean, "\\") || path.Clean(clean) != clean || strings.HasPrefix(clean, "/") || (clean != prefix && !strings.HasPrefix(clean, prefix+"/")) {
			return "", fmt.Errorf("unsafe archive path")
		}
		if mode.IsDir() {
			return "", nil
		}
		if !mode.IsRegular() {
			return "", fmt.Errorf("archive links/special files are not allowed")
		}
		for _, want := range []string{binaryName("frpc", platform), binaryName("frps", platform), "LICENSE"} {
			if clean == prefix+"/"+want {
				if _, ok := hashes[want]; ok {
					return "", fmt.Errorf("duplicate archive entry")
				}
				return want, nil
			}
		}
		return "", nil
	}
	write := func(name string, r io.Reader, size int64) error {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, size+1))
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("archive member size mismatch")
		}
		hashes[name] = hex.EncodeToString(h.Sum(nil))
		return nil
	}
	if strings.HasSuffix(asset, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.UncompressedSize64 > 120<<20 {
				return nil, fmt.Errorf("ZIP member too large")
			}
			name, err := member(f.Name, int64(f.UncompressedSize64), f.Mode())
			if err != nil {
				return nil, err
			}
			if name == "" {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = write(name, r, int64(f.UncompressedSize64))
			r.Close()
			if err != nil {
				return nil, err
			}
		}
	} else if strings.HasSuffix(asset, ".tar.gz") {
		f, err := os.Open(archive)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		gz.Multistream(false)
		t := tar.NewReader(gz)
		for {
			h, err := t.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			name, err := member(h.Name, h.Size, h.FileInfo().Mode())
			if err != nil {
				return nil, err
			}
			if name == "" {
				continue
			}
			if err = write(name, t, h.Size); err != nil {
				return nil, err
			}
		}
		n, err := io.Copy(io.Discard, io.LimitReader(gz, 1<<20))
		if err != nil || n >= 1<<20 {
			return nil, fmt.Errorf("invalid trailing archive data")
		}
	} else {
		return nil, fmt.Errorf("unsupported archive format")
	}
	for _, kind := range []string{"frpc", "frps"} {
		name := binaryName(kind, platform)
		if hashes[name] == "" {
			return nil, fmt.Errorf("archive missing %s", name)
		}
		if err := checkExecutable(filepath.Join(dir, name), platform); err != nil {
			return nil, err
		}
	}
	return hashes, nil
}
func checkExecutable(file, platform string) error {
	var match bool
	switch {
	case strings.HasPrefix(platform, "linux_"):
		f, err := elf.Open(file)
		if err != nil {
			return fmt.Errorf("invalid ELF executable")
		}
		defer f.Close()
		match = (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN) && ((platform == "linux_amd64" && f.Machine == elf.EM_X86_64) || (platform == "linux_arm64" && f.Machine == elf.EM_AARCH64))
	case strings.HasPrefix(platform, "windows_"):
		f, err := pe.Open(file)
		if err != nil {
			return fmt.Errorf("invalid PE executable")
		}
		defer f.Close()
		match = f.Characteristics&pe.IMAGE_FILE_DLL == 0 && ((platform == "windows_amd64" && f.Machine == pe.IMAGE_FILE_MACHINE_AMD64) || (platform == "windows_arm64" && f.Machine == pe.IMAGE_FILE_MACHINE_ARM64))
	case strings.HasPrefix(platform, "darwin_"):
		f, err := macho.Open(file)
		if err != nil {
			return fmt.Errorf("invalid Mach-O executable")
		}
		defer f.Close()
		match = f.Type == macho.TypeExec && ((platform == "darwin_amd64" && f.Cpu == macho.CpuAmd64) || (platform == "darwin_arm64" && f.Cpu == macho.CpuArm64))
	}
	if !match {
		return fmt.Errorf("executable architecture mismatch")
	}
	return nil
}
