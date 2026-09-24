package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func verifyManifestFiles(basePath, targetPath, expectedPath string) error {
	base, err := readManifestFile(basePath, false)
	if err != nil {
		return fmt.Errorf("base manifest: %w", err)
	}
	target, err := readManifestFile(targetPath, false)
	if err != nil {
		return fmt.Errorf("target manifest: %w", err)
	}
	expected, err := readManifestFile(expectedPath, true)
	if err != nil {
		return fmt.Errorf("expected manifest: %w", err)
	}
	return verifyEntries(base, target, expected)
}

func readManifestFile(name string, wildcard bool) ([]entry, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseManifest(file, wildcard)
}

func parseManifest(reader io.Reader, wildcard bool) ([]entry, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var result []entry
	previous := ""
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 7 || validatePath(parts[0]) != nil || parts[0] <= previous {
			return nil, errors.New("manifest is malformed, unsorted or duplicated")
		}
		mode, err := strconv.ParseInt(parts[2], 8, 64)
		if err != nil || len(parts[2]) != 4 || fmt.Sprintf("%04o", mode) != parts[2] || mode > 0o7777 {
			return nil, errors.New("invalid mode")
		}
		uid, err := canonicalInteger(parts[3])
		if err != nil {
			return nil, errors.New("invalid uid")
		}
		gid, err := canonicalInteger(parts[4])
		if err != nil {
			return nil, errors.New("invalid gid")
		}
		current := entry{Path: parts[0], Kind: parts[1], Mode: mode, UID: uid, GID: gid, Link: parts[5], Hash: parts[6]}
		if err = validateEntry(current, wildcard); err != nil {
			return nil, err
		}
		result, previous = append(result, current), current.Path
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func validateEntry(current entry, wildcard bool) error {
	validKind := current.Kind == "file" || current.Kind == "dir" || current.Kind == "symlink" ||
		current.Kind == "hardlink" || current.Kind == "char" || current.Kind == "block" || current.Kind == "fifo"
	if !validKind {
		return errors.New("invalid entry type")
	}
	isLink := current.Kind == "symlink" || current.Kind == "hardlink"
	if isLink {
		if current.Link == "-" || validateText(current.Link) != nil {
			return errors.New("invalid link metadata")
		}
	} else if current.Link != "-" {
		return errors.New("unexpected link metadata")
	}
	if current.Kind == "file" {
		if !validDigest(current.Hash) && !(wildcard && current.Hash == "*") {
			return errors.New("invalid content digest")
		}
	} else if current.Hash != "-" {
		return errors.New("unexpected content digest")
	}
	return nil
}

func verifyEntries(base, target, expected []entry) error {
	targetByPath := index(target)
	baseByPath := index(base)
	for _, inherited := range base {
		actual, exists := targetByPath[inherited.Path]
		if !exists || actual != inherited {
			return fmt.Errorf("inherited path changed or missing: %s", inherited.Path)
		}
	}
	for _, allowed := range expected {
		if _, inherited := baseByPath[allowed.Path]; inherited {
			return fmt.Errorf("allowlist overlaps inherited path: %s", allowed.Path)
		}
		actual, exists := targetByPath[allowed.Path]
		if !exists || !matchesAllowed(actual, allowed) {
			return fmt.Errorf("allowed addition mismatch: %s", allowed.Path)
		}
	}
	if len(target) != len(base)+len(expected) {
		return errors.New("target contains unallowlisted additions")
	}
	return nil
}

func index(entries []entry) map[string]entry {
	result := make(map[string]entry, len(entries))
	for _, current := range entries {
		result[current.Path] = current
	}
	return result
}

func matchesAllowed(actual, allowed entry) bool {
	if allowed.Hash == "*" {
		allowed.Hash = actual.Hash
	}
	return actual == allowed
}

func writeExpectedManifest(kind, repo, output string) error {
	var entries []entry
	switch kind {
	case "runtime":
		entries = []entry{{Path: "service", Kind: "file", Mode: 0o555, UID: 65532, GID: 65532, Link: "-", Hash: "*"}}
	case "migrator":
		entries = []entry{
			{Path: "goose", Kind: "file", Mode: 0o555, UID: 65532, GID: 65532, Link: "-", Hash: "*"},
			{Path: "migrations", Kind: "dir", Mode: 0o755, UID: 65532, GID: 65532, Link: "-", Hash: "-"},
		}
		files, err := filepath.Glob(filepath.Join(repo, "db/migrations/*.sql"))
		if err != nil || len(files) == 0 {
			return errors.New("migrations are missing")
		}
		for _, file := range files {
			digest, digestErr := hashFile(file)
			if digestErr != nil {
				return digestErr
			}
			entries = append(entries, entry{Path: "migrations/" + filepath.Base(file), Kind: "file", Mode: 0o444,
				UID: 65532, GID: 65532, Link: "-", Hash: digest})
		}
	default:
		return errors.New("invalid image kind")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return writeManifest(output, entries)
}

func hashFile(name string) (string, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}
