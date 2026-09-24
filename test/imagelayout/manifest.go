package main

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type entry struct {
	Path, Kind, Link, Hash string
	Mode                   int64
	UID, GID               int
}

func manifestTar(input, output string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	entries, err := readTar(file)
	if err != nil {
		return err
	}
	return writeManifest(output, entries)
}

func readTar(reader io.Reader) ([]entry, error) {
	archive := tar.NewReader(reader)
	seen := map[string]struct{}{}
	var entries []entry
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		current, err := entryFromHeader(archive, header)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[current.Path]; exists {
			return nil, fmt.Errorf("duplicate path %q", current.Path)
		}
		seen[current.Path] = struct{}{}
		entries = append(entries, current)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func entryFromHeader(reader io.Reader, header *tar.Header) (entry, error) {
	kind, err := headerKind(header.Typeflag)
	if err != nil {
		return entry{}, err
	}
	name := header.Name
	if kind == "dir" && strings.HasSuffix(name, "/") {
		name = strings.TrimSuffix(name, "/")
	}
	if err = validatePath(name); err != nil {
		return entry{}, err
	}
	if header.Mode < 0 || header.Mode > 0o7777 || header.Uid < 0 || header.Gid < 0 {
		return entry{}, fmt.Errorf("invalid metadata for %q", name)
	}
	current := entry{Path: name, Kind: kind, Mode: header.Mode, UID: header.Uid, GID: header.Gid, Link: "-", Hash: "-"}
	if kind == "symlink" || kind == "hardlink" {
		if err = validateText(header.Linkname); err != nil || header.Linkname == "" {
			return entry{}, fmt.Errorf("invalid link target for %q", name)
		}
		current.Link = header.Linkname
	}
	if kind == "file" {
		digest := sha256.New()
		if _, err = io.Copy(digest, reader); err != nil {
			return entry{}, err
		}
		current.Hash = hex.EncodeToString(digest.Sum(nil))
	}
	return current, nil
}

func headerKind(flag byte) (string, error) {
	switch flag {
	case tar.TypeReg, byte(0):
		return "file", nil
	case tar.TypeDir:
		return "dir", nil
	case tar.TypeSymlink:
		return "symlink", nil
	case tar.TypeLink:
		return "hardlink", nil
	case tar.TypeChar:
		return "char", nil
	case tar.TypeBlock:
		return "block", nil
	case tar.TypeFifo:
		return "fifo", nil
	default:
		return "", fmt.Errorf("unsupported tar type %d", flag)
	}
}

func validatePath(name string) error {
	if err := validateText(name); err != nil || name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name {
		return fmt.Errorf("invalid path %q", name)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "." || component == ".." {
			return fmt.Errorf("invalid path %q", name)
		}
	}
	return nil
}

func validateText(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("invalid UTF-8")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("control character")
		}
	}
	return nil
}

func writeManifest(output string, entries []entry) error {
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, current := range entries {
		if _, err = fmt.Fprintf(writer, "%s\t%s\t%04o\t%d\t%d\t%s\t%s\n", current.Path, current.Kind,
			current.Mode, current.UID, current.GID, current.Link, current.Hash); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err = writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func canonicalInteger(value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || strconv.Itoa(parsed) != value {
		return 0, errors.New("invalid integer")
	}
	return parsed, nil
}
