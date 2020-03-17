package zwibserve

import "log"

type hub struct {
	ch      chan func()
	clients map[string][]*client
}

func newHub() *hub {
	h := &hub{
		ch:      make(chan func()),
		clients: make(map[string][]*client),
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
		h.clients[docID] = append(h.clients[docID], c)
	}
}

func (h *hub) removeClient(docID string, c *client) {
	h.ch <- func() {
		log.Printf("Client %v is removed from document %v", c.id, docID)
		list := h.clients[docID]
		log.Printf("    before removal: %v", list)
		for i, value := range list {
			if value == c {
				list[i] = list[len(list)-1]
				list = list[:len(list)-1]
				log.Printf("    after removal: %v", list)
				if len(list) > 0 {
					h.clients[docID] = list
				} else {
					delete(h.clients, docID)
				}
				break
			}
		}
	}
}

func (h *hub) broadcast(docID string, source *client, offset uint64, data []uint8) {
	h.ch <- func() {
		log.Printf("client %v broadcasts %v bytes @%v: %s", source.id,
			len(data), offset, string(data))

		log.Printf("client list: %v", h.clients[docID])
		for _, other := range h.clients[docID] {
			if other != source {
				log.Printf("... send to client %d", other.id)
				other.sendAppend(data, offset)
			}
		}
	}
}
