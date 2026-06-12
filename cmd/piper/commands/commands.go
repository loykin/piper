// Package commands provides piper's cobra commands as a library.
// External apps can add piper commands to their own CLI.
//
//	import pipercmd "github.com/piper/piper/cmd/piper/commands"
//
//	// Embed with the standard piper factory:
//	rootCmd.AddCommand(pipercmd.Commands(pipercmd.NewPiper)...)
//
//	// Or inject a custom factory to override Piper construction:
//	rootCmd.AddCommand(pipercmd.Commands(func() (*piper.Piper, error) {
//	    return piper.New(myCustomConfig())
//	})...)
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

// PiperFactory is a function that creates a fully configured Piper instance.
// It is called from within RunE, after cobra has parsed all flags and the
// config file has been loaded, so viper values are always fully initialized.
type PiperFactory func() (*piper.Piper, error)

// Commands returns piper's cobra commands.
// Pass factory=NewPiper for the standard CLI binary; pass a custom factory
// when embedding piper commands in another application.
func Commands(factory PiperFactory) []*cobra.Command {
	return []*cobra.Command{
		newRunCmd(factory),
		newParseCmd(),
		newServerCmd(factory),
		newWorkerCmd(),
		newAgentCmd(),
		newK8sWorkerCmd(),
		newServingWorkerCmd(),
		newNotebookWorkerCmd(),
		newInternalCmd(),
		newUserCmd(),
	}
}
