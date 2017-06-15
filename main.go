package main

import (
	"fmt"
	"os"

	"github.com/cyverse-de/cas-proxy/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}
