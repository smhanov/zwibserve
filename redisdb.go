package zwibserve

import (
	"encoding/json"
	"log"
	"time"

	"context"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

// RedisDocumentDB is a document database using Redis
// The documents are stored as a string with the key "zwibbler:"+docID
// The keys for the document are stored as an HKEY with the key "zwibbler-keys:"+docID
// The tokens are stored as an hkey under the name:
// zwibbler-token: and have docID, userID, permissions.
// zwibbler-user: maps from userid to a set of tokens associated with the user.
// The zwibbler-user keys must be periodically cleaned because they do not automatically expire,
// but the tokens do.
type RedisDocumentDB struct {
	expiration int64
	rdb        *redis.Client
}

// NewRedisDB creates a new document storage based on Redis
func NewRedisDB(options *redis.Options) DocumentDB {

	db := &RedisDocumentDB{
		rdb: redis.NewClient(options),
	}

	go func() {
		db.runMaintenance()
		time.Sleep(time.Hour * 24)
	}()

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

func getToken(tokenID string) string {
	return "zwibbler-token:" + tokenID
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
	if db.expiration == 0 || db.expiration == NoExpiration {
		return 0;
	}
	return time.Duration(db.expiration) * time.Second
}

// AppendDocument ...
func (db *RedisDocumentDB) AppendDocument(docIDin string, oldLength uint64, newData []byte) (uint64, error) {
	var actualLength uint64
	var err error

	docID := getDocID(docIDin)

	err = db.executeWatch(func(tx *redis.Tx) error {
		actualLength, err = tx.StrLen(ctx, docID).Uint64()

		if err != nil {
			return err
		} else if actualLength != oldLength {
			return ErrConflict
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Append(ctx, docID, string(newData))
			expiration := db.getRedisExpiration()
			if expiration > 0 {
				pipe.Expire(ctx, docID, db.getRedisExpiration())
				pipe.Expire(ctx, getKeys(docIDin), db.getRedisExpiration())
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
func (db *RedisDocumentDB) SetDocumentKey(docIDin string, oldVersion int, key Key) error {
	// Keys are stored as hash maps as JSON

	// create the key
	keysID := getKeys(docIDin)
	newJSON, err := json.Marshal(key)

	if err != nil {
		return err
	}

	// in a transaction, set the key if it matches the old version
	return db.executeWatch(func(tx *redis.Tx) error {
		var currentKey Key
		exists := true
		currentJSON, err := tx.HGet(ctx, keysID, key.Name).Bytes()

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
			pipe.HSet(ctx, keysID, key.Name, newJSON)
			expiration := db.getRedisExpiration()
			if expiration > 0 {
				pipe.Expire(ctx, keysID, expiration)
			}
			return nil
		})

		return err
	}, keysID)
}

func (db *RedisDocumentDB) DeleteDocument(docID string) error {
	_, err := db.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, getDocID(docID))
		pipe.Del(ctx, getKeys(docID))
		return nil
	})
	return err
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

// AddToken ...
func (db *RedisDocumentDB) AddToken(tokenID, docID, userID, permissions string, expirationSeconds int64, contents []byte) error {
	tokenID = getToken(tokenID)

	err := db.executeWatch(func(tx *redis.Tx) error {

		// check if token already exists
		m, err := tx.HGetAll(ctx, tokenID).Result()
		if err == nil && len(m) > 0 {
			return ErrExists
		}

		// check if document id exists
		if len(contents) != 0 {
			exists, err := tx.Exists(ctx, getDocID(docID)).Result()
			if err != nil {
				return err
			}
			if exists > 0 {
				return ErrConflict
			}
		}

		expiration := time.Until(time.Unix(expirationSeconds, 0))

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			// Create document if required.
			if len(contents) > 0 {
				pipe.Set(ctx, getDocID(docID), string(contents), db.getRedisExpiration())
			}

			pipe.HSet(ctx, tokenID, "userID", userID, "docID", docID,
				"permissions", permissions)
			pipe.Expire(ctx, tokenID, expiration)
			pipe.SAdd(ctx, "zwibbler-user:"+userID, tokenID)
			return nil
		})

		return err
	}, tokenID, getDocID(docID))

	return err

}

// Given a token, returns docID, userID, permissions. If it does not exist or is expired,
// the error is ErrMissing
func (db *RedisDocumentDB) GetToken(token string) (docID, userID, permissions string, err error) {
	m, err := db.rdb.HGetAll(ctx, getToken(token)).Result()

	if err == nil && len(m) > 0 {
		docID = m["docID"]
		userID = m["userID"]
		permissions = m["permissions"]
	} else if err == nil {
		err = ErrMissing
	}
	return
}

// If the user has any tokens, the permissions of all of them are updated.
func (db *RedisDocumentDB) UpdateUser(userID, permissions string) error {
	tokens, err := db.rdb.SMembers(ctx, "zwibbler-user:"+userID).Result()
	if err != nil {
		panic(err)
	}

	return db.executeWatch(func(tx *redis.Tx) error {
		// for each key, if it exists, then set the permissions

		for _, token := range tokens {
			exists, err := tx.Exists(ctx, token).Result()
			if err != nil {
				return err
			}

			if exists > 0 {
				log.Printf("Update permissions for token %s to %s", token, permissions)
				tx.HSet(ctx, token, "permissions", permissions)
			}
		}
		return nil
	}, tokens...)
}

func (db *RedisDocumentDB) runMaintenance() {
	log.Printf("Running Redis maintenance")
	// We used a zwibbler-user: key to quickly lookup the tokens for a given user.
	// But there is no way to expire a member of a set. So once a day, go through them
	// all and remove any that are all expired.

	// get a list of all the user keys
	var cursor uint64
	var userkeys []string
	for {
		var keys []string
		var err error
		keys, cursor, err = db.rdb.Scan(ctx, cursor, "zwibbler-user:*", 10).Result()
		if err != nil {
			panic(err)
		}
		userkeys = append(userkeys, keys...)
		if cursor == 0 {
			break
		}
	}

	// get the tokens of every user and put the into this structure
	userTokens := make(map[string][]string)
	results, err := db.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, key := range userkeys {
			pipe.SMembers(ctx, key)
		}
		return nil
	})

	if err != nil {
		panic(err)
	}

	for i, result := range results {
		slice, err := result.(*redis.StringSliceCmd).Result()
		if err != nil {
			panic(err)
		}
		userTokens[userkeys[i]] = slice
	}

	// build a unique list of tokens to check existence for
	have := make(map[string]bool)
	var checked []string
	for _, tokenList := range userTokens {
		for _, token := range tokenList {
			if !have[token] {
				have[token] = true
				checked = append(checked, token)
			}
		}
	}

	// check existence
	results, err = db.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, token := range checked {
			pipe.Exists(ctx, token)
		}
		return nil
	})

	if err != nil {
		panic(err)
	}

	// make have[token] = true if the key still exists
	have = make(map[string]bool)
	for i, result := range results {
		ok, err := result.(*redis.IntCmd).Result()
		if err != nil {
			panic(err)
		}

		if ok == 1 {
			have[checked[i]] = true
		}
	}

	_, err = db.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {

		// go through all the user->token set mappings, and remove entries that no longer
		// exist. If all tokens don't exist, then remove the zwibbler-user: entry.
		for userid, tokenList := range userTokens {
			removed := 0
			for _, token := range tokenList {
				if !have[token] {
					log.Printf("Remove from %s set: %s", userid, token)
					pipe.SRem(ctx, userid, token)
					removed++
				}
			}

			if removed == len(tokenList) {
				log.Printf("Remove key %s", userid)
				pipe.Del(ctx, userid)
			}
		}
		return nil
	})

	if err != nil {
		panic(err)
	}
}
