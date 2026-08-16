package forge

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

func validatePullRequestRequest(request PullRequestRequest) error {
	if !operationIDPattern.MatchString(request.OperationID) || !filepath.IsAbs(request.WorktreePath) ||
		filepath.Clean(request.WorktreePath) != request.WorktreePath || !branchPattern.MatchString(request.Branch) ||
		strings.Contains(request.Branch, "..") || !revisionPattern.MatchString(request.HeadRevision) ||
		request.Title == "" || len(request.Title) > 256 || strings.TrimSpace(request.Title) != request.Title ||
		strings.ContainsAny(request.Title, "\x00\r\n") || validateRequiredChecks(request.RequiredChecks) != nil {
		return errors.New("deliver GitHub pull request: request is invalid")
	}
	return nil
}

func validateRequiredChecks(required []string) error {
	if len(required) == 0 || len(required) > 64 {
		return errors.New("required checks are invalid")
	}
	seen := make(map[string]struct{}, len(required))
	for _, check := range required {
		if check == "" || len(check) > 128 || strings.TrimSpace(check) != check || strings.ContainsAny(check, "\x00\r\n") {
			return errors.New("required check is invalid")
		}
		if _, exists := seen[check]; exists {
			return errors.New("required check is duplicated")
		}
		seen[check] = struct{}{}
	}
	return nil
}

func validReadCredential(credential Credential) bool {
	return credential.Kind == CredentialRead && validSecret(credential.Secret) &&
		equalScopes(credential.Scopes, []CredentialScope{ScopeContentsRead, ScopePullRequestsRead, ScopeChecksRead})
}

func validPushCredential(credential Credential) bool {
	return credential.Kind == CredentialPush && validSecret(credential.Secret) &&
		equalScopes(credential.Scopes, []CredentialScope{ScopeContentsWrite})
}

func validSecret(secret string) bool {
	return secret != "" && len(secret) <= 4096 && !strings.ContainsAny(secret, "\x00\r\n\t ")
}

func equalScopes(got, want []CredentialScope) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[CredentialScope]int, len(got))
	for _, scope := range got {
		counts[scope]++
	}
	for _, scope := range want {
		if counts[scope] != 1 {
			return false
		}
	}
	return true
}

func validatePullRequestURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("deliver GitHub pull request: pull-request URL is invalid")
	}
	return nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
