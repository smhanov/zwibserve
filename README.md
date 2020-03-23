# zwibserve [![GoDoc](https://godoc.org/github.com/smhanov/zwibserve?status.svg)](https://godoc.org/github.com/smhanov/zwibserve)

Package zwibserve is an example collaboration server for zwibbler.com. It lets users work on the same document together. The server portion is responsible only for storing the changes and broadcasting them to all connected clients. 

The collaboration works similar to source control. When I try to submit my changes, and you have submitted yours first, then my changes are rejected by the server. I must then modify my changes so they no longer conflict with yours, and try to submit them again.

The protocol is described in [Zwibbler Collaboration Server Protocol V2](https://docs.google.com/document/d/1X3_fzFqPUzTbPqF2GrYlSveWuv_L-xX7Cc69j13i6PY/edit?usp=sharing)

[Test your server online](https://zwibbler.com/collaboration/testing.html).

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
	http.ListenAndServe(":3000", http.DefaultServeMux)
}

```

run `go build` and the server will be compiled as `main`. It will run on port 3000 by default but you can change this in the main() function above.

Architecturally, It uses gorilla websockets and follows closely the [hub and client example](https://github.com/gorilla/websocket/tree/master/examples/chat)

A hub goroutine is responsible for keeping a collection of document IDs. Each
document ID has a list of clients connected to it.

Clients each run one goroutine for receiving messages, and one goroutine for sending.

The DocumentDB, which you can implement, actually stores the contents of the documents.
Before appending to a document, it must atomically check if the length the client has given matches
the actual length of the document.
