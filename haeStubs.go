//go:build !hae
// +build !hae

package zwibserve

import "github.com/gorilla/websocket"

// HAE is an interface that enables High Availability.
type peerList struct {
}

func newPeerList(_ *hub, _ DocumentDB) *peerList {
	return &peerList{}
}

func (p1 *peerList) SetServerID(id string) {

}

func (pl *peerList) SetUrls(urls []string) {
}

func (pl *peerList) SetSecurityInfo(secretUser, secretPassword, jwtKey string, keyIsBase64 bool) {
}

func (pl *peerList) HandleIncomingConnection(ws *websocket.Conn, m []uint8) {
}

func (peers *peerList) NotifyClientAddRemove(docID string, clientID string, docLength uint64, added bool) {
}

func (peers *peerList) NotifyAppend(docID string, offset uint64, data []byte) {
}

func (peers *peerList) NotifyBroadcast(docID string, data []byte) {

}

func (peers *peerList) NotifyKeyUpdated(docID string, clientID string, name, value string, sessionLifetime bool) {
}
