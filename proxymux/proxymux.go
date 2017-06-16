package proxymux

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/cyverse-de/cas-proxy/reverseproxy"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	trie "github.com/tchap/go-patricia/patricia"
)

// proxytrie is a wrapper around github.com/tchap/go-patricia/patricia that adds
// basic concurrency support through the use of sync.RWMutex.
type proxytrie struct {
	*trie.Trie
	lock *sync.RWMutex
}

// Delete is a synchronized call to the underlying trie's Delete().
func (t *proxytrie) Delete(key trie.Prefix) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.Delete(key)
}

// DeleteSubtree is synchronized call to the underlying trie's DeleteSubtree().
func (t *proxytrie) DeleteSubtree(prefix trie.Prefix) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.DeleteSubtree(prefix)
}

// Get is synchronized call to the underlying trie's Get().
func (t *proxytrie) Get(key trie.Prefix) trie.Item {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.Trie.Get(key)
}

// Insert is synchronized call to the underlying trie's Insert().
func (t *proxytrie) Insert(key trie.Prefix, item trie.Item) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.Insert(key, item)
}

// Item is synchronized call to the underlying trie's Item().
func (t *proxytrie) Item() trie.Item {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.Trie.Item()
}

// Match is synchronized call to the underlying trie's Match().
func (t *proxytrie) Match(prefix trie.Prefix) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.Trie.Match(prefix)
}

// MatchSubtree is synchronized call to the underlying trie's MatchSubtree().
func (t *proxytrie) MatchSubtree(key trie.Prefix) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.Trie.MatchSubtree(key)
}

// Set is synchronized call to the underlying trie's Set().
func (t *proxytrie) Set(key trie.Prefix, item trie.Item) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.Trie.Set(key, item)
}

// Visit is synchronized call to the underlying trie's Visit().
func (t *proxytrie) Visit(visitor trie.VisitorFunc) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.Visit(visitor)
}

// VisitPrefixes is synchronized call to the underlying trie's VisitPrefixes().
func (t *proxytrie) VisitPrefixes(key trie.Prefix, visitor trie.VisitorFunc) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.VisitPrefixes(key, visitor)
}

// VisitSubtree is synchronized call to the underlying trie's VisitSubtree().
func (t *proxytrie) VisitSubtree(prefix trie.Prefix, visitor trie.VisitorFunc) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.Trie.VisitSubtree(prefix, visitor)
}

// ProxyMux implements the http.Handler interface. The path for each request
// is matched to a http.Handler by examining the internal trie data structure.
// Only one handler per path is supported.
type ProxyMux struct {
	t *proxytrie
}

// New returnes a new *ProxyMux.
func New() *ProxyMux {
	return &ProxyMux{
		t: &proxytrie{
			trie.NewTrie(),
			&sync.RWMutex{},
		},
	}
}

// Add registers a new handler for the given path. Returns an error if Add is
// called on the same path two or more times.
func (p *ProxyMux) Add(path string, handler http.Handler) error {
	inserted := p.t.Insert(trie.Prefix(path), trie.Item(handler))
	if !inserted {
		return fmt.Errorf("failed to insert item for path %s", path)
	}
	return nil
}

// Remove deregisters the handler for the given path. Does not return an error
// if Remove is called multiple times.
func (p *ProxyMux) Remove(path string) {
	p.t.Delete(trie.Prefix(path))
}

// Get returns the http.Handler associated with the path, or nil if no
// http.Handler was found. If the item stored is somehow not an http.Handler,
// then an error is returned.
func (p *ProxyMux) Get(path string) (http.Handler, error) {
	var (
		val http.Handler
		ok  bool
	)
	if val, ok = p.t.Get(trie.Prefix(path)).(http.Handler); !ok {
		if val == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("value stored at %s was not a http.Handler", path)
	}
	return val, nil
}

// ServeHTTP looks up the handler for the given request in the mux and passes
// control along to it. Implements the http.Handler interface.
func (p *ProxyMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqpath := r.URL.Path
	handler, err := p.Get(reqpath)
	if err != nil {
		msg := errors.Wrapf(err, "error looking up handler for path %s", reqpath)
		http.Error(w, msg.Error(), http.StatusInternalServerError)
		return
	}
	if handler == nil {
		http.Error(w, "handler was nil", http.StatusInternalServerError)
		return
	}
	handler.ServeHTTP(w, r)
}

