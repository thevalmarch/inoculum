package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thevalmarch/inoculum/internal/version"
	"github.com/thevalmarch/inoculum/internal/workload"
)

type capturedStreams struct {
	streams Streams
	stdin   *os.File
	stdout  *os.File
	stderr  *os.File
}

func newCapturedStreams(t *testing.T) *capturedStreams {
	t.Helper()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	captured := &capturedStreams{
		streams: Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr},
		stdin:   stdin, stdout: stdout, stderr: stderr,
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
	})
	return captured
}

func readCaptured(t *testing.T, file *os.File) string {
	t.Helper()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestRootHelp(t *testing.T) {
	captured := newCapturedStreams(t)
	if code := Main([]string{"--help"}, captured.streams); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	want := `Inoculum runs independent tasks across machines on a trusted LAN.

Usage:
  inoculum <command> [flags]

Commands:
  coordinator   Start the task coordinator
  worker        Connect a worker to a coordinator
  submit        Submit independent tasks and wait for completion

Options:
  --version     Print the Inoculum version

Run "inoculum <command> --help" for command-specific options.
`
	got := readCaptured(t, captured.stdout)
	if got != want {
		t.Fatalf("root help:\n%s\nwant:\n%s", got, want)
	}
	for _, flagName := range []string{"--port", "--concurrency", "--tasks"} {
		if strings.Contains(got, flagName) {
			t.Fatalf("root help unexpectedly contains %s", flagName)
		}
	}
}

func TestRootVersion(t *testing.T) {
	previous := version.Value
	version.Value = "v1.0.0"
	t.Cleanup(func() { version.Value = previous })

	captured := newCapturedStreams(t)
	if code := Main([]string{"--version"}, captured.streams); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if got := readCaptured(t, captured.stdout); got != "inoculum v1.0.0\n" {
		t.Fatalf("version output = %q", got)
	}
	if got := readCaptured(t, captured.stderr); got != "" {
		t.Fatalf("version stderr = %q", got)
	}
}

func TestDevelopmentVersionFallback(t *testing.T) {
	previous := version.Value
	version.Value = "dev"
	t.Cleanup(func() { version.Value = previous })

	captured := newCapturedStreams(t)
	if code := Main([]string{"--version"}, captured.streams); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if got := readCaptured(t, captured.stdout); got != "inoculum dev\n" {
		t.Fatalf("version output = %q", got)
	}
}

func TestRootUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		text string
	}{
		{name: "missing command", text: "A command is required"},
		{name: "unknown command", args: []string{"serve"}, text: `Unknown command "serve"`},
		{name: "flag before command", args: []string{"--plain", "worker"}, text: "runtime flags must follow a command"},
		{name: "root help extras", args: []string{"--help", "worker"}, text: "does not accept additional arguments"},
		{name: "version extras", args: []string{"--version", "worker"}, text: "does not accept additional arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := newCapturedStreams(t)
			if code := Main(test.args, captured.streams); code != 2 {
				t.Fatalf("exit code = %d", code)
			}
			if got := readCaptured(t, captured.stderr); !strings.Contains(got, test.text) {
				t.Fatalf("stderr %q does not contain %q", got, test.text)
			}
		})
	}
}

func TestRootForwardsCommandArgumentsUnchanged(t *testing.T) {
	captured := newCapturedStreams(t)
	want := []string{"--plain", "--id", "worker-a", "value"}
	var got []string
	commands := map[string]commandRunner{
		"worker": func(args []string, streams Streams) error {
			got = append([]string(nil), args...)
			return nil
		},
	}
	if code := mainWithCommands(append([]string{"worker"}, want...), captured.streams, commands); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded args = %#v, want %#v", got, want)
	}
}

