package zwibserve

import (
	"errors"
	"log"
	"net/http"
	"strings"

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

// NoExpiration is used in SetExpiration to indicate that documents should never expire.
const NoExpiration = -1

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

	// SetKey sets a key associated with a document
	// If the oldVersion matches the current version of the document, or the key is 0 and the document
	// does not exist, then set the key. In all other cases, return ErrConflict
	SetDocumentKey(docID string, oldVersion int, key Key) error

	// GetKey returns all keys associated with the document.
	GetDocumentKeys(docID string) ([]Key, error)

	// SetExpirationTime sets the number of seconds that a document is kept without any activity
	// before it is deleted. The zero value is the default (24 hours)
	SetExpiration(seconds int64)
}

// Key is a key that can be set by clients, related to the session.
type Key struct {
	Version int
	Name    string
	Value   string
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
	EnableCompression: true,
}

// ServeHTTP ...
func (zh *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got a connection\n")

	upgradeHeader := r.Header.Get("Upgrade")
	if !strings.Contains(upgradeHeader, "websocket") {
		w.Header().Set("Content-type", "text")
		w.Write([]byte("Zwibbler collaboration Server is running."))
		return
	}

	// Upgrade initial GET request to a websocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	runClient(zh.hub, zh.db, ws)
}
