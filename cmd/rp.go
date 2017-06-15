package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// rpCmd represents the rp command
var rpCmd = &cobra.Command{
	Use:   "reverse-proxy",
	Short: "A simple reverse proxy.",
	Long:  `A simple reverse proxy.`,
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: Work your own magic here
		fmt.Println("rp called")
	},
}

func init() {
	RootCmd.AddCommand(rpCmd)
}
