package presentation

import (
	"io"
	"log"
	"os"
)

// ConfigureOperationalLogging keeps conventional logs away from an active
// terminal screen. Plain mode continues to emit them to stderr.
func ConfigureOperationalLogging(interactive bool, path string) (io.Closer, error) {
	if !interactive {
		log.SetOutput(os.Stderr)
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	log.SetOutput(file)
	return file, nil
}
