package zwibserve

import (
	"errors"
	"log"
	"net/http"
	"runtime"
	"strings"

	"github.com/gorilla/websocket"
)

// ErrConflict indicates that the base change ID does not match the existing one.
var ErrConflict error

// ErrMissing indicates that the document cannot be found.
var ErrMissing error

// ErrExists indicates that the document already exists
var ErrExists error

// ErrTokenExists indicates that the token being added already exists.
var ErrTokenExists error

func init() {
	ErrConflict = errors.New("Conflict")
	ErrMissing = errors.New("Missing")
	//lint:ignore ST1005 error text is specified in the protocol
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
	// before it is deleted. The zero value is the default (never)
	SetExpiration(seconds int64)

	DeleteDocument(docID string) error

	// AddToken shall associate token/document/user/permissions together
	// if token already exists, return ErrExists
	// if contents specified and document already exists, return ErrConflict
	AddToken(token, docID, userID, permissions string, expirationSeconds int64, contents []byte) error

	// Given a token, returns docID, userID, permissions. If it does not exist or is expired,
	// the error is ErrMissing
	GetToken(token string) (string, string, string, error)

	// If the user has any tokens, the permissions of all of them are updated.
	UpdateUser(userID, permissions string) error
}

// Key is a key that can be set by clients, related to the session.
type Key struct {
	Version int
	Name    string
	Value   string
}

// HAE is an interface that enables High Availability.
// It is not included in the open source version.
type HAE interface {
	SetServerID(id string)
	SetUrls(urls []string)
	SetSecurityInfo(secretUser, secretPassword, jwtKey string, keyIsBase64 bool)
	HandleIncomingConnection(ws *websocket.Conn, m []uint8)
	NotifyClientAddRemove(docID string, clientID string, docLength uint64, added bool)
	NotifyAppend(docID string, offset uint64, data []byte)
	NotifyBroadcast(docID string, data []byte)
	NotifyKeyUpdated(docID, clientID, name, value string, sessionLifetime bool)
}

// Hub is an interface to the collaboration hub that is used for high availability
// extensions.
type Hub interface {
	Append(docid, clientid string, offset uint64, data []byte)
	Broadcast(docid, clientid string, data []byte)
	EachKey(f func(docID, clientID, name, value string, sessionLifetime bool))
	EachClient(f func(docID, clientID string, docLength uint64))
	SetClientKey(docID string, sourceID string, oldVersion, newVersion int, name, value string) bool
	SetSessionKey(docID string, sourceID string, key Key)
	RemoveClient(docID string, clientID string)
	CheckMissedUpdate(docid string, doc []byte, keys []Key)
}

// Handler is an HTTP handler that will
// enable collaboration between clients.
type Handler struct {
	db               DocumentDB
	hub              *hub
	allowCompression bool
	secretUser       string
	secretPassword   string
	webhookURL       string
}

// NewHandler returns a new Zwibbler Handler. You must pass it a document database to use.
// You may use one of MemoryDocumentDB, SQLITEDocumentDB or create your own.
func NewHandler(db DocumentDB) *Handler {
	return &Handler{
		db:               db,
		hub:              newHub(db),
		allowCompression: true,
	}
}

// Configure the upgrader
var globalUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	EnableCompression: true,
}

// SetCompressionAllowed allows you to always disable socket compression.
// By default, socket compression is allowed.
func (zh *Handler) SetCompressionAllowed(allowed bool) {
	zh.allowCompression = allowed
}

// SetSecretUser allow you to set the secret username and password
// used in Webhooks and to authenticate requests like dumping and
// deleting documents.
func (zh *Handler) SetSecretUser(username, password string) {
	zh.secretUser = username
	zh.secretPassword = password
	zh.hub.setWebhook(zh.webhookURL, zh.secretUser, zh.secretPassword)
	zh.hub.swarm.SetSecurityInfo(zh.hub.secretUser, zh.hub.secretPassword, zh.hub.jwtKey, zh.hub.keyIsBase64)
}

// SetJWTKey enables JWT mode, so that only document IDs contained inside a
// valid JWT will be accepted. The tokens must be signed using HMAC-SHA256
// and this secret key.
func (zh *Handler) SetJWTKey(key string, keyIsBase64 bool) {
	zh.hub.jwtKey = key
	zh.hub.keyIsBase64 = keyIsBase64
	zh.hub.swarm.SetSecurityInfo(zh.hub.secretUser, zh.hub.secretPassword, zh.hub.jwtKey, zh.hub.keyIsBase64)
}

// SetSwarmURLs sets the urls of other servers in the swarm.
func (zh *Handler) SetSwarmURLs(urls []string) {
	zh.hub.swarm.SetUrls(urls)
}

// SetServeID sets the server ID of the server for use with High Availability.
// If unset, a random server ID is chosen.
func (zh *Handler) SetServerID(id string) {
	zh.hub.swarm.SetServerID(id)
}

// EnableHAE enables High Availability Extensions using the given
// interface to the implementation. This must be the first method
// that you call, before setting the security info, server id, etc.
func (zh *Handler) EnableHAE(hae HAE) {

}

// SetWebhookURL sets a url to receive an event, a few minutes after
// all users have left a session.
func (zh *Handler) SetWebhookURL(url string) {
	zh.webhookURL = url
	zh.hub.setWebhook(zh.webhookURL, zh.secretUser, zh.secretPassword)
}

// ServeHTTP ...
func (zh *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgradeHeader := r.Header.Get("Upgrade")
	compression := r.FormValue("compression")

	if !strings.Contains(upgradeHeader, "websocket") {
		var handled bool
		RecoverErrors(CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled = zh.serveMAPI(w, r)
		})))(w, r)

		if !handled && r.Method != "POST" {
			w.Header().Set("Content-type", "text")
			w.Write([]byte("Zwibbler collaboration Server is running."))
		}
		return
	}

	log.Printf("Got a connection\n")
	upgrader := globalUpgrader // copy

	// compression not supported on Windows Server 2016.
	if runtime.GOOS == "windows" || compression == "0" || !zh.allowCompression {
		log.Printf("Disabling socket compression")
		upgrader.EnableCompression = false
	}

	// Upgrade initial GET request to a websocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	runClient(zh.hub, zh.db, ws)
}