func TestCommandHelpIsPlainAndCommandSpecific(t *testing.T) {
	tests := []struct {
		command string
		flags   []string
	}{
		{command: "coordinator", flags: []string{"--port", "--lease-duration", "--max-attempts", "--token", "--audit-log", "--log-file", "--plain", "--verbose", "--no-color", "--ascii"}},
		{command: "worker", flags: []string{"--coordinator", "--id", "--concurrency", "--token", "--coordinator-fingerprint", "--log-file", "--plain", "--verbose", "--no-color", "--ascii"}},
		{command: "submit", flags: []string{"--coordinator", "--token", "--coordinator-fingerprint", "--type", "--input", "--tasks", "--manifest", "--output", "--timeout", "--log-file", "--plain", "--verbose", "--no-color", "--ascii"}},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			captured := newCapturedStreams(t)
			if code := Main([]string{test.command, "--help"}, captured.streams); code != 0 {
				t.Fatalf("exit code = %d", code)
			}
			output := readCaptured(t, captured.stdout)
			if strings.Contains(output, "\x1b[") {
				t.Fatalf("help contains ANSI: %q", output)
			}
			for _, name := range test.flags {
				if !strings.Contains(output, name) {
					t.Errorf("help does not contain %s:\n%s", name, output)
				}
			}
		})
	}
}

func TestCommandDefaultsMatchLegacyEntrypoints(t *testing.T) {
	captured := newCapturedStreams(t)
	coordinator, err := parseCoordinatorOptions(nil, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if coordinator.port != 8080 || coordinator.leaseDuration != 6*time.Second || coordinator.maxAttempts != 3 || coordinator.auditLog != "" || coordinator.presentation.logFile != "inoculum-coordinator.log" {
		t.Fatalf("coordinator defaults = %#v", coordinator)
	}

	worker, err := parseWorkerOptions(nil, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if worker.coordinator != "" || worker.workerID != "" || worker.concurrency != 1 || worker.presentation.logFile != "inoculum-worker.log" {
		t.Fatalf("worker defaults = %#v", worker)
	}

	submit, err := parseSubmitOptions(nil, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if submit.coordinator != "localhost:8080" || submit.taskType != "diagnostic_sleep" || submit.input != "1s" || submit.taskCount != 1 || submit.timeout != 30*time.Minute || submit.presentation.logFile != "inoculum-submit.log" {
		t.Fatalf("submit defaults = %#v", submit)
	}
	if submit.manifestPath != "" || submit.outputPath != "" {
		t.Fatalf("submit manifest defaults = %#v", submit)
	}
}

func TestManifestSubmitFlagsParse(t *testing.T) {
	captured := newCapturedStreams(t)
	manifestPath := filepath.Join(t.TempDir(), "probes.json")
	outputPath := filepath.Join(t.TempDir(), "results.json")
	options, err := parseSubmitOptions([]string{
		"--manifest", manifestPath, "--output", outputPath, "--timeout", "5m", "--plain",
	}, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if options.manifestPath != manifestPath || options.outputPath != outputPath || options.timeout != 5*time.Minute || !options.presentation.plain {
		t.Fatalf("manifest options = %#v", options)
	}
}

func TestManifestModeConflictsAreUsageErrors(t *testing.T) {
	for _, conflicting := range []string{"--type", "--input", "--tasks"} {
		t.Run(strings.TrimPrefix(conflicting, "--"), func(t *testing.T) {
			captured := newCapturedStreams(t)
			args := []string{"submit", "--manifest", "probes.json", conflicting}
			switch conflicting {
			case "--type":
				args = append(args, "http_probe")
			case "--input":
				args = append(args, "https://example.com")
			case "--tasks":
				args = append(args, "2")
			}
			if code := Main(args, captured.streams); code != 2 {
				t.Fatalf("exit code = %d", code)
			}
			if got := readCaptured(t, captured.stderr); !strings.Contains(got, "--manifest cannot be combined") {
				t.Fatalf("stderr = %q", got)
			}
		})
	}
}

func TestManifestAndOutputPathErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "empty manifest value", args: []string{"submit", "--manifest="}, want: "--manifest requires a path"},
		{name: "output without manifest", args: []string{"submit", "--output", "results.json"}, want: "--output requires --manifest"},
		{name: "missing manifest", args: []string{"submit", "--manifest", filepath.Join(t.TempDir(), "missing.json")}, want: "read manifest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := newCapturedStreams(t)
			if code := Main(test.args, captured.streams); code != 2 {
				t.Fatalf("exit code = %d", code)
			}
			if got := readCaptured(t, captured.stderr); !strings.Contains(got, test.want) {
				t.Fatalf("stderr %q does not contain %q", got, test.want)
			}
		})
	}
}

