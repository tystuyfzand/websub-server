package store

import (
	"meow.tf/websub/model"
)

// Store defines an interface for stores to implement for data storage.
type Store interface {
	// All returns all subscriptions for the specified topic.
	All(topic string) ([]model.Subscription, error)

	// Add saves/adds a subscription to the store.
	Add(sub model.Subscription) error

	// Get retrieves a subscription given a topic and callback.
	Get(topic, callback string) (*model.Subscription, error)

	// Remove removes a subscription from the store.
	Remove(sub model.Subscription) error
}
