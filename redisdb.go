package zwibserve

import (
	"encoding/json"
	"sync"
	"time"

	"context"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

// RedisDocumentDB is a document database using SQLITE. The documents are all stored in a single file database.
type RedisDocumentDB struct {
	expiration int64
	rdb        *redis.Client
	mutex      sync.Mutex
}

// NewRedisDB creates a new document storage based on SQLITE
func NewRedisDB(options *redis.Options) DocumentDB {

	db := &RedisDocumentDB{
		rdb: redis.NewClient(options),
	}

	return db
}

// SetExpiration ...
func (db *RedisDocumentDB) SetExpiration(seconds int64) {
	db.expiration = seconds
}

func getDocID(docID string) string {
	return "zwibbler:" + docID
}

func getKeys(docID string) string {
	return "zwibbler-keys:" + docID
}

// GetDocument ...
func (db *RedisDocumentDB) GetDocument(docID string, mode CreateMode, initialData []byte) ([]byte, bool, error) {

	docID = getDocID(docID)
	var doc []byte
	var created bool
	var err error

	// handle possibly and always create using a transaction.
	err = db.executeWatch(func(tx *redis.Tx) error {
		doc, err = tx.Get(ctx, docID).Bytes()

		// if the key exists
		if err == nil {
			// in Possibly create, or Never_create, we are done.
			switch mode {
			case PossiblyCreate, NeverCreate:
				created = false
				return nil
			case AlwaysCreate:
				return ErrExists
			}
		} else if err != redis.Nil {
			return err
		}

		// The key does not exist.
		if mode == NeverCreate {
			return ErrMissing
		}

		created = true
		doc = initialData

		// document does not exist. Create it.
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			_, err := pipe.Set(ctx, docID, string(initialData), db.getRedisExpiration()).Result()
			return err
		})

		return err
	}, docID)

	if err != nil {
		return nil, false, err
	}

	return doc, created, nil

}

func (db *RedisDocumentDB) getRedisExpiration() time.Duration {
	switch db.expiration {
	case NoExpiration:
		return 0
	case 0:
		return 24 * time.Hour
	}
	return time.Duration(db.expiration) * time.Second
}

// AppendDocument ...
func (db *RedisDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	var actualLength uint64
	var err error

	docID = getDocID(docID)

	err = db.executeWatch(func(tx *redis.Tx) error {
		actualLength, err = tx.StrLen(ctx, docID).Uint64()

		if err != nil {
			return err
		} else if actualLength != oldLength {
			return ErrConflict
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			_, err = pipe.Append(ctx, docID, string(newData)).Result()
			if err != nil {
				return err
			}

			expiration := db.getRedisExpiration()
			if expiration > 0 {
				_, err = pipe.Expire(ctx, docID, db.getRedisExpiration()).Result()
			}

			actualLength = oldLength + uint64(len(newData))

			return err
		})

		return err
	}, docID)

	return actualLength, err
}

// GetDocumentKeys ...
func (db *RedisDocumentDB) GetDocumentKeys(docID string) ([]Key, error) {

	var keys []Key
	docID = getKeys(docID)
	m, err := db.rdb.HGetAll(ctx, docID).Result()
	if err != nil {
		return nil, err
	}

	for _, value := range m {
		var key Key
		err = json.Unmarshal([]byte(value), &key)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// SetDocumentKey ...
func (db *RedisDocumentDB) SetDocumentKey(docID string, oldVersion int, key Key) error {
	// Keys are stored as hash maps as JSON

	// create the key
	docID = getKeys(docID)
	newJSON, err := json.Marshal(key)

	if err != nil {
		return err
	}

	// in a transaction, set the key if it matches the old version
	return db.executeWatch(func(tx *redis.Tx) error {
		var currentKey Key
		exists := true
		currentJSON, err := tx.HGet(ctx, docID, key.Name).Bytes()

		if err == redis.Nil {
			err = nil
			exists = false
		}

		// if key exists and the old version does not match, return conflict.
		if exists {
			err = json.Unmarshal(currentJSON, &currentKey)
			if err != nil {
				return err
			}

			if currentKey.Version != oldVersion {
				return ErrConflict
			}

			// if key does not exists and the older version is not zero, it is also a conflict.
		} else if oldVersion != 0 {
			return ErrConflict
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			_, err = tx.HSet(ctx, docID, key.Name, newJSON).Result()
			return err
		})
		return err
	}, docID)
}

func (db *RedisDocumentDB) executeWatch(watchfn func(tx *redis.Tx) error, keys ...string) error {
	var err error
	for {
		err = db.rdb.Watch(ctx, watchfn, keys...)
		if err == redis.TxFailedErr {
			continue
		}

		break
	}
	return err
}
