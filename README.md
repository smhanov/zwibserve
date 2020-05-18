# zwibserve [![GoDoc](https://godoc.org/github.com/smhanov/zwibserve?status.svg)](https://godoc.org/github.com/smhanov/zwibserve)

Package zwibserve is an example collaboration server for zwibbler.com. It lets users work on the same document together. The server portion is responsible only for storing the changes and broadcasting them to all connected clients. 

The collaboration works similar to source control. When I try to submit my changes, and you have submitted yours first, then my changes are rejected by the server. I must then modify my changes so they no longer conflict with yours, and try to submit them again.

The protocol is described in [Zwibbler Collaboration Server Protocol V2](https://docs.google.com/document/d/1X3_fzFqPUzTbPqF2GrYlSveWuv_L-xX7Cc69j13i6PY/edit?usp=sharing)

[Test your server online](https://zwibbler.com/collaboration/testing.html).

This is a go package. To run it, you will create a main program like this:

### Step 1: Get the package
Run `go get github.com/smhanov/zwibserve` to get `go` to install the package in its cache.

### Step 2: Create the main.go file
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

### Step 3: Build and run
Run `go build` and the server will be compiled as `main`. It will run on port 3000 by default but you can change this in the main() function above.

## Architecture
Architecturally, It uses gorilla websockets and follows closely the [hub and client example](https://github.com/gorilla/websocket/tree/master/examples/chat)

A hub goroutine is responsible for keeping a collection of document IDs. Each
document ID has a list of clients connected to it.

Clients each run one goroutine for receiving messages, and one goroutine for sending.

The DocumentDB, which you can implement, actually stores the contents of the documents.
Before appending to a document, it must atomically check if the length the client has given matches
the actual length of the document.

The server is meant to only store documents during the time that multiple people are working on them. You should have a more permanent solution to store them for saving / opening.

## Source tree method

Using the project as a module is the recommended way. You should not need to make modifications to the zwibserve package.  However, if you want to make modifications to zwibserve in your project, you can directly include the files in your project.

### Step 1: Check out the files

```bash
mkdir myproject
cd myproject
git clone git@github.com:smhanov/zwibserve.git
```

Now you have your project folder and zwibserve/ inside of it.

### Step 2: Create go.mod file
Now, create a go.mod file which tells go where to find the zwibserve package.
```
module example.com/server

go 1.14

require (
	zwibserve v0.0.0
)

replace zwibserve v0.0.0 => ./zwibserve
```

### Step 3: Update main.go to refer to the local package
Create the main.go file from above, but remove github.com from the package import.

```go
package main

import (
	"log"
	"net/http"

	"zwibserve" // <-- Was github.com/smhanov/zwibserve
)

func main() {
	http.Handle("/socket", zwibserve.NewHandler(zwibserve.NewSQLITEDB("zwibbler.db")))
	log.Printf("Running...")
	http.ListenAndServe(":3000", http.DefaultServeMux)
}
```

### Step 4: Build and run

Run `go build` to build the project. Run `./server` to start it.

