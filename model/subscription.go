package model

import "time"

type Subscription struct {
	ID        int64                  `json:"id"`
	Topic     string                 `json:"topic"`
	Callback  string                 `json:"callback"`
	Secret    string                 `json:"secret"`
	LeaseTime time.Duration          `json:"lease"`
	Expires   time.Time              `json:"expires"`
	Extra     map[string]interface{} `json:"extra"`
	Reason    error                  `json:"-"`
}
