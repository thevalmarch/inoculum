package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/inoculum/internal/presentation"
)

// Streams are the process streams used by a selected command. They remain
// files because terminal capability detection requires file descriptors.
type Streams struct {
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

func SystemStreams() Streams {
	return Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}

type presentationFlags struct {
	plain   bool
	verbose bool
	noColor bool
	ascii   bool
	logFile string
}

func addPresentationFlags(set *flag.FlagSet, options *presentationFlags, defaultLogFile string) {
	set.StringVar(&options.logFile, "log-file", defaultLogFile, "Operational log used by the interactive terminal UI")
	set.BoolVar(&options.plain, "plain", false, "Use stable line-oriented terminal output")
	set.BoolVar(&options.verbose, "verbose", false, "Show additional diagnostic detail")
	set.BoolVar(&options.noColor, "no-color", false, "Disable terminal colors")
	set.BoolVar(&options.ascii, "ascii", false, "Use ASCII symbols only")
}

func configurePresentation(component string, options presentationFlags, streams Streams) (presentation.Capabilities, io.Closer) {
	caps := presentation.Detect(presentation.ModeOptions{
		Plain: options.plain, Verbose: options.verbose, NoColor: options.noColor, ASCII: options.ascii,
	}, streams.Stdin, streams.Stdout)
	operationalLog, err := presentation.ConfigureOperationalLogging(caps.Interactive, options.logFile)
	if err != nil {
		caps.Interactive = false
		log.Printf("[%s] Could not initialize terminal log %s: %v; using plain mode", component, options.logFile, err)
		operationalLog, _ = presentation.ConfigureOperationalLogging(false, "")
	}
	return caps, operationalLog
}

func resolveToken(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if token := os.Getenv("INOCULUM_TOKEN"); token != "" {
		return token, nil
	}
	return "", usageErrorf("--token or INOCULUM_TOKEN is required")
}

func newFlagSet(command, description, usage string, streams Streams) *flag.FlagSet {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(streams.Stderr)
	set.Usage = func() {
		fmt.Fprintf(streams.Stdout, "%s\n\nUsage:\n  %s\n\nFlags:\n", description, usage)
		printFlagDefaults(streams.Stdout, set)
	}
	return set
}

// printFlagDefaults keeps the standard library's default/help formatting while
// presenting the long flag spelling used in Inoculum examples.
func printFlagDefaults(writer io.Writer, set *flag.FlagSet) {
	var output bytes.Buffer
	originalOutput := set.Output()
	set.SetOutput(&output)
	set.PrintDefaults()
	set.SetOutput(originalOutput)

	text := output.String()
	if strings.HasPrefix(text, "  -") {
		text = "  --" + strings.TrimPrefix(text, "  -")
	}
	text = strings.ReplaceAll(text, "\n  -", "\n  --")
	_, _ = io.WriteString(writer, text)
}

func parseFlagSet(set *flag.FlagSet, args []string, streams Streams) error {
	if err := set.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return flag.ErrHelp
		}
		return reportedUsageError(err)
	}
	if set.NArg() != 0 {
		fmt.Fprintf(streams.Stderr, "unexpected arguments: %v\n", set.Args())
		set.Usage()
		return reportedUsageError(fmt.Errorf("unexpected positional arguments"))
	}
	return nil
}
