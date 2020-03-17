# zwibserve [![GoDoc](https://godoc.org/github.com/smhanov/zwibserve?status.svg)](https://godoc.org/github.com/smhanov/zwibserve)

Package zwibserve is an example collaboration server for zwibbler.com

This is a go package. To run it, you will create a main program like this:

Run `go get github.com/smhanov/zwibserve`

Create a main.go file:

```go
package main

import (
	"log"
	"net/http"

	"github.com/smhanov/zwibserve"
)

func main() {
	http.Handle("/socket", zwibserve.NewHandler(zwibserve.NewSQLITEDB("zwibbler.db")))
	log.Printf("Running...")
	http.ListenAndServe(":8004", http.DefaultServeMux)
}

```

run `go build` and the server will be compiled as `main`.


The protocol is described in this Google Doc: https://docs.google.com/document/d/1X3_fzFqPUzTbPqF2GrYlSveWuv_L-xX7Cc69j13i6PY/edit?usp=sharing

Architecturally, It uses gorilla websockets and follows closely the hub and client example
given at https://github.com/gorilla/websocket/tree/master/examples/chat

A hub goroutine is responsible for keeping a collection of document IDs. Each
document ID has a list of clients connected to it.

Clients each run one goroutine for receiving messages, and one goroutine for sending.

The DocumentDB, which you can implement, actually stores the contents of the documents.
Before appending to a document, it must atomically check if the length the client has given matches
the actual length of the document.
