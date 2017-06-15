package cmd

import "github.com/spf13/cobra"

var RootCmd = &cobra.Command{
	Use:   "cas-proxy",
	Short: "A simple reverse proxy with CAS support.",
	Long:  `A simple reverse proxy with CAS support.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Do Stuff Here
	},
}
