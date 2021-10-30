package websub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/gorilla/schema"
	"github.com/jpillora/backoff"
	"hash"
	"io"
	"log"
	"meow.tf/websub/model"
	"meow.tf/websub/store"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"time"
)

const (
	ModeSubscribe   = "HandleSubscribe"
	ModeUnsubscribe = "HandleUnsubscribe"
	ModeDenied      = "denied"
	ModePublish     = "HandlePublish"
)

// Validator is a function to validate a subscription request.
// If error is not nil, hub.mode=verify will be called with the error.
type Validator func(model.Subscription) error

// ContentProvider is a function to extract content out of the specific content topic.
type ContentProvider func(topic string) ([]byte, string, error)

// Option represents a Hub option.
type Option func(h *Hub)

// Hub represents a WebSub hub.
type Hub struct {
	client          *http.Client
	store           store.Store
	validator       Validator
	contentProvider ContentProvider
	worker          Worker
	hasher          string
	MaxLease        time.Duration
}

var (
	v = validator.New()
)

// WithValidator sets the subscription validator.
func WithValidator(validator Validator) Option {
	return func(h *Hub) {
		h.validator = validator
	}
}

// WithContentProvider sets the content provider for external hub.mode=publish requests.
func WithContentProvider(provider ContentProvider) Option {
	return func(h *Hub) {
		h.contentProvider = provider
	}
}

// WithHasher lets you set other hmac hashers/types (like sha256, sha384, sha512, etc)
func WithHasher(hasher string) Option {
	return func(h *Hub) {
		h.hasher = hasher
	}
}

// WithWorker lets you set the worker used to distribute subscription responses.
// This can be done with any number of systems, such as Amazon SQS, Beanstalk, etc.
func WithWorker(worker Worker) Option {
	return func(h *Hub) {
		h.worker = worker
	}
}

// New creates a new WebSub Hub instance.
// store is required to store all of the subscriptions.
func New(store store.Store, opts ...Option) *Hub {
	h := &Hub{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		store:           store,
		contentProvider: HttpContent,
		hasher:          "sha256",
	}

	for _, opt := range opts {
		opt(h)
	}

	if h.worker == nil {
		h.worker = NewGoWorker(h, runtime.NumCPU())
		h.worker.Start()
	}

	return h
}

// ServeHTTP is a generic webserver handler for websub.
// It takes in "hub.mode" from the form, and passes it to the appropriate handlers.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hubMode := r.FormValue("hub.mode")

	if hubMode == "" {
		http.Error(w, "missing hub.mode parameter", http.StatusBadRequest)
		return
	}

	switch hubMode {
	case ModeSubscribe:
		err := h.HandleSubscribe(r)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	case ModeUnsubscribe:
		err := h.HandleUnsubscribe(r)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	case ModePublish:
		err := h.HandlePublish(r)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "hub.mode not recognized", http.StatusBadRequest)
	}
}

type subscribeRequest struct {
	Mode         string `form:"hub.mode" validate:"required"`
	Callback     string `form:"hub.callback" validate:"required"`
	Topic        string `form:"hub.topic" validate:"required"`
	Secret       string `form:"hub.secret" validate:"max:200"`
	LeaseSeconds int    `form:"hub.lease_seconds" validate:""`
}

// HandleSubscribe handles a hub.mode=subscribe request.
func (h *Hub) HandleSubscribe(r *http.Request) error {
	// validate for required fields
	req := &subscribeRequest{}

	if err := DecodeForm(r, &req); err != nil {
		return err
	}

	if err := v.Struct(req); err != nil {
		return err
	}

	// Default lease
	leaseDuration := 240 * time.Hour

	if req.LeaseSeconds > 0 {
		if req.LeaseSeconds < 60 || time.Duration(req.LeaseSeconds)*time.Second > h.MaxLease {
			return errors.New("invalid hub.lease_seconds value")
		} else {
			leaseDuration = time.Duration(req.LeaseSeconds) * time.Second
		}
	}

	sub := model.Subscription{
		Topic:    req.Topic,
		Callback: req.Callback,
		Secret:   req.Secret,
		Expires:  time.Now().Add(leaseDuration),
	}

	if h.validator != nil {
		err := h.validator(sub)

		if err != nil {
			sub.Reason = err

			return h.Verify(ModeDenied, sub)
		}
	}

	existingSub, err := h.store.Get(req.Topic, req.Callback)

	if existingSub != nil && err == nil {
		// Update existingSub instead.
		// TODO: Can Secret be updated?
		sub = *existingSub
		sub.Expires = time.Now().Add(leaseDuration)
	}

	go func(hubMode string, sub model.Subscription) {
		err := h.Verify(hubMode, sub)

		if err != nil {
			log.Println("Error:", err)
		}
	}(req.Mode, sub)

	return nil
}

type unsubscribeRequest struct {
	Mode     string `form:"hub.mode" validate:"required"`
	Callback string `form:"hub.callback" validate:"required,url"`
	Topic    string `form:"hub.topic" validate:"required"`
}

