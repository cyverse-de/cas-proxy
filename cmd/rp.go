package cmd

import (
	_ "log"
	"net/http"

	"github.com/cyverse-de/cas-proxy/proxymux"
	"github.com/spf13/cobra"
)

// rpCmd represents the rp command
var rpCmd = &cobra.Command{
	Use:   "reverse-proxy",
	Short: "A simple reverse proxy.",
	Long:  `A simple reverse proxy.`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error

		// sslKey and sslCert are managed in RootCmd
		useSSL := false
		if sslCert != "" || sslKey != "" {
			if sslCert == "" {
				log.Fatal("--ssl-cert is required with --ssl-key.")
			}

			if sslKey == "" {
				log.Fatal("--ssl-key is required with --ssl-cert.")
			}
			useSSL = true
		}

		// Create a proxymux
		proxy := proxymux.New()

		// Listen for api updates for the proxymux.
		go func() {
			apiServer := &http.Server{
				Handler: proxy.APIHandler(),
				Addr:    "0.0.0.0:8082",
			}

			if useSSL {
				err = apiServer.ListenAndServeTLS(sslCert, sslKey)
			} else {
				err = apiServer.ListenAndServe()
			}

			log.Fatal(err)
		}()

		// Listen for requests to go through the proxymux
		server := &http.Server{
			Handler: proxy,
			Addr:    listenAddr, // listenAddr is managed in the RootCmd
		}

		if useSSL {
			err = server.ListenAndServeTLS(sslCert, sslKey)
		} else {
			err = server.ListenAndServe()
		}

		log.Fatal(err)
	},
}

func init() {
	RootCmd.AddCommand(rpCmd)
}
