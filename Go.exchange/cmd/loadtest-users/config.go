package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultLoadTestUserCount  = 10
	minLoadTestUserCount      = 1
	maxLoadTestUserCount      = 20
	defaultLoadTestUserPrefix = "loadtestv1"
)

type loadTestUserConfig struct {
	Password string
	Count    int
	Prefix   string
}

func loadTestUserConfigFromEnvironment() (loadTestUserConfig, error) {
	return loadTestUserConfigFromEnv(os.LookupEnv)
}

func loadTestUserConfigFromEnv(lookup func(string) (string, bool)) (loadTestUserConfig, error) {
	password, ok := lookup("LOADTEST_USER_PASSWORD")
	if !ok || strings.TrimSpace(password) == "" {
		return loadTestUserConfig{}, errors.New("LOADTEST_USER_PASSWORD is required")
	}

	count := defaultLoadTestUserCount
	if raw, exists := lookup("LOADTEST_USER_COUNT"); exists && strings.TrimSpace(raw) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return loadTestUserConfig{}, errors.New("LOADTEST_USER_COUNT must be an integer")
		}
		count = parsed
	}
	if count < minLoadTestUserCount || count > maxLoadTestUserCount {
		return loadTestUserConfig{}, fmt.Errorf("LOADTEST_USER_COUNT must be between %d and %d", minLoadTestUserCount, maxLoadTestUserCount)
	}

	prefix := defaultLoadTestUserPrefix
	if raw, exists := lookup("LOADTEST_USER_PREFIX"); exists && strings.TrimSpace(raw) != "" {
		prefix = strings.TrimSpace(raw)
	}
	if err := validateLoadTestUserPrefix(prefix); err != nil {
		return loadTestUserConfig{}, err
	}

	return loadTestUserConfig{Password: password, Count: count, Prefix: prefix}, nil
}

func validateLoadTestUserPrefix(prefix string) error {
	if !strings.HasPrefix(prefix, "loadtest") {
		return errors.New("LOADTEST_USER_PREFIX must start with loadtest")
	}
	if prefix == "" {
		return errors.New("LOADTEST_USER_PREFIX must not be empty")
	}
	for index := 0; index < len(prefix); index++ {
		character := prefix[index]
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return errors.New("LOADTEST_USER_PREFIX may contain only letters, digits, underscore, and hyphen")
	}
	return nil
}

func syntheticUserUsername(prefix string, index int) string {
	return fmt.Sprintf("%s_%04d", prefix, index)
}

func syntheticUserDisplayName(index int) string {
	return fmt.Sprintf("Load Test User %04d", index)
}
