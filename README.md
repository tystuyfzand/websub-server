Go WebSub Server
================

A Go implementation of a [WebSub](https://www.w3.org/TR/websub/) server. It has been tested to pass every [WebSub Rocks! Hub test](https://websub.rocks/hub).

See `examples/main.go` for a basic example which uses boltdb and a simple publisher.

Importing:

```
go get meow.tf/websub
```

Features
--------

* Acts as a package to allow implementation using your favorite HTTP router (See Hub.ServeHTTP, as well as each Handle method for implementation in other routers that aren't stdlib compliant)
* Allows publishing of events directly from the hub, for use with custom implementations (such as a bridge for services that don't support hubs)
* Supports secrets and sha1, sha256, sha384, sha512 validation
* Supports `Content-Type` forwarding.
* Supports external workers to scale past what a single server can do (See [Workers](#Workers))

Stores
------

Specific stores can be implemented/used to store subscriptions and call them.

If you'd like to implement your own store, the following interface can be implemented:

```go
type Store interface {
	All(topic string) ([]model.Subscription, error)
	Add(sub model.Subscription) error
	Get(topic, callback string) (*model.Subscription, error)
	Remove(sub model.Subscription) error
}
```

### Memory

A memory-backed store. This store is cleared when the application is restarted.

### Bolt

A [boltdb/bbolt](https://github.com/etcd-io/bbolt) backed store which persists to disk.

Workers
-------

This hub system uses Workers to implement a system that can be infinitely scaled by adding other nodes/servers and workers which can pull off a queue.

By default, the worker pool is a basic channel + goroutine handler that goes through each request.

```go
type Worker interface {
    Add(f PublishJob)
    Start()
    Stop()
}
```

When implementing workers, pay attention to the fields. `ContentType` is used to say what the body content type is (required by the specification), and if subscription.secret is set it MUST be used to generate an `X-Hub-Signature` header.

Using it with your own Publisher
--------------------------------

If you wish to bypass the included `hub.mode=publish` handler, you can use the `Publish` function to publish your own data.

For example, if you're taking an event off some kind of queue/event subscriber:

```go
hub.Publish("https://example.com", "application/json", []byte("{}"))
```