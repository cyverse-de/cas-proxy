package reverseproxy

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/alexedwards/scs/session"
	"github.com/gorilla/mux"
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
}

// NewCASProxy returns a newly instantiated *CASProxy.
func NewCASProxy(casBase, casValidate, frontendURL, backendURL, wsbackendURL string) *CASProxy {
	return &CASProxy{
		casBase:      casBase,
		casValidate:  casValidate,
		frontendURL:  frontendURL,
		backendURL:   backendURL,
		wsbackendURL: wsbackendURL,
	}
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

	// Store a session, hopefully to short-circuit the CAS redirect dance in later
	// requests. The max age of the cookie should be less than the lifetime of
	// the CAS ticket, which is around 10+ hours. This means that we'll be hitting
	// the CAS server fairly often. Adjust the max age to rate limit requests to
	// CAS.
	err = session.PutInt(r, sessionKey, 1)
	if err != nil {
		err = errors.Wrap(err, "error setting setting value")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, svcURL.String(), http.StatusFound)
}

// Session implements the mux.Matcher interface so that requests can be routed
// based on cookie existence.
func (c *CASProxy) Session(r *http.Request, m *mux.RouteMatch) bool {
	msg, err := session.GetInt(r, sessionKey)
	if err != nil {
		return true
	}

	if msg != 1 {
		log.Infof("session value was %d instead of 1", msg)
		return true
	}

	// This should reset the expiration time.
	err = session.PutInt(r, sessionKey, 1)
	if err != nil {
		log.Error(err)
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
		if c.isWebsocket(r) {
			ws.ServeHTTP(w, r)
			return
		}
		rp.ServeHTTP(w, r)
	}), nil
}
