package cmd

import (
	"github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
)

var log = logrus.WithFields(logrus.Fields{
	"service": "cas-proxy",
	"art-id":  "cas-proxy",
	"group":   "org.cyverse",
})

var (
	sslCert    string
	sslKey     string
	listenAddr string
)

var RootCmd = &cobra.Command{
	Use:   "cas-proxy",
	Short: "A simple reverse proxy with CAS support.",
	Long:  `A simple reverse proxy with CAS support.`,
}

func init() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
	RootCmd.PersistentFlags().StringVar(&listenAddr, "listen-addr", "0.0.0.0:8080", "The listen port number.")
	RootCmd.PersistentFlags().StringVar(&sslCert, "ssl-cert", "", "Path to the SSL .crt file.")
	RootCmd.PersistentFlags().StringVar(&sslKey, "ssl-key", "", "Path to the SSL .key file.")
}
