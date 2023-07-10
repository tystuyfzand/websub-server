package websub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jpillora/backoff"
	"github.com/mitchellh/mapstructure"
	"hash"
	"io"
	"log"
	"meow.tf/websub/handler"
	"meow.tf/websub/model"
	"meow.tf/websub/store"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	SHA1   = "sha1"
	SHA256 = "sha256"
	SHA384 = "sha384"
	SHA512 = "sha512"
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
	*handler.Handler

	client           *http.Client
	store            store.Store
	validator        Validator
	contentProvider  ContentProvider
	worker           Worker
	hasher           string
	url              string
	maxLease         time.Duration
	maxExtraDataSize int
	extraFields      map[string]interface{}
}

var (
	v           = validator.New()
	keyReplacer = strings.NewReplacer(".", "-")
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

// WithURL lets you set the hub url.
// By default, this is auto-detected on first request for ease of use, which is not recommended.
func WithURL(url string) Option {
	return func(h *Hub) {
		h.url = url
	}
}

// WithMaxLease lets you set the hub's max lease time.
// By default, this is 24 hours.
func WithMaxLease(maxLease time.Duration) Option {
	return func(h *Hub) {
		h.maxLease = maxLease
	}
}

// WithExtra allows you to define fields to allow.
// Fields will be pulled from the query parameters when subscriptions are registered.
func WithExtra(extraFields map[string]interface{}) Option {
	return func(h *Hub) {
		h.extraFields = extraFields
	}
}

// New creates a new WebSub Hub instance.
// store is required to store all the subscriptions.
func New(store store.Store, opts ...Option) *Hub {
	h := &Hub{
		Handler: handler.New(),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		store:            store,
		contentProvider:  HttpContent,
		hasher:           SHA256,
		maxLease:         24 * time.Hour,
		maxExtraDataSize: 1024 * 1024 * 5, // 5MB
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

// Hasher returns the current hasher
func (h *Hub) Hasher() string {
	return h.hasher
}

// URL returns the hub url
func (h *Hub) URL() string {
	return h.url
}

// ServeHTTP is a generic webserver handler for websub.
// It takes in "hub.mode" from the form, and passes it to the appropriate handlers.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hubMode := r.FormValue("hub.mode")

	if hubMode == "" {
		http.Error(w, "missing hub.mode parameter", http.StatusBadRequest)
		return
	}

	// If url is not set, set to something we can "guess"
	if h.url == "" {
		proto := "http"

		// Usually X-Forwarded cannot be trusted, but in this case it's the first request that defines it.
		// For our case, this simply sets the hub url via "auto detection".
		// it is STRONGLY advised to set the url using WithURL beforehand.
		if r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = r.Header.Get("X-Forwarded-Proto")
		}

		u := &url.URL{
			Scheme: proto,
			Host:   r.Host,
			Path:   r.RequestURI,
		}

		h.url = strings.TrimRight(u.String(), "/")
	}

	switch hubMode {
	case model.ModeSubscribe:
		var req model.SubscribeRequest

		if err := DecodeForm(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Support extra query parameters -> fields
		if h.extraFields != nil && len(h.extraFields) > 0 {
			err := h.DecodeExtraFields(r, &req)

			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		err := h.HandleSubscribe(req)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	case model.ModeUnsubscribe:
		var req model.UnsubscribeRequest

		if err := DecodeForm(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err := h.HandleUnsubscribe(req)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	case model.ModePublish:
		var req model.PublishRequest

		if err := DecodeForm(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err := h.HandlePublish(req)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "hub.mode not recognized", http.StatusBadRequest)
	}
}

// DecodeExtraFields is split from the handler to handle decoding of standard extra fields
// Exported for external handler implementations to use it
func (h *Hub) DecodeExtraFields(r *http.Request, req *model.SubscribeRequest) error {
	fields := make(map[string]interface{})

	if extraStr := r.FormValue("hub.extra"); extraStr != "" {
		// Support direct passing of JSON-encoded data as hub.extra
		if err := json.Unmarshal([]byte(extraStr), &fields); err != nil {
			return errors.New("invalid json data passed to hub.extra")
		}
	} else {
		// Alternatively, pull fields from the request form
		// Headers are also supported, such as "X-Some-Value" where key is Some-Value or some.value
		for key, _ := range h.extraFields {
			if val := r.FormValue(key); val != "" {
				fields[key] = val
			} else if val := r.Header.Get("x-" + keyReplacer.Replace(key)); val != "" {
				fields[key] = val
			}
		}
	}

	// Validate extra fields - this should always be set
	// Best practice is to enforce max string lengths, integers, etc
	// This prevents data from being too large to save.
	errFields := v.ValidateMap(fields, h.extraFields)

	if errFields != nil {
		return model.ValidationError{Fields: errFields}
	}

	// Validate data length in re-encoded JSON. We have an artificial cap of 5MB (which is pretty high...)
	b, err := json.Marshal(fields)

	if err != nil {
		return errors.New("validation of extra data failed")
	}

	if len(b) > h.maxExtraDataSize {
		return errors.New("extra data too large")
	}

	req.Extra = fields
	return nil
}

// HandleSubscribe handles a hub.mode=subscribe request.
func (h *Hub) HandleSubscribe(req model.SubscribeRequest) error {
	// validate for required fields
	if err := v.Struct(req); err != nil {
		return err
	}

	// Default lease
	leaseDuration := 240 * time.Hour

	if req.LeaseSeconds > 0 {
		if req.LeaseSeconds < 60 || time.Duration(req.LeaseSeconds)*time.Second > h.maxLease {
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

			return h.Verify(model.ModeDenied, sub)
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
			h.Call(&VerificationFailed{
				Subscription: sub,
				Error:        err,
			})
		} else {
			h.Call(&Verified{
				Subscription: sub,
			})
		}
	}(req.Mode, sub)

	return nil
}

// HandleUnsubscribe handles a hub.mode=unsubscribe
func (h *Hub) HandleUnsubscribe(req model.UnsubscribeRequest) error {
	// validate for required fields
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

			return h.Verify(model.ModeDenied, sub)
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

	if mode != model.ModeDenied {
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

	if mode == model.ModeDenied {
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

	if mode == model.ModeSubscribe {
		// Update the subscription and set it as verified
		// time.Now().Add(time.Duration(leaseSeconds) * time.Second), topic, callback
		err = h.store.Add(sub)
	} else if mode == model.ModeUnsubscribe {
		// Delete the subscription
		err = h.store.Remove(sub)
	}

	return err
}

// HandlePublish handles a request to publish from a publisher.
func (h *Hub) HandlePublish(req model.PublishRequest) error {
	if err := v.Struct(req); err != nil {
		return err
	}

	data, contentType, err := h.contentProvider(req.Topic)

	if err != nil {
		return err
	}

	return h.Publish(req.Topic, contentType, data)
}

// Publish queues responses to the worker for a publish.
func (h *Hub) Publish(topic, contentType string, data []byte) error {
	subs, err := h.store.All(topic)

	if err != nil {
		return err
	}

	h.Call(&Publish{
		Topic:       topic,
		ContentType: contentType,
		Data:        data,
	})

	hub := model.Hub{
		Hasher: h.hasher,
		URL:    h.url,
	}

	for _, sub := range subs {
		h.worker.Add(PublishJob{
			Hub:          hub,
			Subscription: sub,
			ContentType:  contentType,
			Data:         data,
		})
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
		mac := hmac.New(NewHasher(h.hasher), []byte(job.Subscription.Secret))
		mac.Write(job.Data)
		req.Header.Set("X-Hub-Signature", h.hasher+"="+hex.EncodeToString(mac.Sum(nil)))
	}

	req.Header.Set("Content-Type", job.ContentType)
	req.Header.Set("Link", fmt.Sprintf("<%s>; rel=\"hub\", <%s>; rel=\"self\"", h.url, job.Subscription.Topic))

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

// NewHasher takes a string and returns a hash.Hash based on type.
func NewHasher(hasher string) func() hash.Hash {
	switch hasher {
	case SHA1:
		return sha1.New
	case SHA256:
		return sha256.New
	case SHA384:
		return sha512.New384
	case SHA512:
		return sha512.New
	}

	panic("Invalid hasher type supplied")
}

// DecodeForm decodes a request form into a struct using the mapstructure package.
func DecodeForm(r *http.Request, dest interface{}) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName: "form",
		Result:  dest,
		// This hook is a trick to allow us to map from []string -> string in the case of elements.
		// This is only required because we're mapping from r.Form -> struct.
		DecodeHook: func(from reflect.Kind, to reflect.Kind, v interface{}) (interface{}, error) {
			if from == reflect.Slice && (to == reflect.String || to == reflect.Int) {
				switch s := v.(type) {
				case []string:
					if len(s) < 1 {
						return "", nil
					}

					// Switch statement seems wasteful here, but if we want to add uint/etc we can easily.
					switch to {
					case reflect.Int:
						return strconv.Atoi(s[0])
					}

					return s[0], nil
				}

				return v, nil
			}

			return v, nil
		},
	})

	if err != nil {
		return err
	}

	return decoder.Decode(r.Form)
}
