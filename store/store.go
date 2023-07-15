package store

import (
	"context"
	"errors"
	"meow.tf/websub/model"
)

var (
	ErrNotFound = errors.New("subscription not found")
)

// Handler is an interface of handler.Handler, letting us embed it in Store.
type Handler interface {
	// Call calls all handlers with the given event. This is an internal method; use
	// with care.
	Call(ev interface{})

	// WaitFor blocks until there's an event. It's advised to use ChanFor instead,
	// as WaitFor may skip some events if it's not ran fast enough after the event
	// arrived.
	WaitFor(ctx context.Context, fn func(interface{}) bool) interface{}

	// ChanFor returns a channel that would receive all incoming events that match
	// the callback given. The cancel() function removes the handler and drops all
	// hanging goroutines.
	//
	// This method is more intended to be used as a filter. For a persistent event
	// channel, consider adding it directly as a handler with AddHandler.
	ChanFor(fn func(interface{}) bool) (out <-chan interface{}, cancel func())

	// AddHandler adds the handler, returning a function that would remove this
	// handler when called. A handler type is either a single-argument no-return
	// function or a channel.
	//
	// # Function
	//
	// A handler can be a function with a single argument that is the expected event
	// type. It must not have any returns or any other number of arguments.
	//
	//	// An example of a valid function handler.
	//	h.AddHandler(func(*gateway.MessageCreateEvent) {})
	//
	// # Channel
	//
	// A handler can also be a channel. The underlying type that the channel wraps
	// around will be the event type. As such, the type rules are the same as
	// function handlers.
	//
	// Keep in mind that the user must NOT close the channel. In fact, the channel
	// should not be closed at all. The caller function WILL PANIC if the channel is
	// closed!
	//
	// When the rm callback that is returned is called, it will also guarantee that
	// all blocking sends will be cancelled. This helps prevent dangling goroutines.
	//
	//	// An example of a valid channel handler.
	//	ch := make(chan *gateway.MessageCreateEvent)
	//	h.AddHandler(ch)
	AddHandler(handler interface{}) (rm func())

	// AddHandlerCheck adds the handler, but safe-guards reflect panics with a
	// recoverer, returning the error. Refer to AddHandler for more information.
	AddHandlerCheck(handler interface{}) (rm func(), err error)
}

// Store defines an interface for stores to implement for data storage.
type Store interface {
	Handler

	// All returns all subscriptions for the specified topic.
	All(topic string) ([]model.Subscription, error)

	// For returns the subscriptions for the specified callback
	For(callback string) ([]model.Subscription, error)

	// Add saves/adds a subscription to the store.
	Add(sub model.Subscription) error

	// Get retrieves a subscription given a topic and callback.
	Get(topic, callback string) (*model.Subscription, error)

	// Remove removes a subscription from the store.
	Remove(sub model.Subscription) error
}
