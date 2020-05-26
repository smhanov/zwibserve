package zwibserve

import "log"

type hub struct {
	ch       chan func()
	sessions map[string]*session
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
		} else {
			s.clients = append(s.clients, c)
		}
	}
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
		log.Printf("client %v appends %v bytes offset %v: %s", source.id,
			len(data), offset, string(data))

		for _, other := range h.sessions[docID].clients {
			if other != source {
				log.Printf("... send to client %d", other.id)
				other.enqueueAppend(data, offset)
			}
		}
	}
}

func (h *hub) broadcast(docID string, source *client, data []uint8) {
	h.ch <- func() {
		log.Printf("client %v broadcasts %v bytes: %s", source.id,
			len(data), string(data))

		for _, other := range h.sessions[docID].clients {
			if other != source {
				log.Printf("... send to client %d", other.id)
				other.enqueueBroadcast(data)
			}
		}
	}
}

func (h *hub) setSessionKey(docID string, source *client, key Key) {
	h.ch <- func() {
		for _, other := range h.sessions[docID].clients {
			if other != source {
				log.Printf("... send to client %d", other.id)
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
					log.Printf("... send to client %d", other.id)
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
