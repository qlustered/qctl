package releasebuild

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	link     string
}

func buildTar(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %s: %v", e.name, err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("Write %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &buf
}

func TestExtractTarWritesFilesDirsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	buf := buildTar(t,
		tarEntry{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader},
		tarEntry{name: "cmd/", typeflag: tar.TypeDir, mode: 0o775},
		tarEntry{name: "cmd/qctl/main.go", typeflag: tar.TypeReg, mode: 0o664, body: "package main\n"},
		tarEntry{name: "scripts/run.sh", typeflag: tar.TypeReg, mode: 0o775, body: "#!/bin/sh\n"},
		tarEntry{name: "docs/link.go", typeflag: tar.TypeSymlink, link: "../cmd/qctl/main.go"},
	)
	if err := extractTar(buf, dir); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "docs", "link.go"))
	if err != nil || string(got) != "package main\n" {
		t.Fatalf("symlinked file: %q, %v", got, err)
	}
	for name, want := range map[string]os.FileMode{"cmd/qctl/main.go": 0o644, "scripts/run.sh": 0o755} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}

func TestExtractTarRejectsEscapingEntries(t *testing.T) {
	cases := map[string]tarEntry{
		"parent path":       {name: "../evil", typeflag: tar.TypeReg, body: "x"},
		"nested parent":     {name: "a/../../evil", typeflag: tar.TypeReg, body: "x"},
		"absolute path":     {name: "/tmp/evil", typeflag: tar.TypeReg, body: "x"},
		"absolute symlink":  {name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"},
		"escaping symlink":  {name: "a/link", typeflag: tar.TypeSymlink, link: "../../outside"},
		"unsupported entry": {name: "dev", typeflag: tar.TypeChar},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "export")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extractTar(buildTar(t, entry), dir); err == nil {
				t.Fatal("extractTar accepted the entry")
			}
			if _, err := os.Lstat(filepath.Join(parent, "evil")); !os.IsNotExist(err) {
				t.Fatalf("an entry was written outside the export directory: %v", err)
			}
		})
	}
}

func TestExtractTarRejectsDuplicateFiles(t *testing.T) {
	buf := buildTar(t,
		tarEntry{name: "a", typeflag: tar.TypeReg, body: "1"},
		tarEntry{name: "a", typeflag: tar.TypeReg, body: "2"},
	)
	if err := extractTar(buf, t.TempDir()); err == nil {
		t.Fatal("extractTar accepted a duplicate file")
	}
}
