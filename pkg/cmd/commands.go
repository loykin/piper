// Package cmd provides piper's cobra commands as a library.
// External apps such as data-voyager can add piper commands to their own CLI.
//
//	import pipercmd "github.com/piper/piper/pkg/cmd"
//
//	// Add piper commands to the voyager CLI
//	rootCmd.AddCommand(pipercmd.Commands(p)...)
//
//	// Result:
//	// voyager pipeline run train.yaml
//	// voyager pipeline server
//	// voyager pipeline worker --master ...
package cmd

import (
	"github.com/piper/piper/pkg/piper"
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
