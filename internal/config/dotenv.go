package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const envExampleFile = ".env.example"

// pickEnvFile resolves which env file to load, in priority order: a bare
// ".env" first, then the alphabetically-first ".env.<id>" file in the
// working directory (skipping the tracked-in-git example template). Neither
// existing is a deploy mistake, not a state the binary should run with.
func pickEnvFile() (string, error) {
	if fileExists(".env") {
		return ".env", nil
	}

	matches, err := filepath.Glob(".env.*")
	if err != nil {
		return "", fmt.Errorf("glob env files: %w", err)
	}
	sort.Strings(matches)

	for _, match := range matches {
		if match == envExampleFile {
			continue
		}
		if fileExists(match) {
			return match, nil
		}
	}

	return "", errors.New("no .env or .env.<id> file found in working directory")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func loadDotEnvFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf(".env line %d: missing '='", lineNumber)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf(".env line %d: empty key", lineNumber)
		}

		value = strings.TrimSpace(value)
		value = trimMatchingQuotes(value)

		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf(".env line %d: %w", lineNumber, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func trimMatchingQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	if value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}

	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}

	return value
}
