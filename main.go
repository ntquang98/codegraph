package main

import (
	"embed"
	"io/fs"

	"github.com/codegraph-cli/codegraph/cmd"
)

//go:embed frontend
var frontendAssets embed.FS

func main() {
	// Inject the embedded frontend filesystem into the ui command before
	// executing the root cobra command.
	subFS, err := fs.Sub(frontendAssets, "frontend")
	if err != nil {
		panic("embed sub-fs: " + err.Error())
	}
	cmd.SetFrontendFS(subFS)
	cmd.Execute()
}
