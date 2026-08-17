package worker

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inoculum/internal/types"
	"github.com/inoculum/internal/workload"
)

const (
	defaultProbeTimeout      = 10 * time.Second
	defaultProbeMaxRedirects = 5
)

var errProbeRedirectLimit = errors.New("HTTP probe redirect limit exceeded")

// Executor processes a task and returns the output.
type Executor struct {
	allowedPaths      []string
	probeTransport    http.RoundTripper
	probeTimeout      time.Duration
	probeMaxRedirects int
}

// NewExecutor creates a new task executor.
func NewExecutor(allowedPaths []string) *Executor {
	var resolvedPaths []string
	for _, p := range allowedPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue // skip invalid paths
		}
		// Resolve symlinks for the allowed path itself to establish the true root
		real, err := filepath.EvalSymlinks(abs)
		if err == nil {
			resolvedPaths = append(resolvedPaths, real)
		} else if os.IsNotExist(err) {
			// If it doesn't exist yet, we still track the absolute path
			resolvedPaths = append(resolvedPaths, abs)
		}
	}
	return &Executor{
		allowedPaths:      resolvedPaths,
		probeTransport:    http.DefaultTransport,
		probeTimeout:      defaultProbeTimeout,
		probeMaxRedirects: defaultProbeMaxRedirects,
	}
}

// Execute runs a task based on its type and returns the output and duration.
func (e *Executor) Execute(taskType, input string) (string, time.Duration, error) {
	start := time.Now()

	var output string
	var err error

	switch taskType {
	case "dummy":
		output, err = e.executeDummy(input)
	case "diagnostic_sleep":
		output, err = e.executeDiagnosticSleep(input)
	case "file_analyze":
		output, err = e.executeFileAnalyze(input)
	case workload.HTTPProbeType:
		output, err = e.executeHTTPProbe(input)
	default:
		err = fmt.Errorf("unknown task type: %s", taskType)
	}

	duration := time.Since(start)
	return output, duration, err
}

// executeDiagnosticSleep is a duplicate-safe workload for validating leases,
// retries, and worker failure. It intentionally has no external side effects.
func (e *Executor) executeDiagnosticSleep(input string) (string, error) {
	duration, err := time.ParseDuration(input)
	if err != nil || duration <= 0 || duration > 5*time.Minute {
		return "", fmt.Errorf("diagnostic_sleep requires a duration between 1ns and 5m")
	}
	time.Sleep(duration)
	return fmt.Sprintf("slept for %s", duration), nil
}

// executeDummy is the Phase 1 dummy executor — sleeps briefly and returns a message.
func (e *Executor) executeDummy(input string) (string, error) {
	time.Sleep(10 * time.Millisecond)
	return fmt.Sprintf("dummy result for input: %s", input), nil
}

// executeFileAnalyze counts lines, words, and bytes of a file using pure Go (no shell).
func (e *Executor) executeFileAnalyze(input string) (string, error) {
	// 1. Resolve absolute path
	absInput, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// 2. Resolve symlinks to get the real underlying path
	realInput, err := filepath.EvalSymlinks(absInput)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate symlinks: %w", err)
	}

	// 3. Strict boundary check against allowed paths
	allowed := false
	for _, allowedPath := range e.allowedPaths {
		rel, err := filepath.Rel(allowedPath, realInput)
		if err != nil {
			continue
		}
		// If the relative path doesn't start with ".." and is not "..",
		// then it is inside the allowed directory.
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			allowed = true
			break
		}
	}

	if !allowed {
		return "", fmt.Errorf("path traversal attempt blocked: %s is outside allowed directories", input)
	}

	f, err := os.Open(realInput)
	if err != nil {
		return "", fmt.Errorf("file_analyze error: %w", err)
	}
	defer f.Close()

	var lines, words, bytes int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines++
		line := scanner.Text()
		bytes += len(line) + 1 // +1 for newline
		inWord := false
		for _, r := range line {
			if r == ' ' || r == '\t' || r == '\r' {
				inWord = false
			} else if !inWord {
				inWord = true
				words++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("file_analyze read error: %w", err)
	}

	return fmt.Sprintf("lines=%d words=%d bytes=%d file=%s", lines, words, bytes, input), nil
}

// executeHTTPProbe performs one bounded HEAD request. The Inoculum bearer
// token is never available to this request and therefore cannot be forwarded.
func (e *Executor) executeHTTPProbe(input string) (string, error) {
	startedAt := time.Now()
	target, category, message := validateProbeURL(input)
	if message != "" {
		return marshalProbeFailure(startedAt, category, message)
	}

	timeout := e.probeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return marshalProbeFailure(startedAt, "invalid_url", "could not create HTTP probe request")
	}
	client := &http.Client{
		Transport: e.probeTransport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			limit := e.probeMaxRedirects
			if limit <= 0 {
				limit = defaultProbeMaxRedirects
			}
			if len(via) > limit {
				return errProbeRedirectLimit
			}
			if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return errors.New("HTTP probe redirect used an unsupported scheme")
			}
			if request.URL.User != nil {
				return errors.New("HTTP probe redirect contained URL credentials")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		category, message = classifyProbeError(err)
		return marshalProbeFailure(startedAt, category, message)
	}
	defer response.Body.Close()

	output := types.HTTPProbeOutput{
		StatusCode:          response.StatusCode,
		FinalURL:            response.Request.URL.String(),
		ElapsedMilliseconds: time.Since(startedAt).Milliseconds(),
	}
	if response.ContentLength >= 0 {
		contentLength := response.ContentLength
		output.DeclaredContentLength = &contentLength
	}
	if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		output.TLSCertificateExpiry = response.TLS.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("encode HTTP probe result: %w", err)
	}
	return string(encoded), nil
}

func validateProbeURL(input string) (*url.URL, string, string) {
	target, err := url.ParseRequestURI(input)
	if err != nil || target.Host == "" {
		return nil, "invalid_url", "HTTP probe requires a valid absolute URL"
	}
	target.Scheme = strings.ToLower(target.Scheme)
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, "unsupported_scheme", "HTTP probe allows only http and https URLs"
	}
	if target.User != nil {
		return nil, "url_credentials", "HTTP probe URLs must not contain credentials"
	}
	return target, "", ""
}

func marshalProbeFailure(startedAt time.Time, category, message string) (string, error) {
	output := types.HTTPProbeOutput{
		ElapsedMilliseconds: time.Since(startedAt).Milliseconds(),
		ErrorCategory:       category,
		ErrorMessage:        message,
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("encode HTTP probe failure: %w", err)
	}
	return string(encoded), errors.New(message)
}

func classifyProbeError(err error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "HTTP probe timed out"
	}
	if errors.Is(err, errProbeRedirectLimit) {
		return "redirect_limit", fmt.Sprintf("HTTP probe exceeded the %d-redirect limit", defaultProbeMaxRedirects)
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns", "HTTP probe DNS lookup failed"
	}
	var certificateError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	if errors.As(err, &certificateError) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) {
		return "tls", "HTTP probe TLS verification failed"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return "connection", "HTTP probe connection failed"
	}
	return "request", "HTTP probe request failed"
}
