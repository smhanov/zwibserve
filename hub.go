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
	swarm          HAE
}

type session struct {
	clients []*client
	keys    []clientKey
}

type clientKey struct {
	Key
	owner string // clientID or serverID-clientID
}

func newHub(db DocumentDB) *hub {
	h := &hub{
		ch:       make(chan func()),
		sessions: make(map[string]*session),
		hooks:    createWebhookQueue(),
	}
	h.swarm = newPeerList(h, db)

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

		h.swarm.NotifyClientAddRemove(docID, c.id, c.lastEnd, true)
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

func (h *hub) RemoveClient(docID string, clientID string) {
	h.ch <- func() {
		log.Printf("Client %v is removed from document %v", clientID, docID)
		sess := h.sessions[docID]
		if sess != nil {
			list := sess.clients
			for i, value := range list {
				if value.id == clientID {
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
				if key.owner != clientID {
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

			if !isRemoteID(clientID) {
				h.swarm.NotifyClientAddRemove(docID, clientID, 0, false)
			}
		}
	}
}

func isRemoteID(id string) bool {
	// return true if id contains a '-'
	for _, c := range id {
		if c == '-' {
			return true
		}
	}
	return false
}

func (h *hub) Append(docID string, source string, offset uint64, data []uint8) {
	h.ch <- func() {
		if _, ok := h.sessions[docID]; ok {
			log.Printf("client %v appends %v bytes offset %v, send to %v other clients", source,
				len(data), offset, len(h.sessions[docID].clients)-1)

			for _, other := range h.sessions[docID].clients {
				if other.id != source {
					other.enqueueAppend(data, offset)
				}
			}

			// if source is nil, it came from a remote server.
			log.Printf("source %v isRemote?: %v", source, isRemoteID(source))
			if !isRemoteID(source) {
				h.swarm.NotifyAppend(docID, offset, data)
			}
		}
	}
}

func (h *hub) Broadcast(docID string, sourceID string, data []uint8) {
	h.ch <- func() {
		if _, ok := h.sessions[docID]; ok {
			log.Printf("client %v broadcasts %v bytes to %v other clients", sourceID,
				len(data), len(h.sessions[docID].clients)-1)

			for _, other := range h.sessions[docID].clients {
				if other.id != sourceID {
					other.enqueueBroadcast(data)
				}
			}

			if !isRemoteID(sourceID) {
				h.swarm.NotifyBroadcast(docID, data)
			}
		}
	}
}

func (h *hub) SetSessionKey(docID string, sourceID string, key Key) {
	h.ch <- func() {
		if _, ok := h.sessions[docID]; ok {
			for _, other := range h.sessions[docID].clients {
				if other.id != sourceID {
					other.notifyKeysUpdated([]Key{key})
				}
			}

			if !isRemoteID(sourceID) {
				h.swarm.NotifyKeyUpdated(docID, sourceID, key.Name, key.Value, true)
			}
		}
	}
}

func (h *hub) SetClientKey(docID string, sourceID string, oldVersion, newVersion int, name, value string) bool {
	reply := make(chan bool)

	newKey := clientKey{
		owner: sourceID,
		Key: Key{
			Version: newVersion,
			Name:    name,
			Value:   value,
		},
	}

	h.ch <- func() {
		found := false
		if _, ok := h.sessions[docID]; ok {

			sess := h.sessions[docID]
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
					if other.id != sourceID {
						other.notifyKeysUpdated([]Key{newKey.Key})
					}
				}
			}

			if !isRemoteID(sourceID) {
				h.swarm.NotifyKeyUpdated(docID, sourceID, name, value, false)
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

func (h *hub) run(fn func()) {
	reply := make(chan bool)
	h.ch <- func() {
		fn()
		reply <- true
	}
	<-reply
}

func (h *hub) EachKey(fn func(docID, clientID, name, value string, sessionLifetime bool)) {
	h.run(func() {
		for docID, sess := range h.sessions {
			for _, key := range sess.keys {
				clientID := key.owner
				name := key.Name
				value := key.Value
				fn(docID, clientID, name, value, false)
			}
		}
	})
}

func (h *hub) EachClient(fn func(docID, clientID string, docLength uint64)) {
	h.run(func() {
		for docID, sess := range h.sessions {
			for _, client := range sess.clients {
				fn(docID, client.id, client.lastEnd)
			}
		}
	})
}

func (h *hub) CheckMissedUpdate(docid string, doc []byte, keys []Key) {
	h.run(func() {
		sess := h.sessions[docid]
		if sess != nil {
			log.Printf("Check missed updates for doc %s", docid)
			for _, client := range sess.clients {
				if len(doc) > 0 {
					client.enqueueAppend(doc, 0)
				}
				client.notifyKeysUpdated(keys)
			}
		}
	})
}
