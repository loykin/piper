// Package commands provides piper's cobra commands as a library.
// External apps can add piper commands to their own CLI.
//
//	import pipercmd "github.com/piper/piper/cmd/piper/commands"
//	import cliconfig "github.com/piper/piper/cmd/piper/config"
//
//	loader := cliconfig.NewLoader()
//
//	// Inject a factory to override Piper construction:
//	rootCmd.AddCommand(pipercmd.Commands(loader, func() (*piper.Piper, error) {
//	    return piper.New(myCustomConfig())
//	})...)
//
//	// Result:
//	// voyager pipeline run train.yaml
//	// voyager pipeline server
//	// voyager pipeline worker --master-url ...
package commands

import (
	piper "github.com/piper/piper"
	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"
)

// PiperFactory is a function that creates a fully configured Piper instance.
// It is called from within RunE after the canonical loader has read all sources.
type PiperFactory func() (*piper.Piper, error)

// Commands returns piper's cobra commands.
// Pass one loader per command tree. The factory may construct the standard
// Piper instance or an embedding application's custom instance.
func Commands(loader *cliconfig.Loader, factory PiperFactory) []*cobra.Command {
	return []*cobra.Command{
		newRunCmd(loader, factory),
		newParseCmd(),
		newServerCmd(loader, factory),
		newWorkerCmd(loader),
		newAgentCmd(),
		newK8sWorkerCmd(loader),
		newServingWorkerCmd(loader),
		newNotebookWorkerCmd(loader),
		newInternalCmd(),
		newUserCmd(loader),
		newConfigCmd(loader),
	}
}
