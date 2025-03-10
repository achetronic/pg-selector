package run

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"pg-selector/internal/pgs/watcher"

	"github.com/spf13/cobra"
)

const (
	//
	errorMessageFlag = "error in '%s' flag: %s"

	flagNameLogLevel         = "log-level"
	flagNameSyncTime         = "sync-time"
	flagNameServicesCreation = "services-creation"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "run",
		DisableFlagsInUseLine: true,
		Short:                 `Execute replication-role labels synchronizer`,
		Long: `
	Run execute replication-role labels synchronizer`,

		Run: RunCommand,
	}

	//
	cmd.Flags().String(flagNameLogLevel, "info", "Verbosity level for logs")
	cmd.Flags().String(flagNameSyncTime, "5s", "Synchronization time in seconds")
	cmd.Flags().Bool(flagNameServicesCreation, true, "Enable the creation of the services")

	return cmd
}

// RunCommand TODO
func RunCommand(cmd *cobra.Command, args []string) {
	logLevelFlag, err := cmd.Flags().GetString(flagNameLogLevel)
	if err != nil {
		log.Fatalf(errorMessageFlag, flagNameLogLevel, err.Error())
	}

	syncTimeFlag, err := cmd.Flags().GetString(flagNameSyncTime)
	if err != nil {
		log.Fatalf(errorMessageFlag, flagNameSyncTime, err.Error())
	}
	syncTime, err := time.ParseDuration(syncTimeFlag)
	if err != nil {
		log.Fatalf("error parsing sync-time flag: %s", err.Error())
	}

	servicesCreationFlag, err := cmd.Flags().GetBool(flagNameServicesCreation)
	if err != nil {
		log.Fatalf(errorMessageFlag, flagNameServicesCreation, err.Error())
	}

	/////////////////////////////
	// EXECUTION FLOW RELATED
	/////////////////////////////

	w, err := watcher.NewWatcher(watcher.OptionsT{
		LogLevel:         logLevelFlag,
		WaitTime:         syncTime,
		ServicesCreation: servicesCreationFlag,
	})
	if err != nil {
		log.Fatalf("error watcher creating instance: %s", err.Error())
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	w.Run(ctx, &wg)

	sig := <-sigs
	cancel()

	wg.Wait()

	log.Printf("shutdown worker service by system with signal: %s", sig.String())
}
