package model

import "time"

type Subscription struct {
    Topic string `json:"topic"`
    Callback string `json:"callback"`
    Secret string `json:"secret"`
    LeaseTime time.Duration `json:"lease"`
    Expires time.Time `json:"expires"`
    Reason error `json:"-"`
}
