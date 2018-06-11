package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/pkg/errors"
	"github.com/yhat/wsutil"
)

var log = logrus.WithFields(logrus.Fields{
	"service": "cas-proxy",
	"art-id":  "cas-proxy",
	"group":   "org.cyverse",
})

func init() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
}

const sessionName = "proxy-session"
const sessionKey = "proxy-session-key"
const sessionAccess = "proxy-session-last-access"

// CASProxy contains the application logic that handles authentication, session
// validations, ticket validation, and request proxying.
type CASProxy struct {
	casBase      string // base URL for the CAS server
	casValidate  string // The path to the validation endpoint on the CAS server.
	frontendURL  string // The URL placed into service query param for CAS.
	backendURL   string // The backend URL to forward to.
	wsbackendURL string // The websocket URL to forward requests to.
	resourceType string // The resource type for analysis.
	resourceName string // The UUID of the analysis.
	permsURL     string // The service URL for the permissions service.
	subjectType  string // The subject type for a user.
	sessionStore *sessions.CookieStore
}

// NewCASProxy returns a newly instantiated *CASProxy.
func NewCASProxy(casBase, casValidate, frontendURL, backendURL, wsbackendURL string, cs *sessions.CookieStore) *CASProxy {
	return &CASProxy{
		casBase:      casBase,
		casValidate:  casValidate,
		frontendURL:  frontendURL,
		backendURL:   backendURL,
		wsbackendURL: wsbackendURL,
		sessionStore: cs,
	}
}

// Analysis contains the ID for the Analysis, which gets used as the resource
// name when checking permissions.
type Analysis struct {
	ID string `json:"id"` // Literally all we care about here.
}

// Analyses is a list of analyses returned by the apps service.
type Analyses struct {
	Analyses []Analysis `json:"analyses"`
}

func getResourceName(appsURL, appsUser, externalID string) (string, error) {
	reqURL, err := url.Parse(appsURL)
	if err != nil {
		return "", err
	}
	reqURL.Path = filepath.Join(reqURL.Path, "admin/analyses/by-external-id", externalID)

	v := url.Values{}
	v.Set("user", appsUser)
	reqURL.RawQuery = v.Encode()

	resp, err := http.Get(reqURL.String())
	defer func() {
		if resp != nil {
			if resp.Body != nil {
				resp.Body.Close()
			}
		}
	}()
	if err != nil {
		return "", err
	}

	analyses := &Analyses{}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(b, analyses); err != nil {
		return "", err
	}
	if len(analyses.Analyses) < 1 {
		return "", errors.New("no analyses found")
	}
	return analyses.Analyses[0].ID, nil
}

