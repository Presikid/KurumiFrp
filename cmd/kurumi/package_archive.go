package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A name alone never authorizes archive removal. Every member must match the
// extracted manifest; links, additional payloads, duplicate members and oversized
// files are rejected. Only the explicit source or adjacent release locations are
// considered; no user's Downloads/history/backup folders are searched.
func archiveMatches(path string, m packageManifest, manifest []byte) error {
	if err := plainPath(path); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 256<<20 {
		return fmt.Errorf("unsafe source archive")
	}
	seen := map[string]bool{}
	check := func(name string, size int64, reader io.Reader) error {
		if !strings.HasPrefix(name, "KurumiFRP/") {
			return fmt.Errorf("unexpected archive path")
		}
		name = strings.TrimPrefix(name, "KurumiFRP/")
		if seen[name] || size < 0 || size > 128<<20 {
			return fmt.Errorf("duplicate or oversized archive member")
		}
		if name == "PACKAGE-MANIFEST.json" {
			if size != int64(len(manifest)) {
				return fmt.Errorf("archive manifest size differs")
			}
			b, e := io.ReadAll(io.LimitReader(reader, size+1))
			if e != nil {
				return e
			}
			if !bytes.Equal(b, manifest) {
				return fmt.Errorf("archive manifest differs")
			}
		} else {
			want, ok := m.Files[name]
			if !ok {
				return fmt.Errorf("unknown archive member")
			}
			hash := sha256.New()
			n, e := io.Copy(hash, io.LimitReader(reader, size+1))
			if e != nil {
				return e
			}
			if n != size || hex.EncodeToString(hash.Sum(nil)) != want {
				return fmt.Errorf("archive member differs")
			}
		}
		seen[name] = true
		return nil
	}
	if strings.HasSuffix(path, ".zip") {
		z, e := zip.OpenReader(path)
		if e != nil {
			return e
		}
		defer z.Close()
		if z.Comment != "" || len(z.File) > len(m.Files)+2 {
			return fmt.Errorf("unexpected ZIP metadata")
		}
		for _, f := range z.File {
			if f.Name == "KurumiFRP/" && f.Mode().IsDir() {
				continue
			}
			if !f.Mode().IsRegular() || f.UncompressedSize64 > 128<<20 {
				return fmt.Errorf("unsafe ZIP member")
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = check(f.Name, int64(f.UncompressedSize64), r)
			ce := r.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		}
	} else if strings.HasSuffix(path, ".tar.gz") {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		gz, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for count := 0; ; count++ {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if count > len(m.Files)+2 {
				return fmt.Errorf("too many tar members")
			}
			if (h.Name == "KurumiFRP" || h.Name == "KurumiFRP/") && h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
				return fmt.Errorf("unsafe tar member")
			}
			if e = check(h.Name, h.Size, tr); e != nil {
				return e
			}
		}
		padding, e := io.ReadAll(io.LimitReader(gz, 1<<20))
		if e != nil || len(padding) >= 1<<20 {
			return fmt.Errorf("invalid gzip trailer or excessive padding")
		}
		for _, b := range padding {
			if b != 0 {
				return fmt.Errorf("non-padding data after archive")
			}
		}
	} else {
		return fmt.Errorf("unsupported source archive format")
	}
	if len(seen) != len(m.Files)+1 {
		return fmt.Errorf("source archive is incomplete")
	}
	return nil
}

func findSourceArchive(source, explicit string, m packageManifest, raw []byte) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if err = archiveMatches(abs, m, raw); err != nil {
			return "", fmt.Errorf("explicit source archive retained: %w", err)
		}
		return abs, nil
	}
	stem := "KurumiFRP_" + m.Version + "_" + m.Platform
	suffix := ".tar.gz"
	if strings.HasPrefix(m.Platform, "windows_") {
		suffix = ".zip"
	}
	parent := filepath.Dir(source)
	paths := []string{filepath.Join(parent, stem+suffix)}
	if filepath.Base(parent) == stem {
		paths = append(paths, filepath.Join(filepath.Dir(parent), stem+suffix))
	}
	found := []string{}
	for _, p := range paths {
		if archiveMatches(p, m, raw) == nil {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) > 1 {
		fmt.Println("Multiple matching archives found; retained rather than guessing which copy to delete.")
	} else {
		fmt.Println("Original archive not identified; no unrelated or moved archives were deleted.")
	}
	return "", nil
}
