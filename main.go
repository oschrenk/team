package main

import (
	_ "embed"
	"strings"

	"github.com/oschrenk/team/cmd"
)

//go:embed VERSION
var versionRaw string

func main() {
	cmd.SetVersion(strings.TrimSpace(versionRaw))
	cmd.Execute()
}
