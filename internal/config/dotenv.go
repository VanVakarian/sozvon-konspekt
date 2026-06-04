package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

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
