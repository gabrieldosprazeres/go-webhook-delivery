package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestVerifierRejectsEveryManifestMutationClass(t *testing.T) {
	base := []entry{
		{Path: "bin/tool", Kind: "file", Mode: 0o755, UID: 0, GID: 0, Link: "-", Hash: digestA},
		{Path: "etc", Kind: "dir", Mode: 0o755, UID: 0, GID: 0, Link: "-", Hash: "-"},
		{Path: "lib", Kind: "symlink", Mode: 0o777, UID: 0, GID: 0, Link: "usr/lib", Hash: "-"},
	}
	allowed := entry{Path: "service", Kind: "file", Mode: 0o555, UID: 65532, GID: 65532, Link: "-", Hash: "*"}
	target := append(cloneEntries(base), entry{Path: "service", Kind: "file", Mode: 0o555,
		UID: 65532, GID: 65532, Link: "-", Hash: digestB})
	if err := verifyEntries(base, target, []entry{allowed}); err != nil {
		t.Fatal(err)
	}

	tests := map[string]func([]entry) []entry{
		"same path changed content": func(items []entry) []entry { items[0].Hash = digestB; return items },
		"symlink target":            func(items []entry) []entry { items[2].Link = "usr/other"; return items },
		"type": func(items []entry) []entry {
			items[1].Kind, items[1].Hash = "file", digestA
			return items
		},
		"mode":  func(items []entry) []entry { items[0].Mode = 0o777; return items },
		"owner": func(items []entry) []entry { items[0].UID, items[0].GID = 1, 2; return items },
		"allowed path as symlink": func(items []entry) []entry {
			items[3].Kind, items[3].Link, items[3].Hash = "symlink", "bin/tool", "-"
			return items
		},
		"extra": func(items []entry) []entry {
			return append(items, entry{Path: "secret", Kind: "file", Mode: 0o600, Link: "-", Hash: digestA})
		},
		"missing": func(items []entry) []entry { return items[1:] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if err := verifyEntries(base, mutate(cloneEntries(target)), []entry{allowed}); err == nil {
				t.Fatal("mutated manifest accepted")
			}
		})
	}
}

func TestManifestParserRejectsTraversalDuplicatesAndMalformedMetadata(t *testing.T) {
	valid := "bin/tool\tfile\t0755\t0\t0\t-\t" + digestA + "\n"
	tests := map[string]string{
		"traversal": "../tool\tfile\t0755\t0\t0\t-\t" + digestA + "\n",
		"duplicate": valid + valid,
		"unsorted":  "z\tfile\t0755\t0\t0\t-\t" + digestA + "\n" + valid,
		"bad mode":  "bin/tool\tfile\t755\t0\t0\t-\t" + digestA + "\n",
		"bad owner": "bin/tool\tfile\t0755\t00\t0\t-\t" + digestA + "\n",
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseManifest(strings.NewReader(manifest), false); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
	if _, err := parseManifest(strings.NewReader(valid), false); err != nil {
		t.Fatal(err)
	}
}

func TestTarManifestCapturesContentTypeModeOwnerAndLink(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("verified content")
	writeHeader(t, writer, &tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg, Mode: 0o555,
		Uid: 42, Gid: 43, Size: int64(len(content))}, content)
	writeHeader(t, writer, &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Mode: 0o777,
		Uid: 44, Gid: 45, Linkname: "bin/tool"}, nil)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := readTar(&archive)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	digest := sha256.Sum256(content)
	if entries[0].Kind != "file" || entries[0].Mode != 0o555 || entries[0].UID != 42 ||
		entries[0].GID != 43 || entries[0].Hash != hex.EncodeToString(digest[:]) {
		t.Fatalf("regular metadata lost: %+v", entries[0])
	}
	if entries[1].Kind != "symlink" || entries[1].Link != "bin/tool" || entries[1].UID != 44 {
		t.Fatalf("symlink metadata lost: %+v", entries[1])
	}
}

func TestExpectedMigratorManifestPinsSQLContentAndOnlyBinaryHashIsWildcard(t *testing.T) {
	repo := t.TempDir()
	migrations := filepath.Join(repo, "db", "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("-- migration content\n")
	if err := os.WriteFile(filepath.Join(migrations, "000001.sql"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "expected.tsv")
	if err := writeExpectedManifest("migrator", repo, output); err != nil {
		t.Fatal(err)
	}
	entries, err := readManifestFile(output, true)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	digest := sha256.Sum256(content)
	if entries[0].Path != "goose" || entries[0].Hash != "*" || entries[1].Path != "migrations" ||
		entries[2].Hash != hex.EncodeToString(digest[:]) || entries[2].Mode != 0o444 {
		t.Fatalf("unexpected allowlist: %+v", entries)
	}
	if _, err = parseManifest(strings.NewReader("service\tfile\t0555\t65532\t65532\t-\t*\n"), false); err == nil {
		t.Fatal("wildcard accepted in observed manifest")
	}
}

func cloneEntries(source []entry) []entry {
	return append([]entry(nil), source...)
}

func writeHeader(t *testing.T, writer *tar.Writer, header *tar.Header, content []byte) {
	t.Helper()
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if len(content) > 0 {
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
}
