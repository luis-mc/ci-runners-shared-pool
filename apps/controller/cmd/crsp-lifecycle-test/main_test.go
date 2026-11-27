package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPair(t *testing.T) {
	if err := pair(); err != nil {
		t.Fatal(err)
	}
}

func TestSelfTestPassesWhenConsoleAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	if err := selfTest(srv.URL, 5*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestSelfTestFailsWhenConsoleIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	if err := selfTest(srv.URL, 100*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("want a failure after the limit")
	}
}

func TestPrintState(t *testing.T) {
	if err := printState(); err != nil {
		t.Fatal(err)
	}
}
