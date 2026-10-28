// Copyright 2026 Woodpecker Authors
// Licensed under the Apache License, Version 2.0 (the "License").
package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The optional machine identity is statically assigned to these repositories.
// It is never selected because another token returned HTTP 403 or 429.
const (
	wfo882Login            = "only50bw"
	wfo882EnabledKey       = "WOODPECKER_WFO882_STATUS_IDENTITY_ENABLED"
	wfo882OwnerKey         = "WOODPECKER_WFO882_OWNER_APPROVED"
	wfo882ComplianceKey    = "WOODPECKER_WFO882_COMPLIANCE_APPROVED"
	wfo882TokenFileKey     = "WOODPECKER_WFO882_TOKEN_FILE"
)

type wfo882StatusRoute struct {
	token         string
	once          sync.Once
	verificationErr error
}

func wfo882AllowedStatusRepository(owner, repo string) bool {
	if owner != "sevranty" {
		return false
	}
	switch repo {
	case "telegram-bot-hub", "servers", "agent-execution-fabric", "web-factory-os":
		return true
	default:
		return false
	}
}

// wfo882LoadStatusRoute only loads an explicitly approved static credential.
// A second identity is NOT a quota-fallback mechanism. Operator flags alone
// do not prove contractual compliance or authorize a live deployment.
func wfo882LoadStatusRoute(getenv func(string) string, readToken func(string) (string, error)) (*wfo882StatusRoute, error) {
	enabled := getenv(wfo882EnabledKey)
	owner := getenv(wfo882OwnerKey)
	compliance := getenv(wfo882ComplianceKey)
	path := getenv(wfo882TokenFileKey)

	if enabled == "" && owner == "" && compliance == "" && path == "" {
		return nil, nil
	}
	if enabled != "1" || owner != "1" || compliance != "1" || path == "" {
		return nil, errors.New("WFO882_PROTECTED_ADMISSION_REQUIRED")
	}
	token, err := readToken(path)
	if err != nil {
		return nil, errors.New("WFO882_TOKEN_FILE_UNAVAILABLE")
	}
	if token == "" || strings.ContainsAny(token, "\r\n \t") {
		return nil, errors.New("WFO882_TOKEN_FORMAT_INVALID")
	}
	return &wfo882StatusRoute{token: token}, nil
}

// A protected file is mandatory; no PAT is read from command-line arguments,
// ordinary environment variables, GitHub Issues, logs or repository files.
func wfo882ReadProtectedToken(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("WFO882_TOKEN_PATH_INVALID")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 ||
		info.Size() < 8 || info.Size() > 4096 {
		return "", errors.New("WFO882_TOKEN_FILE_UNSAFE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("WFO882_TOKEN_READ_FAILED")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n \t") {
		return "", errors.New("WFO882_TOKEN_FORMAT_INVALID")
	}
	return token, nil
}

// selectToken is invoked only for a non-deployment status in an allowed repo.
// verify must independently call GET /user with THIS credential and confirm
// login=only50bw. A rejected verification is sticky until service restart;
// no fallback to the original token occurs after admission.
func (r *wfo882StatusRoute) selectToken(
	owner, repo, existingToken string, verify func(string) error,
) (string, error) {
	if r == nil || !wfo882AllowedStatusRepository(owner, repo) {
		return existingToken, nil
	}
	r.once.Do(func() {
		r.verificationErr = verify(r.token)
	})
	if r.verificationErr != nil {
		return "", errors.New("WFO882_MACHINE_IDENTITY_NOT_VERIFIED")
	}
	return r.token, nil
}
