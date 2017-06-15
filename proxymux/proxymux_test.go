package proxymux

import (
	"net/http"
	"testing"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Error("p was nil")
	}
}

func TestAdd(t *testing.T) {
	p := New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	err := p.Add("/test", h)
	if err != nil {
		t.Error(err)
	}
	err = p.Add("/test", h)
	if err == nil {
		t.Error("error should have been returned on second insert")
	}
}

func TestGet(t *testing.T) {
	p := New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	err := p.Add("/test", h)
	if err != nil {
		t.Error(err)
	}
	r, err := p.Get("/test")
	if err != nil {
		t.Error(err)
	}
	if r == nil {
		t.Error("r was nil")
	}
}

func TestRemove(t *testing.T) {
	p := New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	err := p.Add("/test", h)
	if err != nil {
		t.Error(err)
	}
	r, err := p.Get("/test")
	if err != nil {
		t.Error(err)
	}
	if r == nil {
		t.Error("r was nil")
	}

	p.Remove("/test")

	r2, err := p.Get("/test")
	if err != nil {
		t.Error(err)
	}
	if r2 != nil {
		t.Error("r2 was not nil")
	}
}
