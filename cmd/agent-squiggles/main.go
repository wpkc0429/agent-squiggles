// Command agent-squiggles gives coding agents the red squiggles that
// editors give humans: after each edit it reports the errors the edit
// introduced, straight from the language server.
package main

import (
	"os"

	"github.com/wpkc0429/agent-squiggles/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
