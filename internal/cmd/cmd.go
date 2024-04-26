package cmd

import (
	"pg-selector/internal/cmd/run"
	"pg-selector/internal/cmd/version"

	"github.com/spf13/cobra"
)

const (
	descriptionShort = `CLI that label the role of your Postgres pods on Kubernetes`

	// descriptionLong TODO
	descriptionLong = `
	PG Selector is a simple CLI to label the replication-role
	of your Postgres HA pods in Kubernetes`
)

func NewRootCommand(name string) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: descriptionShort,
		Long:  descriptionLong,
	}

	c.AddCommand(
		version.NewCommand(),
		run.NewCommand(),
	)

	return c
}
