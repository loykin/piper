// Package commands provides piper's cobra commands as a library.
// External apps can add piper commands to their own CLI.
//
//	import pipercmd "github.com/piper/piper/cmd/piper/commands"
//
//	// Add piper commands to the voyager CLI
//	rootCmd.AddCommand(pipercmd.Commands(p)...)
//
//	// Result:
//	// voyager pipeline run train.yaml
//	// voyager pipeline server
//	// voyager pipeline worker --master ...
package commands

import (
	piper "github.com/piper/piper"
	"github.com/spf13/cobra"
)

// Commands returns a slice of cobra commands bound to the given piper instance.
// Add the returned commands to any parent command using AddCommand.
func Commands(p *piper.Piper) []*cobra.Command {
	return []*cobra.Command{
		newRunCmd(p),
		newParseCmd(p),
		newServerCmd(p),
		newWorkerCmd(p),
		newAgentCmd(),
	}
}
