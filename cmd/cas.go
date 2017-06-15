package cmd

import (
	"net/http"
	"net/url"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/alexedwards/scs/engine/memstore"
	"github.com/alexedwards/scs/session"
	"github.com/cyverse-de/cas-proxy/reverseproxy"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
)

var log = logrus.WithFields(logrus.Fields{
	"service": "cas-proxy",
	"art-id":  "cas-proxy",
	"group":   "org.cyverse",
})

var (
	casBase      string
	casValidate  string
	frontendURL  string
	sslCert      string
	sslKey       string
	wsbackendURL string
	backendURL   string
	listenAddr   string
	maxAge       int
)

// casCmd represents the cas command
var casCmd = &cobra.Command{
	Use:   "cas",
	Short: "A CAS-enabled proxy that maps a single frontend to a single backend.",
	Long:  `A CAS-enabled proxy that maps a single frontend to a single backend.`,
	Run: func(cmd *cobra.Command, args []string) {
		if casBase == "" {
			log.Fatal("--cas-base-url must be set.")
		}

		if frontendURL == "" {
			log.Fatal("--frontend-url must be set.")
		}

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

		if wsbackendURL == "" {
			w, err := url.Parse(backendURL)
			if err != nil {
				log.Fatal(err)
			}
			w.Scheme = "ws"
			wsbackendURL = w.String()
		}

		log.Infof("backend URL is %s", backendURL)
		log.Infof("websocket backend URL is %s", wsbackendURL)
		log.Infof("frontend URL is %s", frontendURL)
		log.Infof("listen address is %s", listenAddr)
		log.Infof("CAS base URL is %s", casBase)
		log.Infof("CAS ticket validator endpoint is %s", casValidate)

		p := reverseproxy.NewCASProxy(casBase, casValidate, frontendURL, backendURL, wsbackendURL)

		proxy, err := p.Proxy()
		if err != nil {
			log.Fatal(err)
		}

		sessionEngine := memstore.New(30 * time.Second)

		var sessionManager func(h http.Handler) http.Handler
		if maxAge > 0 {
			d := time.Duration(maxAge) * time.Second
			sessionManager = session.Manage(sessionEngine, session.IdleTimeout(d))
		} else {
			sessionManager = session.Manage(sessionEngine)
		}

		r := mux.NewRouter()

		// If the query contains a ticket in the query params, then it needs to be
		// validated.
		r.PathPrefix("/").Queries("ticket", "").Handler(http.HandlerFunc(p.ValidateTicket))
		r.PathPrefix("/").MatcherFunc(p.Session).Handler(http.HandlerFunc(p.RedirectToCAS))
		r.PathPrefix("/").Handler(proxy)

		server := &http.Server{
			Handler: sessionManager(r),
			Addr:    listenAddr,
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
	logrus.SetFormatter(&logrus.JSONFormatter{})
	RootCmd.AddCommand(casCmd)
	casCmd.PersistentFlags().StringVar(&backendURL, "backend-url", "http://localhost:60000", "The hostname and port to proxy requests to.")
	casCmd.PersistentFlags().StringVar(&wsbackendURL, "ws-backend-url", "", "The backend URL for the handling websocket requests. Defaults to the value of --backend-url with a scheme of ws://")
	casCmd.PersistentFlags().StringVar(&frontendURL, "frontend-url", "", "The URL for the frontend server. Might be different from the hostname and listen port.")
	casCmd.PersistentFlags().StringVar(&listenAddr, "listen-addr", "0.0.0.0:8080", "The listen port number.")
	casCmd.PersistentFlags().StringVar(&casBase, "cas-base-url", "http://localhost:60000", "The hostname and port to proxy to.")
	casCmd.PersistentFlags().StringVar(&casValidate, "cas-validate", "validate", "The CAS URL endpoint for validating tickets.")
	casCmd.PersistentFlags().IntVar(&maxAge, "max-age", 0, "The idle timeout for session, in seconds.")
	casCmd.PersistentFlags().StringVar(&sslCert, "ssl-cert", "", "Path to the SSL .crt file.")
	casCmd.PersistentFlags().StringVar(&sslKey, "ssl-key", "", "Path to the SSL .key file.")
}