func TestAllRetainedFlagsParse(t *testing.T) {
	captured := newCapturedStreams(t)
	coordinator, err := parseCoordinatorOptions([]string{
		"--port", "9000", "--lease-duration", "15s", "--max-attempts", "5", "--token", "secret", "--audit-log", "audit.json", "--log-file", "coord.log", "--plain", "--verbose", "--no-color", "--ascii",
	}, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if coordinator.port != 9000 || coordinator.leaseDuration != 15*time.Second || coordinator.maxAttempts != 5 || coordinator.token != "secret" || coordinator.auditLog != "audit.json" || !coordinator.presentation.plain || !coordinator.presentation.verbose || !coordinator.presentation.noColor || !coordinator.presentation.ascii || coordinator.presentation.logFile != "coord.log" {
		t.Fatalf("coordinator options = %#v", coordinator)
	}

	worker, err := parseWorkerOptions([]string{
		"--coordinator", "host:9000", "--id", "worker-a", "--concurrency", "4", "--token", "secret", "--coordinator-fingerprint", "AA:BB", "--log-file", "worker.log", "--plain", "--verbose", "--no-color", "--ascii",
	}, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if worker.coordinator != "host:9000" || worker.workerID != "worker-a" || worker.concurrency != 4 || worker.token != "secret" || worker.fingerprint != "AA:BB" || !worker.presentation.plain || !worker.presentation.verbose || !worker.presentation.noColor || !worker.presentation.ascii || worker.presentation.logFile != "worker.log" {
		t.Fatalf("worker options = %#v", worker)
	}

	submit, err := parseSubmitOptions([]string{
		"--coordinator", "host:9000", "--token", "secret", "--coordinator-fingerprint", "AA:BB", "--type", "diagnostic_sleep", "--input", "1s", "--tasks", "4", "--timeout", "2m", "--log-file", "submit.log", "--plain", "--verbose", "--no-color", "--ascii",
	}, captured.streams)
	if err != nil {
		t.Fatal(err)
	}
	if submit.coordinator != "host:9000" || submit.token != "secret" || submit.fingerprint != "AA:BB" || submit.taskType != "diagnostic_sleep" || submit.input != "1s" || submit.taskCount != 4 || submit.timeout != 2*time.Minute || !submit.presentation.plain || !submit.presentation.verbose || !submit.presentation.noColor || !submit.presentation.ascii || submit.presentation.logFile != "submit.log" {
		t.Fatalf("submit options = %#v", submit)
	}
}

func TestRemovedAndUnknownFlagsFailAsUsage(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "coordinator discovery", command: "coordinator", args: []string{"--discovery=false"}},
		{name: "worker mode", command: "worker", args: []string{"--mode", "pull"}},
		{name: "worker allowed paths", command: "worker", args: []string{"--allowed-paths", "."}},
		{name: "unknown", command: "submit", args: []string{"--wat"}},
		{name: "positional", command: "submit", args: []string{"extra"}},
		{name: "invalid integer", command: "worker", args: []string{"--concurrency", "many"}},
		{name: "invalid duration", command: "submit", args: []string{"--timeout", "later"}},
		{name: "missing flag value", command: "coordinator", args: []string{"--port"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := newCapturedStreams(t)
			if code := Main(append([]string{test.command}, test.args...), captured.streams); code != 2 {
				t.Fatalf("exit code = %d", code)
			}
			if got := strings.Count(readCaptured(t, captured.stdout), "Usage:"); got != 1 {
				t.Fatalf("usage block count = %d, want 1", got)
			}
		})
	}
}

