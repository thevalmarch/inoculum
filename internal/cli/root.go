package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/inoculum/internal/version"
)

type commandRunner func(args []string, streams Streams) error

var productionCommands = map[string]commandRunner{
	"coordinator": RunCoordinator,
	"worker":      RunWorker,
	"submit":      RunSubmit,
}

// Main selects exactly one command and converts its result into a process exit
// code. Signal handling remains owned by the selected command.
func Main(args []string, streams Streams) int {
	return mainWithCommands(args, streams, productionCommands)
}

func mainWithCommands(args []string, streams Streams, commands map[string]commandRunner) int {
	if len(args) == 0 {
		fmt.Fprintln(streams.Stderr, "A command is required.")
		fmt.Fprintln(streams.Stderr)
		printRootHelp(streams.Stderr)
		return 2
	}
	if isRootHelp(args[0]) {
		if len(args) != 1 {
			fmt.Fprintln(streams.Stderr, "root help does not accept additional arguments")
			return 2
		}
		printRootHelp(streams.Stdout)
		return 0
	}
	if args[0] == "--version" {
		if len(args) != 1 {
			fmt.Fprintln(streams.Stderr, "--version does not accept additional arguments")
			return 2
		}
		fmt.Fprintf(streams.Stdout, "inoculum %s\n", version.Value)
		return 0
	}
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(streams.Stderr, "unknown root flag %q; runtime flags must follow a command\n\n", args[0])
		fmt.Fprintln(streams.Stderr, `Run "inoculum --help" for available commands.`)
		return 2
	}
	runner, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(streams.Stderr, "Unknown command %q.\n\n", args[0])
		fmt.Fprintln(streams.Stderr, `Run "inoculum --help" for available commands.`)
		return 2
	}
	return renderExit(runner(args[1:], streams), streams)
}

func isRootHelp(argument string) bool {
	return argument == "--help" || argument == "-h" || argument == "-help"
}

func printRootHelp(writer io.Writer) {
	fmt.Fprintln(writer, "Inoculum runs independent tasks across machines on a trusted LAN.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  inoculum <command> [flags]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Commands:")
	fmt.Fprintln(writer, "  coordinator   Start the task coordinator")
	fmt.Fprintln(writer, "  worker        Connect a worker to a coordinator")
	fmt.Fprintln(writer, "  submit        Submit independent tasks and wait for completion")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Options:")
	fmt.Fprintln(writer, "  --version     Print the Inoculum version")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, `Run "inoculum <command> --help" for command-specific options.`)
}
