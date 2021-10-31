package bolt

import (
	"errors"
	"meow.tf/websub/handler"
	"meow.tf/websub/model"
	"meow.tf/websub/store"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("subscription not found")
)

// New creates a new memory store.
func New() (*Store, error) {
	s := &Store{
		Handler:   handler.New(),
		topicLock: &sync.RWMutex{},
		topics:    make(map[string][]model.Subscription),
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

// Store represents a memory backed store.
type Store struct {
	*handler.Handler
	topicLock *sync.RWMutex
	topics    map[string][]model.Subscription
}

// Cleanup will loop all buckets and keys, expiring subscriptions that are old.
func (s *Store) Cleanup() {
	now := time.Now()

	s.topicLock.RLock()

	remove := make([]model.Subscription, 0)

	for _, subscriptions := range s.topics {
		for _, sub := range subscriptions {
			if sub.Expires.Before(now) {
				remove = append(remove, sub)
			}
		}
	}

	s.topicLock.RUnlock()

	for _, sub := range remove {
		s.Remove(sub)
	}
}

// All retrieves all active subscriptions for a topic.
func (s *Store) All(topic string) ([]model.Subscription, error) {
	subscriptions := make([]model.Subscription, 0)

	now := time.Now()

	if subs, ok := s.topics[topic]; ok {
		for _, sub := range subs {
			if now.Before(sub.Expires) {
				continue
			}

			subscriptions = append(subscriptions, sub)
		}
	} else {
		return nil, ErrNotFound
	}

	return subscriptions, nil
}

// Add stores a subscription in the bucket for the specified topic.
func (s *Store) Add(sub model.Subscription) error {
	s.topicLock.Lock()

	if list, ok := s.topics[sub.Topic]; ok {
		list = append(list, sub)

		s.topics[sub.Topic] = list
		s.topicLock.Unlock()

		s.Call(&store.Added{Subscription: sub})
		return nil
	}

	s.topics[sub.Topic] = []model.Subscription{sub}
	s.topicLock.Unlock()

	s.Call(&store.Added{Subscription: sub})
	return nil
}

// Get retrieves a subscription for the specified topic and callback.
func (s *Store) Get(topic, callback string) (*model.Subscription, error) {
	s.topicLock.RLock()
	defer s.topicLock.RUnlock()

	subs := s.topics[topic]

	if subs == nil {
		return nil, ErrNotFound
	}

	for _, sub := range subs {
		if sub.Callback == callback {
			return &sub, nil
		}
	}

	return nil, ErrNotFound
}

// Remove removes a subscription from the bucket for the specified topic.
func (s *Store) Remove(sub model.Subscription) error {
	// Lock topics since we're doing a modification to a specific topic.
	s.topicLock.Lock()

	subs := s.topics[sub.Topic]

	var found bool

	for i, s := range subs {
		if s.Topic == sub.Topic && s.Callback == s.Callback {
			subs = append(subs[:i], subs[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		s.topicLock.Unlock()
		return ErrNotFound
	}

	s.topics[sub.Topic] = subs
	s.topicLock.Unlock()

	s.Call(&store.Removed{Subscription: sub})
	return nil
}
