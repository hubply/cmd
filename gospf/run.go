package main

import (
	"github.com/hubply/cmd/harness"
	"github.com/hubply/gospf"
	"strconv"
)

var cmdRun = &Command{
	UsageLine: "run [import path] [run mode] [port]",
	Short:     "run a Revel application",
	Long: `
Run the Revel web application named by the given import path.

For example, to run the chat room sample application:

    gospf run github.com/hubply/samples/chat dev

The run mode is used to select which set of app.conf configuration should
apply and may be used to determine logic in the application itself.

Run mode defaults to "dev".

You can set a port as an optional third parameter.  For example:

    gospf run github.com/hubply/samples/chat prod 8080`,
}

func init() {
	cmdRun.Run = runApp
}

func runApp(args []string) {
	if len(args) == 0 {
		errorf("No import path given.\nRun 'gospf help run' for usage.\n")
	}

	// Determine the run mode.
	mode := "dev"
	if len(args) >= 2 {
		mode = args[1]
	}

	// Find and parse app.conf
	gospf.Init(mode, args[0], "")
	gospf.LoadMimeConfig()

	// Determine the override port, if any.
	port := gospf.HttpPort
	if len(args) == 3 {
		var err error
		if port, err = strconv.Atoi(args[2]); err != nil {
			errorf("Failed to parse port as integer: %s", args[2])
		}
	}

	gospf.INFO.Printf("Running %s (%s) in %s mode\n", gospf.AppName, gospf.ImportPath, mode)
	gospf.TRACE.Println("Base path:", gospf.BasePath)

	// If the app is run in "watched" mode, use the harness to run it.
	if gospf.Config.BoolDefault("watch", true) && gospf.Config.BoolDefault("watch.code", true) {
		gospf.TRACE.Println("Running in watched mode.")
		gospf.HttpPort = port
		harness.NewHarness().Run() // Never returns.
	}

	// Else, just build and run the app.
	gospf.TRACE.Println("Running in live build mode.")
	app, err := harness.Build()
	if err != nil {
		errorf("Failed to build app: %s", err)
	}
	app.Port = port
	app.Cmd().Run()
}
