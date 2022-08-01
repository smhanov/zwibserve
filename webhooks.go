package zwibserve

import (
	"context"
	"log"
	"sync"
	"time"
)

type webhookEvent struct {
	sendBy     time.Time
	url        string
	name       string
	documentID string
	username   string
	password   string
}

type webhookQueue struct {
	events []webhookEvent
	mutex  sync.Mutex
	cancel func()
}

func createWebhookQueue() *webhookQueue {
	whq := &webhookQueue{
		cancel: func() {},
	}

	go func() {
		// in a loop, figure out how long we have to sleep for and then
		// wait for that amount of time.

		for {
			// set up the cancellable timeout.
			whq.mutex.Lock()
			ctx, cancel := context.WithCancel(context.Background())

			now := time.Now()
			at := now.Add(time.Hour * 24)
			// determine the minimum event
			removed := 0
			for i := range whq.events {
				item := whq.events[i]
				if item.sendBy.Before(now) {
					go item.send()
					removed++
					continue
				} else if item.sendBy.Before(at) {
					at = item.sendBy
				}

				if removed > 0 {
					whq.events[i-removed] = whq.events[i]
				}
			}
			whq.events = whq.events[:len(whq.events)-removed]

			whq.cancel = cancel
			whq.mutex.Unlock()

			select {
			case <-ctx.Done():
			case <-time.After(at.Sub(now)):
			}
		}
	}()

	return whq
}

func (whq *webhookQueue) removeIf(fn func(event webhookEvent) bool) {
	whq.mutex.Lock()
	defer whq.mutex.Unlock()
	removed := 0
	l := len(whq.events)
	for i := range whq.events {
		if fn(whq.events[i]) {
			removed++
			log.Printf("Remove queued webhook %s/%s", whq.events[i].name, whq.events[i].documentID)
		} else if removed > 0 {
			whq.events[i-removed] = whq.events[i]
		}
	}

	whq.events = whq.events[:l-removed]
}

func (whq *webhookQueue) add(event webhookEvent) {
	whq.mutex.Lock()
	defer whq.mutex.Unlock()
	log.Printf("Queue webhook %s/%s", event.name, event.documentID)
	whq.events = append(whq.events, event)
	whq.cancel()
}

func (event webhookEvent) send() {
	var reply string
	result := MakeHTTPRequest(HTTPRequestArgs{
		Method: "POST",
		URI:    event.url,
		Data: map[string]interface{}{
			"event":      event.name,
			"documentID": event.documentID,
		},
		Username: event.username,
		Password: event.password,
	}, &reply)

	log.Printf("%s/%s to %s: HTTP Status=%v Error=%v", event.name, event.documentID, event.url,
		result.StatusCode, result.Err)
}
