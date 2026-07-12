package worker

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Executor processes a task and returns the output.
type Executor struct{}

// NewExecutor creates a new task executor.
func NewExecutor() *Executor {
	return &Executor{}
}

// Execute runs a task based on its type and returns the output and duration.
func (e *Executor) Execute(taskType, input string) (string, time.Duration, error) {
	start := time.Now()

	var output string
	var err error

	switch taskType {
	case "dummy":
		output, err = e.executeDummy(input)
	case "file_analyze":
		output, err = e.executeFileAnalyze(input)
	case "http_fetch":
		output, err = e.executeHTTPFetch(input)
	default:
		err = fmt.Errorf("unknown task type: %s", taskType)
	}

	duration := time.Since(start)
	return output, duration, err
}

// executeDummy is the Phase 1 dummy executor — sleeps briefly and returns a message.
func (e *Executor) executeDummy(input string) (string, error) {
	time.Sleep(10 * time.Millisecond)
	return fmt.Sprintf("dummy result for input: %s", input), nil
}

// executeFileAnalyze counts lines, words, and bytes of a file using pure Go (no shell).
func (e *Executor) executeFileAnalyze(input string) (string, error) {
	f, err := os.Open(input)
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

// executeHTTPFetch fetches a URL and returns its content length (Phase 3).
func (e *Executor) executeHTTPFetch(input string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(input)
	if err != nil {
		return "", fmt.Errorf("http_fetch error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	return fmt.Sprintf("status=%d content_length=%d", resp.StatusCode, len(body)), nil
}
