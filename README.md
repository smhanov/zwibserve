# zwibserve [![GoDoc](https://godoc.org/github.com/smhanov/zwibserve?status.svg)](https://godoc.org/github.com/smhanov/zwibserve)

Package zwibserve is an example collaboration server for zwibbler.com. It lets users work on the same document together. The server portion is responsible only for storing the changes and broadcasting them to all connected clients. 

The collaboration works similar to source control. When I try to submit my changes, and you have submitted yours first, then my changes are rejected by the server. I must then modify my changes so they no longer conflict with yours, and try to submit them again.

The protocol is described in [Zwibbler Collaboration Server Protocol V3](https://docs.google.com/document/d/1l2BsVVsP6mD_BuzErIdD8ig1ICur6D3V0vrYQVWJTDQ/edit?usp=sharing). There are additional methods that add security and the ability to delete documents described in [Zwibbler Collaboration Server Management API](https://docs.google.com/document/d/1vdUUEooti4F5Ob9rca2DVoOJOxyO2uftaCOUzKdXb4M/edit?usp=sharing).

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
    Compression=

Change CertFile and KeyFile to be the path to your SSL certificate information on the system. CertFile is your certificate, and KeyFile is your private key. You can leave the other settings as they are. Specifying Compression=0 will disable socket compression. Otherwise, it will be enabled by default.

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

### Using an Apache proxy
If you are running the Apache web server and have existing services running on it, you will need to configure it to forward requests for the socket to the Zwibbler collaboration service.

When using a proxy, leave the CertFile/KeyFile blank and keep the Zwibbler service on a different port than 3000. In this case, Apache will handle the security. Example /etc/zwibbler.conf file:

    ServerBindAddress=0.0.0.0
    ServerPort=3000
    CertFile=
    KeyFile=

If you are running CentOS, you may need to install the Apache proxy module, and configure security to allow Apache to make connections. If the second line fails because you do not have SELinux installed, that is OK.

    sudo yum install mod_proxy
    sudo /usr/sbin/setsebool -P httpd_can_network_connect 1

Find the virtual host configuration for your web site. It may be in /etc/httpd/conf/httpd.conf or in a file under /etc/httpd/conf.d/. Add the SSLProxyEngine and <Location></Location> lines to your virtual host configuration for your web site. It says to forward any request to /socket to our collaboration service running on port 3000. Here is an example.

	<VirtualHost *:443>
	    SSLEngine on 
	    SSLProxyEngine On
	    ServerName www.example.com
	    DocumentRoot "/var/www/html"
	    SSLCertificateFile /path/to/.crt
	    SSLCertificateKeyFile /path/to/.key
	    
	    # ADD THESE LINES
	    SSLProxyEngine on 
	    <Location "/socket">
		ProxyPass ws://localhost:3000/socket
		ProxyPassReverse ws://localhost:3000/socket
	    </Location>
	</VirtualHost>

### Using Windows Server
After installing the [setup file](https://github.com/smhanov/zwibbler-service/releases), follow the instructions to [redirect traffic from the /socket url to your server.](https://docs.google.com/document/d/13pb4Accpa1B62gLBcwsWJDSmUND14qjXVIdJwnYc4tg/edit?usp=sharing)

On Windows, the zwibbler.log, zwibbler.conf, and zwibbler.db files are all located in C:\zwibbler, and they will not be removed if you uninstall the server.

## Testing if it is working

Once you have the server running, test if it is working by going to https://yourserver.com/socket in your web browser. You should see a message like this:

    Zwibbler collaboration Server is running.
    
After that, go to https://zwibbler.com/collaboration/testing.html and enter the your server wss:// url in the box (eg, wss://yourserver.com/socket) and click on "Run Tests". You should see the tests all pass and turn green.

### Where is the data? What if it fails?


If the collaboration server restarts, clients will gracefully reconnect without the user noticing anything wrong and any active sessions are preserved.

Multiple instances of the server are not supported at this time. There must be one single collaboration server. It will support thousands of users without problems.

There are two options for data storage.
#### Sqlite (default)
The data is stored in an SQLITE database in /var/lib/zwibbler/zwibbler.db. The collaboration server is designed to store data only while a session is active. Long term storage should use [ctx.save()](https://zwibbler.com/docs/#save) and store the data using other means. Sessions that have not been accessed in 24 hours are purged.

#### Redis

To use redis, add these lines to the zwibbler.conf file:

    # Can be sqlite or redis
    Database=redis

    # If using Redis, these are the settings.
    # Default: localhost:6379
    RedisServer=
    RedisPassword=
    
## Advanced options

### Security
The [Zwibbler Collaboration Server Management API](https://docs.google.com/document/d/1vdUUEooti4F5Ob9rca2DVoOJOxyO2uftaCOUzKdXb4M/edit?usp=sharing) adds additional security, so that a skilled student hacker will be unable to alter the Javascript and write to a teacher's whiteboard unless given permission to do so. In this case, you must configure a username and password, and configure your own server software make a request to add a token with permissions before each persion connects to a session. That way, participants  connect using a token instead of a session identifier, and the permissions are enforced by the collaboration server instead of the client browser. Any management requests are authenticated using HTTP Basic Authentication with the given username and password.

    # If set, the management API is enabled to allow deleting and dumping documents.
    SecretUser=
    SecretPassword=

    # If set, this webhook will be called when all users have left a session.
    # See the API documents on Google Drive for details.
    Webhook=


### JWT (Javascript Web Tokens)
If desired, the server can be configured to only accept session identifiers contained inside of a JWT. The JWT also contains permission information, but are signed using a preconfigured key. That way, only authorized individuals will be able to write to a whiteboard. Using JWT means that the tokens do not need to be registered in advance with the collaboration server. The format of the tokens is described in [Zwibbler Collaboration Server Management API](https://docs.google.com/document/d/1vdUUEooti4F5Ob9rca2DVoOJOxyO2uftaCOUzKdXb4M/edit#heading=h.wrucymxrj81i)

    # If set, only JWT tokens will be accepted as document identifiers from the client.
    # The HMAC-SHA256 signature method is used.
    JWTKey=
    JWTKeyIsBase64=False

    
### Increasing maximum number of connections
To support more than 1024 connections on Linux, you will have to increase your system limit on the number of file handles. This is often done by adding these lines to /etc/security/limits.conf:

    * hard nofiles 100000
    * soft nofiles 100000
    
To tell Zwibbler to take advantage of these limits, you set MaxFiles to the same value in /etc/zwibbler.conf:
    
    # Attempt to set the open file limit of the operating system to this value.
    # This must be less than or equal to the hard limit of the operating system.
     MaxFiles=100000
     
## Load testing
The current load testing results are available in the [Zwibbler Collaboration Server Load Testing](https://docs.google.com/document/d/1P6wzmka-C3ZbJXgfFGjYgR1v4be3GkLw6tclpwzZn5s/edit?usp=sharing) guide. A single server can support many thousands of connections, even when using the built-in SQLITE database.

The collaboration server has command line options that enable it to perform load testing on a server on another machine. It does this by simulating a number of participants viewing and writing to a single whiteboard. These are termed students and teachers.

| Command | Description |
| --- | --- |
| --test <socket url> | Test the server at the given websocket url (eg. ws://yourserver:3000/socket) |
| --docid <string> | The name of the whiteboard all participants connect to |
| --teachers <number> | The number of teachers to simulate. |
| --students <number> | The number of students to simulate. |
| --verbose | Show all actions from every student and teacher |

All initial connections will be made over a few seconds, to avoid a long backlog. A simulated student simply connects to the whiteboard and receives the initial contents as well as updates from it. A teacher will connect, and continuously update the whiteboard with new changes as well as receiving updates from other teachers. The teachers make changes at random intervals that average to one second. If there are multiple teachers, the changes may conflict and have to be resent. The test continues until stopped by the user using CTRL-C.

During the test you will see statistics about the test, including the screen-to-screen time. This is the amount of time between when a teacher makes a change to when a student sees that change on his whiteboard.

     Connections=51 docLength=149360 Screen-to-screen time avg=71ms min=19ms max=958ms

The changes are nonsensical data, not real whiteboard commands, so the real zwibbler will be unable to connect to the document used.

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

