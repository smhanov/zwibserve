package zwibserve

import (
	"errors"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// ErrConflict indicates that the base change ID does not match the existing one.
var ErrConflict error

// ErrMissing indicates that the document cannot be found.
var ErrMissing error

// ErrExists indicates that the document already exists
var ErrExists error

func init() {
	ErrConflict = errors.New("Conflict")
	ErrMissing = errors.New("Missing")
	ErrExists = errors.New("Already exists")
}

// CreateMode determines if the document should be created if it does not exists.
type CreateMode int

const (
	// PossiblyCreate creates file if it does not exist, otherwise return the existing one.
	PossiblyCreate = 0

	// NeverCreate returns the existing file. If it does not exist, return ErrMissing
	NeverCreate = 1

	// AlwaysCreate creates the file. If it exists already, return ErrExists
	AlwaysCreate = 2
)

// DocumentDB is the interface to a document storage.
type DocumentDB interface {
	// GetDocument creates or retrieves the document or returns an error, depending on the value of mode.
	// It returns the document and whether or not it was created in this call.
	GetDocument(docID string, mode CreateMode, initialData []byte) ([]byte, bool, error)

	// AppendDocument appends to the document if it exists and the oldLength
	// matches the actual one.
	// If the document is not present, it returns ErrMissing.
	// If the oldLength does not match the one recorded, then it returns ErrConflict and the current document length.
	AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error)
}

// Handler is an HTTP handler that will
// enable collaboration between clients.
type Handler struct {
	db  DocumentDB
	hub *hub
}

// NewHandler returns a new Zwibbler Handler. You must pass it a document database to use.
// You may use one of MemoryDocumentDB, SQLITEDocumentDB or create your own.
func NewHandler(db DocumentDB) *Handler {
	return &Handler{
		db:  db,
		hub: newHub(),
	}
}

// Configure the upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// ServeHTTP ...
func (zh *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got a connection\n")
	// Upgrade initial GET request to a websocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	runClient(zh.hub, zh.db, ws)
}
