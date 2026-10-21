// Command empirio starts coding agents on EmpirioLabs AI models without
// changing their settings.
package main

import (
	"os"

	"github.com/EmpirioLabs-AI/cli/internal/app"
)

// version is set by the release build: -ldflags "-X main.version=<version>".
var version = "0.0.0-dev"

func main() {
	app.Version = version
	os.Exit(app.Run(os.Args[1:]))
}
