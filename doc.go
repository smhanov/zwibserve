/*
Package zwibserve is an example collaboration server for zwibbler.com

The protocol is described in this Google Doc: https://docs.google.com/document/d/1X3_fzFqPUzTbPqF2GrYlSveWuv_L-xX7Cc69j13i6PY/edit?usp=sharing

Architecturally, It uses gorilla websockets and follows closely the hub and client example
given at https://github.com/gorilla/websocket/tree/master/examples/chat

A hub goroutine is responsible for keeping a collection of document IDs. Each
document ID has a list of clients connected to it.

Clients each run one goroutine for receiving messages, and one goroutine for sending.

The DocumentDB, which you can implement, actually stores the contents of the documents.
Before appending to a document, it must atomically check if the length the client has given matches
the actual length of the document. Default implementations MemoryDocumentDB and SQLITEDocumentDB are provided.

*/
package zwibserve
