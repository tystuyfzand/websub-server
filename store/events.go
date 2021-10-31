package store

import "meow.tf/websub/model"

// Added represents an event when a subscription is added to the store.
type Added struct {
	Subscription model.Subscription
}

// Removed represents an event when a subscription is removed from the store.
type Removed struct {
	Subscription model.Subscription
}
