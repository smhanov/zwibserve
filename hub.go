package zwibserve

import (
	"log"
	"time"
)

type hub struct {
	ch             chan func()
	sessions       map[string]*session
	hooks          *webhookQueue
	webhookURL     string
	secretUser     string
	secretPassword string
	jwtKey         string
	keyIsBase64    bool
}

type session struct {
	clients []*client
	keys    []clientKey
}

type clientKey struct {
	Key
	owner *client
}

func newHub() *hub {
	h := &hub{
		ch:       make(chan func()),
		sessions: make(map[string]*session),
		hooks:    createWebhookQueue(),
	}

	go func() {
		for fn := range h.ch {
			fn()
		}
	}()

	return h
}

func (h *hub) addClient(docID string, c *client) {
	h.ch <- func() {
		log.Printf("Client %v registers for document %v", c.id, docID)
		s := h.sessions[docID]
		if s == nil {
			h.sessions[docID] = &session{
				clients: []*client{c},
			}

			// remove any queued idle-session webhook
			h.hooks.removeIf(func(event webhookEvent) bool {
				return event.name == "idle-session" && event.documentID == docID
			})
		} else {
			s.clients = append(s.clients, c)
		}
	}
}

// Immediately disconnect all clients and remove records of the document.
func (h *hub) signalDocumentDeleted(docID string) {
	h.ch <- func() {
		sess := h.sessions[docID]
		if sess != nil {
			log.Printf("Document deleted with %v clients: %v", len(sess.clients), docID)
			for _, client := range sess.clients {
				client.notifyLostAccess(errorDoesNotExist)
				// will be removed through normal mechanism.
			}
		} else {
			log.Printf("Doc %s has no clients.", docID)
		}
	}
}

func (h *hub) setWebhook(url, user, password string) {
	h.webhookURL = url
	h.secretUser = user
	h.secretPassword = password
}

func (h *hub) removeClient(docID string, c *client) {
	h.ch <- func() {
		log.Printf("Client %v is removed from document %v", c.id, docID)
		sess := h.sessions[docID]
		list := sess.clients
		for i, value := range list {
			if value == c {
				list[i] = list[len(list)-1]
				list = list[:len(list)-1]
				sess.clients = list
				if len(list) == 0 {
					delete(h.sessions, docID)

					// enqueue webhook
					if h.webhookURL != "" {
						h.hooks.add(webhookEvent{
							name:       "idle-session",
							url:        h.webhookURL,
							username:   h.secretUser,
							password:   h.secretPassword,
							documentID: docID,
							sendBy:     time.Now().Add(10 * time.Second),
						})
					}
				}
				break
			}
		}

		// now remove it's client keys
		n := 0
		var removed []Key
		for _, key := range sess.keys {
			if key.owner != c {
				sess.keys[n] = key
				n++
			} else {
				key.Value = ""
				key.Version++
				removed = append(removed, key.Key)
			}
		}
		sess.keys = sess.keys[:n]

		if len(removed) > 0 {
			for _, client := range sess.clients {
				client.notifyKeysUpdated(removed)
			}
		}
	}
}

func (h *hub) append(docID string, source *client, offset uint64, data []uint8) {
	h.ch <- func() {
		log.Printf("client %v appends %v bytes offset %v, send to %v other clients", source.id,
			len(data), offset, len(h.sessions[docID].clients)-1)

		for _, other := range h.sessions[docID].clients {
			if other != source {
				other.enqueueAppend(data, offset)
			}
		}
	}
}

func (h *hub) broadcast(docID string, source *client, data []uint8) {
	h.ch <- func() {
		log.Printf("client %v broadcasts %v bytes to %v other clients", source.id,
			len(data), len(h.sessions[docID].clients)-1)

		for _, other := range h.sessions[docID].clients {
			if other != source {
				other.enqueueBroadcast(data)
			}
		}
	}
}

func (h *hub) setSessionKey(docID string, source *client, key Key) {
	h.ch <- func() {
		for _, other := range h.sessions[docID].clients {
			if other != source {
				other.notifyKeysUpdated([]Key{key})
			}
		}
	}
}

func (h *hub) setClientKey(docID string, source *client, oldVersion, newVersion int, name, value string) bool {
	reply := make(chan bool)

	newKey := clientKey{
		owner: source,
		Key: Key{
			Version: newVersion,
			Name:    name,
			Value:   value,
		},
	}

	h.ch <- func() {
		sess := h.sessions[docID]
		found := false
		for i, k := range sess.keys {
			if k.Name == name {
				found = true
				if k.Version == oldVersion {
					sess.keys[i] = newKey
				} else {
					reply <- false
					return
				}
				break
			}
		}

		if !found && oldVersion == 0 {
			sess.keys = append(sess.keys, newKey)
			found = true
		}

		if found {
			for _, other := range h.sessions[docID].clients {
				if other != source {
					other.notifyKeysUpdated([]Key{newKey.Key})
				}
			}
		}

		reply <- found
	}

	return <-reply
}

func (h *hub) getClientKeys(docID string) []Key {
	reply := make(chan bool)
	var keys []Key
	h.ch <- func() {
		sess := h.sessions[docID]

		for _, k := range sess.keys {
			keys = append(keys, k.Key)
		}

		reply <- true
	}

	<-reply
	return keys
}

func (h *hub) updatePermissions(userid string, permissions string) {
	h.ch <- func() {
		for _, session := range h.sessions {
			for _, client := range session.clients {
				if client.userID == userid {
					client.notifyPermissionChange(permissions)
				}
			}
		}
	}
}
