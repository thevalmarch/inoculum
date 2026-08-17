// Command inoculum dispatches the coordinator, worker, and submit commands.
package main

import (
	"os"

	"github.com/inoculum/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.SystemStreams()))
}
