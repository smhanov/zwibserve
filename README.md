# zwibserve [![GoDoc](https://godoc.org/github.com/smhanov/zwibserve?status.svg)](https://godoc.org/github.com/smhanov/zwibserve)

Package zwibserve is an example collaboration server for zwibbler.com. It lets users work on the same document together. The server portion is responsible only for storing the changes and broadcasting them to all connected clients. 

The collaboration works similar to source control. When I try to submit my changes, and you have submitted yours first, then my changes are rejected by the server. I must then modify my changes so they no longer conflict with yours, and try to submit them again.

The protocol is described in [Zwibbler Collaboration Server Protocol V2](https://docs.google.com/document/d/1X3_fzFqPUzTbPqF2GrYlSveWuv_L-xX7Cc69j13i6PY/edit?usp=sharing)

[Test your server online](https://zwibbler.com/collaboration/testing.html).

## What do I need?
* Linux or Windows server. [.rpm, .deb, and .exe](https://github.com/smhanov/zwibbler-service/releases) installers are provided. 
* Security certificates are highly recommended. Your server needs to be running on HTTPS if your web page containing the drawing tool is also served over HTTPS, otherwise the browser will not allow it to connect. 

## Quick install

Use the .deb or .rpm packages from https://github.com/smhanov/zwibbler-service/releases . This will install a system service that automatically restarts if interrupted.
After installation, it will be running on port 3000 as non-https. You can check this by going to http://yourserver.com:3000 in a web browser. You should receive a message that says it is working.

The next step is to enable HTTPS using your certificate and private key file. You need HTTPS because if your web site is served using HTTPS, it will not be able to contact the collaboration server unless it is also HTTPS.

Edit /etc/zwibbler.conf and change the port to the HTTPS port 443:

    ServerBindAddress=0.0.0.0
    ServerPort=443
    CertFile=
    KeyFile=

Change CertFile and KeyFile to be the path to your SSL certificate information on the system. CertFile is your certificate, and KeyFile is your private key.

Next, restart the service using

    systemctl restart zwibbler

You can view the logs using

    sudo tail -f /var/log/zwibbler/zwibbler.log

You should now be able to test using https://zwibbler.com/collaboration and entering wss://yourserver/socket in the URL with no port.

### Using an nginx proxy
The method above will dedicate your server to running only Zwibbler, since it takes over the HTTPS port 443. If you want to run other things on the same machine, you will likely be using the nginx web server. You can run zwibbler on a different port (eg, 3000) and configure nginx to forward requests from a certain URL to zwibbler.

In this case, you can leave zwibbler.conf at the default, with blank CertFile and KeyFile.  They will not be necessary since nginx will handle the security.

    ServerBindAddress=0.0.0.0
    ServerPort=3000
    CertFile=
    KeyFile=

In your nginx configuration, include this in your server {} block. This will redirect anything on https://yourserver/socket to the zwibbler service running on port 3000.

    location /socket {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
    }

### Using Windows Server
After installing the [setup file](https://github.com/smhanov/zwibbler-service/releases), follow the instructions to [redirect traffic from the /socket url to your server.](https://docs.google.com/document/d/13pb4Accpa1B62gLBcwsWJDSmUND14qjXVIdJwnYc4tg/edit?usp=sharing)

On Windows, the zwibbler.log, zwibbler.conf, and zwibbler.db files are all located in C:\zwibbler, and they will not be removed if you uninstall the server.

### Where is the data? What if it fails?

The data is stored in an SQLITE database in /var/lib/zwibbler/zwibbler.db. The collaboration server is designed to store data only while a session is active. Long term storage should use [ctx.save()](https://zwibbler.com/docs/#save) and store the data using other means. Sessions that have not been accessed in 24 hours are purged.

If the collaboration server restarts, clients will gracefully reconnect without the user noticing anything wrong and any active sessions are preserved.

Multiple instances of the server are not supported at this time. There must be one single collaboration server. It will support thousands of users without problems.

## Using it from a go project
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
git clone https://github.com/smhanov/zwibserve.git
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

