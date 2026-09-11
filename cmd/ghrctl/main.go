package main

import (
	"fmt"
	"os"

	"github.com/hkgdevx/hosted-github-runner/internal/ghrctl"
)

var version = "dev"
var commit = "unknown"
var image = ""

func main() {
	if err := ghrctl.Run(os.Args[1:], ghrctl.Build{Version: version, Commit: commit, Image: image}); err != nil {
		fmt.Fprintln(os.Stderr, "ghrctl:", err)
		os.Exit(1)
	}
}
