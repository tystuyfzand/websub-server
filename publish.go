package websub

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jpillora/backoff"
	"net/http"
	"time"
)

var (
	ErrPublishFailed    = errors.New("failed to publish after 3 attempts")
	ErrSubscriptionGone = errors.New("subscription callback gone")
)

func Notify(client *http.Client, job PublishJob) error {
	req, err := http.NewRequest(http.MethodPost, job.Subscription.Callback, bytes.NewReader(job.Data))

	if err != nil {
		return err
	}

	if job.Subscription.Secret != "" {
		mac := hmac.New(NewHasher(job.Hub.Hasher), []byte(job.Subscription.Secret))
		mac.Write(job.Data)
		req.Header.Set("X-Hub-Signature", job.Hub.Hasher+"="+hex.EncodeToString(mac.Sum(nil)))
	}

	req.Header.Set("Content-Type", job.ContentType)
	req.Header.Set("Link", fmt.Sprintf("<%s>; rel=\"hub\", <%s>; rel=\"self\"", job.Hub.URL, job.Subscription.Topic))

	b := &backoff.Backoff{
		Min:    100 * time.Millisecond,
		Max:    10 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	var attempts int

	for {
		res, err := client.Do(req)

		if err == nil {
			res.Body.Close()

			if res.StatusCode >= 200 && res.StatusCode <= 299 {
				return nil
			} else if res.StatusCode == http.StatusGone {
				return ErrSubscriptionGone
			}
		}

		attempts++

		if attempts >= 3 {
			break
		}

		<-time.After(b.Duration())
	}

	return ErrPublishFailed
}
