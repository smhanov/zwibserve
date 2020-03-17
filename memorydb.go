package zwibserve

import (
	"log"
	"sync"
	"time"
)

// MemoryDocumentDB ...
type MemoryDocumentDB struct {
	mutex     sync.Mutex
	docs      map[string]*document
	lastClean time.Time
}

type document struct {
	data       []byte
	lastAccess time.Time
}

// NewMemoryDB ...
func NewMemoryDB() DocumentDB {
	return &MemoryDocumentDB{
		docs: make(map[string]*document),
	}
}

func (db *MemoryDocumentDB) clean() {
	// must be locked
	now := time.Now()

	if time.Since(db.lastClean).Minutes() < 60 {
		return
	}

	total := 0
	for docid, doc := range db.docs {
		if time.Since(doc.lastAccess).Hours() > 24 {
			log.Printf("Remove expired document %s", docid)
			delete(db.docs, docid)
			continue
		}

		total += cap(doc.data)
	}

	if total > 0 {
		log.Printf("%d documents use %d bytes of memory.", len(db.docs), total)
	}
	db.lastClean = now
}

// GetDocument ...
func (db *MemoryDocumentDB) GetDocument(docID string, mode CreateMode, initialData []byte) ([]byte, bool, error) {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	db.clean()
	created := false

	doc := db.docs[docID]
	if doc == nil && mode == NeverCreate {
		return nil, false, ErrMissing
	} else if doc != nil && mode == AlwaysCreate {
		return nil, false, ErrExists
	}

	if doc == nil {
		doc = &document{
			data: initialData,
		}
		db.docs[docID] = doc
		created = true
	}
	doc.lastAccess = time.Now()

	return doc.data, created, nil
}

// AppendDocument ...
func (db *MemoryDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	db.clean()

	doc := db.docs[docID]

	if doc == nil {
		return 0, ErrMissing
	}

	if uint64(len(doc.data)) != oldLength {
		return uint64(len(doc.data)), ErrConflict
	}

	doc.lastAccess = time.Now()
	doc.data = append(doc.data, newData...)

	return uint64(len(doc.data)), nil
}
