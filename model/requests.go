package model

// SubscribeRequest represents a form request for a subscribe.
type SubscribeRequest struct {
	Mode         string `form:"hub.mode" validate:"required"`
	Callback     string `form:"hub.callback" validate:"required"`
	Topic        string `form:"hub.topic" validate:"required"`
	Secret       string `form:"hub.secret" validate:"max=200"`
	LeaseSeconds int    `form:"hub.lease_seconds" validate:""`
	Extra        map[string]interface{}
}

// UnsubscribeRequest represents a form request for an unsubscribe.
type UnsubscribeRequest struct {
	Mode     string `form:"hub.mode" validate:"required"`
	Callback string `form:"hub.callback" validate:"required,url"`
	Topic    string `form:"hub.topic" validate:"required"`
}

// PublishRequest represents a form request for a publish.
type PublishRequest struct {
	Topic string `form:"hub.topic" validation:"required"`
}
