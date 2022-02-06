package bolt

import (
	"encoding/json"
	bolt "go.etcd.io/bbolt"
	"meow.tf/websub/handler"
	"meow.tf/websub/model"
	"meow.tf/websub/store"
	"time"
)

// New creates a new boltdb store.
// Bolt is fine for low throughput applications, though a full database should be used for performance.
func New(file string) (*Store, error) {
	db, err := bolt.Open(file, 0600, nil)

	if err != nil {
		return nil, err
	}

	s := &Store{
		Handler: handler.New(),
		db:      db,
	}

	go func() {
		t := time.NewTicker(60 * time.Second)
		for {
			<-t.C

			s.Cleanup()
		}
	}()

	return s, nil
}

// Store represents a boltdb backed store.
type Store struct {
	*handler.Handler
	db *bolt.DB
}

// Cleanup will loop all buckets and keys, expiring subscriptions that are old.
func (s *Store) Cleanup() {
	now := time.Now()

	s.db.Update(func(tx *bolt.Tx) error {
		return tx.ForEach(func(topic []byte, b *bolt.Bucket) error {
			return b.ForEach(func(k, v []byte) error {
				var s model.Subscription
				err := json.Unmarshal(v, &s)

				if err != nil {
					return err
				}

				if s.Expires.Before(now) {
					return b.Delete(k)
				}

				return nil
			})
		})
	})
}

// All retrieves all active subscriptions for a topic.
func (s *Store) All(topic string) ([]model.Subscription, error) {
	subscriptions := make([]model.Subscription, 0)

	now := time.Now()

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topic))

		if b == nil {
			return store.ErrNotFound
		}

		return b.ForEach(func(k, v []byte) error {
			var s model.Subscription

			err := json.Unmarshal(v, &s)

			if err != nil {
				return err
			}

			if now.Before(s.Expires) {
				subscriptions = append(subscriptions, s)
			}

			return err
		})
	})

	return subscriptions, err
}

// For returns the subscriptions for the specified callback
func (s *Store) For(callback string) ([]model.Subscription, error) {
	ret := make([]model.Subscription, 0)

	err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.ForEach(func(topic []byte, b *bolt.Bucket) error {
			return b.ForEach(func(k, v []byte) error {
				var s model.Subscription
				err := json.Unmarshal(v, &s)

				if err != nil {
					return err
				}

				if s.Callback != callback {
					return nil
				}

				ret = append(ret, s)

				return nil
			})
		})
	})

	return ret, err
}

// Add stores a subscription in the bucket for the specified topic.
func (s *Store) Add(sub model.Subscription) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(sub.Topic))

		if err != nil {
			return err
		}

		jsonB, err := json.Marshal(sub)

		if err != nil {
			return err
		}

		return b.Put([]byte(sub.Callback), jsonB)
	})

	if err == nil {
		s.Call(&store.Added{
			Subscription: sub,
		})
	}

	return err
}

// Get retrieves a subscription for the specified topic and callback.
func (s *Store) Get(topic, callback string) (*model.Subscription, error) {
	var sub *model.Subscription

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topic))

		if b == nil {
			return nil
		}

		data := b.Get([]byte(callback))

		if data == nil {
			return store.ErrNotFound
		}

		return json.Unmarshal(data, &sub)
	})

	return sub, err
}

// Remove removes a subscription from the bucket for the specified topic.
func (s *Store) Remove(sub model.Subscription) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sub.Topic))

		if b == nil {
			return store.ErrNotFound
		}

		return b.Delete([]byte(sub.Callback))
	})

	if err == nil {
		s.Call(&store.Removed{
			Subscription: sub,
		})
	}

	return err
}