// HandleUnsubscribe handles a hub.mode=unsubscribe
func (h *Hub) HandleUnsubscribe(r *http.Request) error {
	// validate for required fields
	req := &unsubscribeRequest{}

	if err := DecodeForm(r, &req); err != nil {
		return err
	}

	if err := v.Struct(req); err != nil {
		return err
	}

	sub := model.Subscription{
		Topic:    req.Topic,
		Callback: req.Callback,
	}

	if h.validator != nil {
		err := h.validator(sub)

		if err != nil {
			sub.Reason = err

			return h.Verify(ModeDenied, sub)
		}
	}

	go func(hubMode string, sub model.Subscription) {
		err := h.Verify(hubMode, sub)

		if err != nil {
			log.Println("Error:", err)
		}
	}(req.Mode, sub)

	return nil
}

// Verify sends a response to a subscription model with the specified data.
// If the subscription failed, Reason can be set to send hub.reason in the callback.
func (h *Hub) Verify(mode string, sub model.Subscription) error {
	u, err := url.Parse(sub.Callback)

	if err != nil {
		return err
	}

	challenge := uuid.New().String()

	q := u.Query()
	q.Set("hub.mode", mode)
	q.Set("hub.topic", sub.Topic)

	if mode != ModeDenied {
		q.Set("hub.challenge", challenge)
		q.Set("hub.lease_seconds", strconv.Itoa(int(sub.LeaseTime/time.Second)))
	} else if sub.Reason != nil {
		q.Set("hub.reason", sub.Reason.Error())
	}

	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)

	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Go WebSub 1.0 ("+runtime.Version()+")")

	res, err := h.client.Do(req)

	if err != nil {
		return err
	}

	if res.StatusCode != 200 {
		// Uh oh!
		return errors.New("unexpected status code")
	}

	defer res.Body.Close()

	if mode == ModeDenied {
		io.Copy(io.Discard, res.Body)
		return nil
	}

	// Read max of challenge size bytes
	data := make([]byte, len(challenge))

	read, err := io.ReadFull(res.Body, data)

	if err != nil && err != io.ErrUnexpectedEOF {
		return err
	}

	data = data[0:read]

	if string(data) != challenge {
		// Nope.
		return errors.New(fmt.Sprint("verification: challenge did not match for "+u.Host+", expected: ", challenge, " actual: ", string(data)))
	}

	if mode == ModeSubscribe {
		// Update the subscription and set it as verified
		// time.Now().Add(time.Duration(leaseSeconds) * time.Second), topic, callback
		err = h.store.Add(sub)
	} else if mode == ModeUnsubscribe {
		// Delete the subscription
		err = h.store.Remove(sub)
	}

	return err
}

type publishRequest struct {
	URL string `form:"hub.url" validation:"required"`
}

// HandlePublish handles a request to publish from a publisher.
func (h *Hub) HandlePublish(r *http.Request) error {
	req := &publishRequest{}

	if err := DecodeForm(r, &req); err != nil {
		return err
	}

	data, contentType, err := h.contentProvider(req.URL)

	if err != nil {
		return err
	}

	return h.Publish(req.URL, contentType, data)
}

// Publish queues responses to the worker for a publish.
func (h *Hub) Publish(topic, contentType string, data []byte) error {
	subs, err := h.store.All(topic)

	if err != nil {
		return err
	}

	for _, sub := range subs {
		h.worker.Add(PublishJob{sub, contentType, data})
	}

	return nil
}

// callCallback sends a request to the specified URL with the publish data.
func (h *Hub) callCallback(job PublishJob) bool {
	req, err := http.NewRequest("POST", job.Subscription.Callback, bytes.NewReader(job.Data))

	if err != nil {
		return false
	}

	if job.Subscription.Secret != "" {
		mac := hmac.New(newHash(h.hasher), []byte(job.Subscription.Secret))
		mac.Write(job.Data)
		req.Header.Set("X-Hub-Signature", h.hasher+"="+hex.EncodeToString(mac.Sum(nil)))
	}

	req.Header.Set("Content-Type", job.ContentType)
	req.Header.Set("Link", fmt.Sprintf("<%s>; rel=\"hub\", <%s>; rel=\"self\"", "", job.Subscription.Topic))

	b := &backoff.Backoff{
		Min:    100 * time.Millisecond,
		Max:    10 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	var attempts int

	for {
		res, err := h.client.Do(req)

		if err == nil {
			res.Body.Close()

			if res.StatusCode >= 200 && res.StatusCode <= 299 {
				return true
			} else if res.StatusCode == http.StatusGone {
				h.store.Remove(job.Subscription)
				return false
			}
		}

		attempts++

		if attempts >= 3 {
			break
		}

		<-time.After(b.Duration())
	}

	return false
}

// newHash takes a string and returns a hash.Hash based on type.
func newHash(hasher string) func() hash.Hash {
	switch hasher {
	case "sha1":
		return sha1.New
	case "sha256":
		return sha256.New
	case "sha384":
		return sha512.New384
	case "sha512":
		return sha512.New
	}

	panic("Invalid hasher type supplied")
}

// DecodeForm decodes a request form into a struct using the gorilla schema package.
func DecodeForm(r *http.Request, dest interface{}) error {
	if err := r.ParseForm(); err != nil {
		return err
	}

	decoder := schema.NewDecoder()
	decoder.SetAliasTag("form")
	return decoder.Decode(dest, r.Form)
}