// APIRequest is the data that is needed for the API calls.
type APIRequest struct {
	Path    string `json:"path"`
	Backend string `json:"backend,omitempty"`
}

// APIRegisterHandler is an http.Handler for registering new routes.
func (p *ProxyMux) APIRegisterHandler(w http.ResponseWriter, r *http.Request) {
	// Read request body
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read the body of the request", http.StatusInternalServerError)
		return
	}

	// Parse body
	registration := &APIRequest{}
	err = json.Unmarshal(body, registration)
	if err != nil {
		http.Error(w, errors.Wrap(err, "failed to unmarshal the request body as JSON").Error(), http.StatusBadRequest)
		return
	}

	if registration.Path == "" {
		http.Error(w, "The path field was empty", http.StatusBadRequest)
		return
	}

	if registration.Backend == "" {
		http.Error(w, "The backend field was empty", http.StatusBadRequest)
		return
	}

	// Create the proxy
	proxy, err := reverseproxy.NewSimpleProxy(registration.Backend).Proxy()
	if err != nil {
		http.Error(w, errors.Wrapf(err, "failed to create proxy for %s", registration.Path).Error(), http.StatusInternalServerError)
		return
	}

	// Register the path
	err = p.Add(registration.Path, proxy)
	if err != nil {
		http.Error(
			w,
			errors.Wrapf(err, "failed to add route path: %s, backend: %s", registration.Path, registration.Backend).Error(),
			http.StatusBadRequest,
		)
		return
	}
}

// APIUnregisterHandler is an http.Handler for unregistering routes.
func (p *ProxyMux) APIUnregisterHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read the body of the request", http.StatusInternalServerError)
		return
	}

	// Parse body
	registration := &APIRequest{}
	err = json.Unmarshal(body, registration)
	if err != nil {
		http.Error(w, errors.Wrap(err, "failed to unmarshal the request body as JSON").Error(), http.StatusBadRequest)
		return
	}

	if registration.Path == "" {
		http.Error(w, "The path field was empty", http.StatusBadRequest)
		return
	}

	p.Remove(registration.Path)
}

// // APILookupHandler is an http.Handler for looking up routes.
// func (p *ProxyMux) APILookupHandler(w http.ResponseWriter, r *http.Request) {
// 	body, err := ioutil.ReadAll(r.Body)
// 	defer r.Body.Close()
// 	if err != nil {
// 		http.Error(w, "failed to read the body of the request", http.StatusInternalServerError)
// 		return
// 	}
//
// 	// Parse body
// 	registration := &APIRequest{}
// 	err = json.Unmarshal(body, registration)
// 	if err != nil {
// 		http.Error(w, errors.Wrap(err, "failed to unmarshal the request body as JSON").Error(), http.StatusBadRequest)
// 		return
// 	}
//
// 	if registration.Path == "" {
// 		http.Error(w, "The path field was empty", http.StatusBadRequest)
// 		return
// 	}
//
// 	h, err := p.Get(registration.Path)
// 	if err != nil {
// 		http.Error(w, errors.Wrapf(err, "error looking up handler for path %s", registration.Path).Error(), http.StatusInternalServerError)
// 	}
//   if h == n
// }

// APIListHandler is an http.Handler for listing all configured routes.
func (p *ProxyMux) APIListHandler(w http.ResponseWriter, r *http.Request) {

}

// APIHandler returns an http.Handler that can mux requests to the api functions
// for managing the ProxyMux.
func (p *ProxyMux) APIHandler() http.Handler {
	r := mux.NewRouter()
	r.Path("/api/register").Methods("POST").Handler(http.HandlerFunc(p.APIRegisterHandler))
	r.Path("/api/unregister").Methods("POST").Handler(http.HandlerFunc(p.APIUnregisterHandler))
	//r.Path("/api/lookup").Methods("POST").Handler(http.HandlerFunc(p.APILookupHandler))
	//r.Path("/api/list").Methods("GET").Handler(http.HandlerFunc(p.APIListHandler))
	return r
}