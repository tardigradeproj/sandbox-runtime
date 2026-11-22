package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tardigradeproj/sandbox-runtime/cmd/app"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox-runtime",
		Short: "sandbox-runtime runs the Firecracker-backed sandbox runtime daemon",
	}

	cmd.AddCommand(app.NewGRPCCmd(), app.NewConsoleCmd())

	return cmd
}
