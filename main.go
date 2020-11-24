package main

import (
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/anton-dessiatov/exifserve/handler"
)

const Port = 8080

func main() {
	err := http.ListenAndServe(net.JoinHostPort("", strconv.Itoa(Port)),
		http.HandlerFunc(handler.Handler))
	if err != nil {
		log.Fatalf("http.ListenAndServe: %v", err)
	}
}
