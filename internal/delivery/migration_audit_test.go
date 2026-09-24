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

func TestOperationsMigrationsKeepFunctionsAuditable(t *testing.T) {
	paths, err := filepath.Glob("../../db/migrations/00000[7-9]_*.sql")
	if err != nil {
		t.Fatal(err)
	}
	final, err := filepath.Glob("../../db/migrations/000010_*.sql")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, final...)
	if len(paths) != 4 {
		t.Fatalf("operations migrations=%d", len(paths))
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(string(contents), "\n") + 1; lines > 200 {
			t.Errorf("migration %s lines=%d", filepath.Base(path), lines)
		}
		if strings.Contains(strings.ToUpper(string(contents)), "EXECUTE FORMAT") ||
			strings.Contains(strings.ToUpper(string(contents)), "EXECUTE IMMEDIATE") {
			t.Errorf("migration %s contains dynamic SQL", filepath.Base(path))
		}
	}
}
