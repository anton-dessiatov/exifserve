package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
)

// Handler is HTTP handler function for the application
func Handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/tags":
		GetTags(w, r)
		return
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
}

// Get tags is HTTP handler function for the /tags GET method
func GetTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	et := Listx(r.Context())
	defer et.Close()
	err := et.Start()
	if err != nil {
		log.Printf("et.Start: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)

	err = et.StreamTags(w)
	if err != nil {
		// It's too late to return HTTP 500, so the best we could do is log and return
		// But first let's check if we've been canceled. If that was the case, no
		// need to spam logs with errors (which are guaranteed to happen because of the
		// process being killed and writer forcefully closed)
		if !errors.Is(r.Context().Err(), context.Canceled) {
			log.Printf("et.StreamTags: %v", err)
		}
		return
	}
}
