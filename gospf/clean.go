package main

import (
	"fmt"
	"go/build"
	"os"
	"path"
)

var cmdClean = &Command{
	UsageLine: "clean [import path]",
	Short:     "clean a Gospf application's temp files",
	Long: `
Clean the Gospf web application named by the given import path.

For example:

    gospf clean github.com/gospf/samples/chat

It removes the app/tmp directory.
`,
}

func init() {
	cmdClean.Run = cleanApp
}

func cleanApp(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, cmdClean.Long)
		return
	}

	appPkg, err := build.Import(args[0], "", build.FindOnly)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Abort: Failed to find import path:", err)
		return
	}

	// Remove the app/tmp directory.
	tmpDir := path.Join(appPkg.Dir, "app", "tmp")
	fmt.Println("Removing:", tmpDir)
	err = os.RemoveAll(tmpDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Abort:", err)
		return
	}
}
