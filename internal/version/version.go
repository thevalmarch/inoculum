// Package version contains the concise release identity printed by the CLI.
package version

// Value is replaced for release builds with:
//
//	-X github.com/thevalmarch/inoculum/internal/version.Value=v1.0.0
//
// Development builds deliberately identify themselves as dev.
var Value = "dev"
