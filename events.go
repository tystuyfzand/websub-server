package websub

import "meow.tf/websub/model"

// Verified is an event called when a subscription is successfully verified.
type Verified struct {
	Subscription model.Subscription
}

// VerificationFailed is an event called when a subscription fails to verify.
type VerificationFailed struct {
	Subscription model.Subscription
	Error        error
}

type Publish struct {
	Topic       string
	ContentType string
	Data        []byte
}
