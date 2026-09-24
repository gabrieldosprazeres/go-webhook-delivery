package delivery

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReliabilityMigrationKeepsFunctionsAuditable(t *testing.T) {
	paths, err := filepath.Glob("../../db/migrations/00000[3-6]_reliability_*.sql")
	if err != nil {
		t.Fatal(err)
	}
	functions := 0
	for _, path := range paths {
		file, openErr := os.Open(path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		scanner := bufio.NewScanner(file)
		name, lines, fileLines := "", 0, 0
		for scanner.Scan() {
			fileLines++
			line := scanner.Text()
			if strings.HasPrefix(line, "CREATE FUNCTION wde.") {
				name, lines = strings.Fields(line)[2], 1
				continue
			}
			if name == "" {
				continue
			}
			lines++
			if line == "$function$;" {
				functions++
				if lines > 100 {
					t.Errorf("function %s has %d lines; limit is 100", name, lines)
				}
				name, lines = "", 0
			}
		}
		if err = scanner.Err(); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		if fileLines > 200 || name != "" {
			t.Errorf("migration %s lines=%d unterminated=%q", filepath.Base(path), fileLines, name)
		}
	}
	if len(paths) != 4 || functions != 5 {
		t.Fatalf("audited migrations=%d functions=%d", len(paths), functions)
	}
}