// Resource is an item that can have permissions attached to it in the
// permissions service.
type Resource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"resource_type"`
}

// Subject is an item that accesses resources contained in the permissions
// service.
type Subject struct {
	ID        string `json:"id"`
	SubjectID string `json:"subject_id"`
	SourceID  string `json:"subject_source_id"`
	Type      string `json:"subject_type"`
}

// Permission is an entry from the permissions service that tells what access
// a subject has to a resource.
type Permission struct {
	ID       string   `json:"id"`
	Level    string   `json:"permission_level"`
	Resource Resource `json:"resource"`
	Subject  Subject  `json:"subject"`
}

// PermissionList contains a list of permission returned by the permissions
// service.
type PermissionList struct {
	Permissions []Permission `json:"permissions"`
}

// IsAllowed will return true if the user is allowed to access the running app
// and false if they're not. An error might be returned as well. Access should
// be denied if an error is returned, even if the boolean return value is true.
func (c *CASProxy) IsAllowed(user string) (bool, error) {
	requrl, err := url.Parse(c.permsURL)
	if err != nil {
		return false, err
	}
	requrl.Path = filepath.Join(requrl.Path, "permissions/subjects", c.subjectType, user, c.resourceType, c.resourceName)
	resp, err := http.Get(requrl.String())
	defer func() {
		if resp != nil {
			if resp.Body != nil {
				resp.Body.Close()
			}
		}
	}()
	if err != nil {
		return false, err
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	l := &PermissionList{
		Permissions: []Permission{},
	}
	if err = json.Unmarshal(b, l); err != nil {
		return false, err
	}
	if len(l.Permissions) > 0 {
		if l.Permissions[0].Level != "" {
			return true, nil
		}
	}
	return false, nil
}

// ValidateTicket will validate a CAS ticket against the configured CAS server.
func (c *CASProxy) ValidateTicket(w http.ResponseWriter, r *http.Request) {
	casURL, err := url.Parse(c.casBase)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse CAS base URL %s", c.casBase)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Make sure the path in the CAS params is the same as the one that was
	// requested.
	svcURL, err := url.Parse(c.frontendURL)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse the frontend URL %s", c.frontendURL)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure that the service path and the query params are set to the incoming
	// request's values for those fields.
	svcURL.Path = r.URL.Path
	sq := r.URL.Query()
	sq.Del("ticket") // Remove the ticket from the service URL. Redirection loops occur otherwise.
	svcURL.RawQuery = sq.Encode()

	// The request URL for CAS ticket validation needs to have the service and
	// ticket in it.
	casURL.Path = path.Join(casURL.Path, c.casValidate)
	q := casURL.Query()
	q.Add("service", svcURL.String())
	q.Add("ticket", r.URL.Query().Get("ticket"))
	casURL.RawQuery = q.Encode()

	// Actually validate the ticket.
	resp, err := http.Get(casURL.String())
	if err != nil {
		err = errors.Wrap(err, "ticket validation error")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// If this happens then something went wrong on the CAS side of things. Doesn't
	// mean the ticket is invalid, just that the CAS server is in a state where
	// we can't trust the response.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err = errors.Wrapf(err, "ticket validation status code was %d", resp.StatusCode)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = errors.Wrap(err, "error reading body of CAS response")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// This is where the actual ticket validation happens. If the CAS server
	// returns 'no\n\n' in the body, then the validation was not successful. The
	// HTTP status code will be in the 200 range regardless of the validation
	// status.
	if bytes.Equal(b, []byte("no\n\n")) {
		err = fmt.Errorf("ticket validation response body was %s", b)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	fields := bytes.Fields(b)
	if len(fields) < 2 {
		err = errors.New("not enough fields in ticket validation response body")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	username := string(fields[1])

	// Store a session, hopefully to short-circuit the CAS redirect dance in later
	// requests. The max age of the cookie should be less than the lifetime of
	// the CAS ticket, which is around 10+ hours. This means that we'll be hitting
	// the CAS server fairly often. Adjust the max age to rate limit requests to
	// CAS.
	var s *sessions.Session
	s, err = c.sessionStore.Get(r, sessionName)
	if err != nil {
		err = errors.Wrap(err, "error getting session")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Values[sessionKey] = username
	s.Save(r, w)

	http.Redirect(w, r, svcURL.String(), http.StatusFound)
}

// ResetSessionExpiration should reset the session expiration time.
func (c *CASProxy) ResetSessionExpiration(w http.ResponseWriter, r *http.Request) error {
	session, err := c.sessionStore.Get(r, sessionName)
	if err != nil {
		return err
	}

	msg := session.Values[sessionKey].(string)
	session.Values[sessionKey] = msg
	session.Save(r, w)
	return nil
}

// Session implements the mux.Matcher interface so that requests can be routed
// based on cookie existence.
func (c *CASProxy) Session(r *http.Request, m *mux.RouteMatch) bool {
	session, err := c.sessionStore.Get(r, sessionName)
	if err != nil {
		return true
	}

	msg := session.Values[sessionKey].(string)
	if msg == "" {
		log.Infof("session value was empty instead of a username")
		return true
	}

	return false
}

// RedirectToCAS redirects the request to CAS, setting the service query
// parameter to the value in frontendURL.
func (c *CASProxy) RedirectToCAS(w http.ResponseWriter, r *http.Request) {
	fmt.Println("redirect to cas")
	casURL, err := url.Parse(c.casBase)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse CAS base URL %s", c.casBase)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Make sure the path in the CAS params is the same as the one that was
	// requested.
	svcURL, err := url.Parse(c.frontendURL)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse the frontend URL %s", c.frontendURL)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Make sure the serivce path and the query params are set to the incoming
	// requests values for those fields.
	svcURL.Path = r.URL.Path
	svcURL.RawQuery = r.URL.RawQuery

	//set the service query param in the casURL.
	q := casURL.Query()
	q.Add("service", svcURL.String())
	casURL.RawQuery = q.Encode()
	casURL.Path = path.Join(casURL.Path, "login")

	// perform the redirect
	http.Redirect(w, r, casURL.String(), http.StatusPermanentRedirect)
}

// ReverseProxy returns a proxy that forwards requests to the configured
// backend URL. It can act as a http.Handler.
func (c *CASProxy) ReverseProxy() (*httputil.ReverseProxy, error) {
	backend, err := url.Parse(c.backendURL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse %s", c.backendURL)
	}
	return httputil.NewSingleHostReverseProxy(backend), nil
}

// WSReverseProxy returns a proxy that forwards websocket request to the
// configured backend URL. It can act as a http.Handler.
func (c *CASProxy) WSReverseProxy() (*wsutil.ReverseProxy, error) {
	w, err := url.Parse(c.wsbackendURL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse the websocket backend URL %s", c.wsbackendURL)
	}
	return wsutil.NewSingleHostReverseProxy(w), nil
}

// isWebsocket returns true if the connection is a websocket request. Adapted
// from the code at https://groups.google.com/d/msg/golang-nuts/KBx9pDlvFOc/0tR1gBRfFVMJ.
func (c *CASProxy) isWebsocket(r *http.Request) bool {
	connectionHeader := ""
	allHeaders := r.Header["Connection"]
	if len(allHeaders) > 0 {
		connectionHeader = allHeaders[0]
	}

	upgrade := false
	if strings.Contains(strings.ToLower(connectionHeader), "upgrade") {
		if len(r.Header["Upgrade"]) > 0 {
			upgrade = (strings.ToLower(r.Header["Upgrade"][0]) == "websocket")
		}
	}
	return upgrade
}

// Proxy returns a handler that can support both websockets and http requests.
func (c *CASProxy) Proxy() (http.Handler, error) {
	ws, err := c.WSReverseProxy()
	if err != nil {
		return nil, err
	}

	rp, err := c.ReverseProxy()
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//Get the username from the cookie
		session, err := c.sessionStore.Get(r, sessionName)
		if err != nil {
			err = errors.Wrap(err, "failed to get session")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		username := session.Values[sessionKey].(string)
		if username == "" {
			err = errors.Wrap(err, "username was empty")
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// Check to make sure the user can access the resource.
		allowed, err := c.IsAllowed(username)
		if !allowed || err != nil {
			if err != nil {
				err = errors.Wrap(err, "access denied")
			} else {
				err = errors.New("access denied")
			}
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		log.Printf("%+v\n", r.Header)

		if err = c.ResetSessionExpiration(w, r); err != nil {
			err = errors.Wrap(err, "error resetting session expiration")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if c.isWebsocket(r) {
			ws.ServeHTTP(w, r)
			return
		}
		rp.ServeHTTP(w, r)
	}), nil
}

func main() {
	var (
		backendURL   = flag.String("backend-url", "http://localhost:60000", "The hostname and port to proxy requests to.")
		wsbackendURL = flag.String("ws-backend-url", "", "The backend URL for the handling websocket requests. Defaults to the value of --backend-url with a scheme of ws://")
		frontendURL  = flag.String("frontend-url", "", "The URL for the frontend server. Might be different from the hostname and listen port.")
		listenAddr   = flag.String("listen-addr", "0.0.0.0:8080", "The listen port number.")
		casBase      = flag.String("cas-base-url", "", "The base URL to the CAS host.")
		casValidate  = flag.String("cas-validate", "validate", "The CAS URL endpoint for validating tickets.")
		maxAge       = flag.Int("max-age", 0, "The idle timeout for session, in seconds.")
		sslCert      = flag.String("ssl-cert", "", "Path to the SSL .crt file.")
		sslKey       = flag.String("ssl-key", "", "Path to the SSL .key file.")
		resourceType = flag.String("resource-type", "analysis", "The resource type that gets passed to the permissions service.")
		permsURL     = flag.String("permissions-url", "", "The URL for the permissions service.")
		subjectType  = flag.String("subject-type", "user", "The subject type to pass to the permissions service.")
		appsUser     = flag.String("apps-user", "", "Username to use when calling the apps api.")
		appsURL      = flag.String("apps-url", "", "The URL for the apps service.")
		externalID   = flag.String("external-id", "", "The external ID to pass to the apps service when looking up the analysis ID.")
	)

	flag.Parse()

	if *casBase == "" {
		log.Fatal("--cas-base-url must be set.")
	}

	if *frontendURL == "" {
		log.Fatal("--frontend-url must be set.")
	}

	useSSL := false
	if *sslCert != "" || *sslKey != "" {
		if *sslCert == "" {
			log.Fatal("--ssl-cert is required with --ssl-key.")
		}

		if *sslKey == "" {
			log.Fatal("--ssl-key is required with --ssl-cert.")
		}
		useSSL = true
	}

	if *wsbackendURL == "" {
		w, err := url.Parse(*backendURL)
		if err != nil {
			log.Fatal(err)
		}
		w.Scheme = "ws"
		*wsbackendURL = w.String()
	}

	if *appsUser == "" {
		log.Fatal("--apps-user must be set.")
	}

	if *appsURL == "" {
		log.Fatal("--apps-url must be set.")
	}

	if *permsURL == "" {
		log.Fatal("--permissions-url must be set.")
	}

	if *externalID == "" {
		log.Fatal("--external-id must be set.")
	}

	log.Infof("backend URL is %s", *backendURL)
	log.Infof("websocket backend URL is %s", *wsbackendURL)
	log.Infof("frontend URL is %s", *frontendURL)
	log.Infof("listen address is %s", *listenAddr)
	log.Infof("CAS base URL is %s", *casBase)
	log.Infof("CAS ticket validator endpoint is %s", *casValidate)

	resourceName, err := getResourceName(*appsURL, *appsUser, *externalID)
	if err != nil {
		log.Fatal(err)
	}

	p := &CASProxy{
		casBase:      *casBase,
		casValidate:  *casValidate,
		frontendURL:  *frontendURL,
		backendURL:   *backendURL,
		wsbackendURL: *wsbackendURL,
		resourceType: *resourceType,
		subjectType:  *subjectType,
		resourceName: resourceName,
		permsURL:     *permsURL,
	}

	proxy, err := p.Proxy()
	if err != nil {
		log.Fatal(err)
	}

	sessionStore := sessions.NewCookieStore([]byte("auth-key"))
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   *maxAge,
		HttpOnly: true,
	}

	r := mux.NewRouter()

	// If the query contains a ticket in the query params, then it needs to be
	// validated.
	r.PathPrefix("/").Queries("ticket", "").Handler(http.HandlerFunc(p.ValidateTicket))
	r.PathPrefix("/").MatcherFunc(p.Session).Handler(http.HandlerFunc(p.RedirectToCAS))
	r.PathPrefix("/").Handler(proxy)

	server := &http.Server{
		Handler: r,
		Addr:    *listenAddr,
	}
	if useSSL {
		err = server.ListenAndServeTLS(*sslCert, *sslKey)
	} else {
		err = server.ListenAndServe()
	}
	log.Fatal(err)

}
