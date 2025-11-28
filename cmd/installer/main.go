package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	controllers "xiaoshiai.cn/installer/controller"
)

const ErrExitCode = 1

func main() {
	if err := NewInstallerCmd().Execute(); err != nil {
		fmt.Println(err.Error())
		os.Exit(ErrExitCode)
	}
}

func NewInstallerCmd() *cobra.Command {
	options := controllers.NewDefaultOptions()
	cmd := &cobra.Command{
		Use:   "installer",
		Short: "run installer controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return controllers.Run(ctx, options)
		},
	}
	cmd.Flags().StringVarP(&options.MetricsAddr, "metrics-addr", "", options.MetricsAddr, "metrics address")
	cmd.Flags().StringVarP(&options.ProbeAddr, "probe-addr", "", options.ProbeAddr, "probe address")
	cmd.Flags().BoolVarP(&options.LeaderElection, "leader-elect", "", options.LeaderElection, "enable leader election")
	cmd.Flags().StringVarP(&options.LeaderElectionID, "leader-elect-id", "", options.LeaderElectionID, "leader election id")
	cmd.Flags().StringVarP(&options.CacheDir, "cache-dir", "", options.CacheDir, "cache directory for downloaded bundle charts")
	return cmd
}
