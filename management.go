package zwibserve

import (
	"crypto/subtle"
	"io"
	"log"
	"net/http"
	"time"
)

// This file implements the server management API
// https://docs.google.com/document/d/1vdUUEooti4F5Ob9rca2DVoOJOxyO2uftaCOUzKdXb4M/edit#

func (zh *Handler) serveMAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "POST" {
		method := r.FormValue("method")

		switch method {
		case "addToken":
			zh.handleAddToken(w, r)
		case "updateUser":
			zh.handleUpdateUser(w, r)
		case "createDocument":
			zh.handleCreateDocument(w, r)
		case "deleteDocument":
			zh.handleDeleteDocument(w, r)
		case "dumpDocument":
			zh.handleDumpDocument(w, r, true)
		case "checkDocument":
			zh.handleDumpDocument(w, r, false)
		default:
			HTTPPanic(400, "Unknown 'method' parameter")
		}
		return true
	}
	return false
}

func (zh *Handler) verifyAuth(r *http.Request) {
	username, password, ok := r.BasicAuth()
	eq1 := subtle.ConstantTimeCompare([]byte(username), []byte(zh.secretUser))
	eq2 := subtle.ConstantTimeCompare([]byte(password), []byte(zh.secretPassword))
	if !ok || eq1 == 0 || eq2 == 0 || (zh.secretUser == "" && zh.secretPassword == "") {
		log.Printf("    Request not authorized; got %s/%s", username, password)
		HTTPPanic(401, "Unauthorized")
	}
}

func mustGet(r *http.Request, key string) string {
	value := r.FormValue(key)
	if value == "" {
		log.Printf("    Missing " + key)
		HTTPPanic(400, "Missing "+key)
	}
	return value
}

func (zh *Handler) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got request for createDocument")
	zh.verifyAuth(r)

	docID := mustGet(r, "documentID")
	contents := []byte(r.FormValue("contents"))

	// if there is no string by that name, then try a file.
	if len(contents) == 0 {
		file, _, err := r.FormFile("contents")
		if err != nil {
			HTTPPanic(400, "Missing contents")
		}
		defer file.Close()

		// read the entire file as a string.
		contents, err = io.ReadAll(file)
		if err != nil {
			HTTPPanic(400, "Error reading contents: "+err.Error())
		}
	}

	_, _, err := zh.db.GetDocument(docID, AlwaysCreate, []byte(contents))
	if err == ErrExists {
		w.WriteHeader(409)
		return
	} else if err != nil {
		panic(err)
	}

	w.WriteHeader(200)
}

func (zh *Handler) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got request for deleteDocument")
	zh.verifyAuth(r)

	docID := mustGet(r, "documentID")
	zh.hub.signalDocumentDeleted(docID)
	err := zh.db.DeleteDocument(docID)
	if err != nil {
		panic(err)
	}

	w.WriteHeader(200)
}

func (zh *Handler) handleDumpDocument(w http.ResponseWriter, r *http.Request, dump bool) {
	log.Printf("Got request for dumpDocument")
	zh.verifyAuth(r)

	docID := mustGet(r, "documentID")

	contents, _, err := zh.db.GetDocument(docID, NeverCreate, nil)

	if err == ErrMissing {
		w.WriteHeader(404)
		return
	} else if err != nil {
		log.Panic(err)
	}

	if dump {
		w.Header().Set("Content-Type", "text")
		w.Write(contents)
	} else {
		w.WriteHeader(200)
	}
}

func (zh *Handler) handleAddToken(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got request for addToken")
	zh.verifyAuth(r)
	docID := mustGet(r, "documentID")
	token := mustGet(r, "token")
	userID := mustGet(r, "userID")
	permissions := r.FormValue("permissions")
	contents := r.FormValue("contents")
	expiration := mustGet(r, "expiration")

	expirationTime, err := time.Parse(time.RFC1123, expiration)
	if err != nil {
		HTTPPanic(400, "Incorrect expires format")
	}

	err = zh.db.AddToken(token, docID, userID, permissions, expirationTime.Unix(), []byte(contents))
	log.Printf("AddToken %s for doc %s user %s", token, docID, userID)
	if err == ErrExists || err == ErrConflict {
		w.WriteHeader(409)
	} else if err != nil {
		log.Printf("Error is %v", err)
		log.Panic(err)
	} else {
		w.WriteHeader(200)
	}
}

func (zh *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	log.Printf("Got request for updateUser")
	zh.verifyAuth(r)
	userID := mustGet(r, "userID")
	permissions := r.FormValue("permissions")

	err := zh.db.UpdateUser(userID, permissions)

	if err != nil {
		log.Panic(err)
	}

	zh.hub.updatePermissions(userID, permissions)
	w.WriteHeader(200)
}
