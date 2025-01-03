package help

import (
	"github.com/spf13/cobra"
)

const (
	descriptionShort = `Help about any command`

	descriptionLong = `
	Help provides help for any command in the application.
	Simply type pg-selector help [path to command] for full details.`
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "help [command] | STRING_TO_SEARCH",
		DisableFlagsInUseLine: true,
		Short:                 descriptionShort,
		Long:                  descriptionLong,

		Run: RunCommand,
	}

	return cmd
}

func RunCommand(cmd *cobra.Command, args []string) {
}