func TestInvalidSemanticFlagValuesExitTwo(t *testing.T) {
	t.Setenv("INOCULUM_TOKEN", "secret")
	for _, args := range [][]string{
		{"coordinator", "--port", "0"},
		{"coordinator", "--port", "65536"},
		{"coordinator", "--lease-duration", "0s"},
		{"coordinator", "--lease-duration", "-1s"},
		{"coordinator", "--max-attempts", "0"},
		{"coordinator", "--max-attempts", "-1"},
		{"worker", "--coordinator", "localhost:8080", "--concurrency", "0"},
		{"worker", "--coordinator", "localhost:8080", "--id", "bad id"},
		{"submit", "--tasks", "0"},
		{"submit", "--tasks", "1001"},
		{"submit", "--input", strings.Repeat("x", workload.MaxInputBytes+1)},
		{"submit", "--type", "bad\ntype"},
	} {
		captured := newCapturedStreams(t)
		if code := Main(args, captured.streams); code != 2 {
			t.Fatalf("Main(%v) exit code = %d", args, code)
		}
	}
}

func TestCoordinatorPolicyValidationMessages(t *testing.T) {
	t.Setenv("INOCULUM_TOKEN", "secret")
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"coordinator", "--lease-duration", "0s"}, want: "--lease-duration must be greater than 0"},
		{args: []string{"coordinator", "--max-attempts", "0"}, want: "--max-attempts must be at least 1"},
	}
	for _, test := range tests {
		captured := newCapturedStreams(t)
		if code := Main(test.args, captured.streams); code != 2 {
			t.Fatalf("Main(%v) exit code = %d", test.args, code)
		}
		if got := readCaptured(t, captured.stderr); !strings.Contains(got, test.want) {
			t.Fatalf("stderr %q does not contain %q", got, test.want)
		}
	}
}

func TestCoordinatorPolicyHelpIsActionable(t *testing.T) {
	captured := newCapturedStreams(t)
	if code := Main([]string{"coordinator", "--help"}, captured.streams); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	help := readCaptured(t, captured.stdout)
	for _, want := range []string{
		"--lease-duration",
		"Time a worker owns a claimed task before the lease expires unless renewed",
		"--max-attempts",
		"Maximum execution attempts before a task is permanently failed",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help does not contain %q:\n%s", want, help)
		}
	}
}

func TestMissingRequiredConfigurationExitsTwo(t *testing.T) {
	t.Setenv("INOCULUM_TOKEN", "")
	for _, args := range [][]string{
		{"coordinator", "--plain"},
		{"worker", "--plain", "--token", "secret"},
		{"submit", "--plain"},
	} {
		captured := newCapturedStreams(t)
		if code := Main(args, captured.streams); code != 2 {
			t.Fatalf("Main(%v) exit code = %d", args, code)
		}
	}
}

func TestTokenFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("INOCULUM_TOKEN", "from-env")
	if got, err := resolveToken(""); err != nil || got != "from-env" {
		t.Fatalf("environment token = %q, %v", got, err)
	}
	if got, err := resolveToken("from-flag"); err != nil || got != "from-flag" {
		t.Fatalf("flag token = %q, %v", got, err)
	}
}

func TestExitCodeMappingAndNoDuplicateReportedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		text string
	}{
		{name: "help", err: flag.ErrHelp, code: 0},
		{name: "usage", err: usageErrorf("bad usage"), code: 2, text: "bad usage"},
		{name: "runtime", err: runtimeError(errors.New("startup failed"), false), code: 1, text: "startup failed"},
		{name: "reported runtime", err: reportedRuntimeError(errors.New("already shown")), code: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := newCapturedStreams(t)
			if code := renderExit(test.err, captured.streams); code != test.code {
				t.Fatalf("exit code = %d", code)
			}
			output := readCaptured(t, captured.stderr)
			if test.text == "" && output != "" {
				t.Fatalf("unexpected stderr %q", output)
			}
			if test.text != "" && !strings.Contains(output, test.text) {
				t.Fatalf("stderr %q does not contain %q", output, test.text)
			}
		})
	}
}
