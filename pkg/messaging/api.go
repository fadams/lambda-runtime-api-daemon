//
// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//

// Provides a JMS-like Connection/Session/Producer/Consumer/Message abstraction
// for AMQP 0.9.1 connections (to RabbitMQ, though may work with other brokers).
// It is a fairly thin wrapper around github.com/rabbitmq/amqp091-go
//
// By using JMS-like semantics the intention is to have an API that is already
// reasonably abstracted from the underlying messaging fabric, so that it should
// *hopefully* be a little simpler to build implementations for other fabrics.
//
// This implementation provides a mechanism to enable transparent reconnection
// to the messaging infrastructure, either where it is not immediately available
// when a client application starts or where a transient failure occurs, like
// a message broker restart, in which case this implementation will attempt
// to reconnect until a specified number or retries have been made.

package messaging

import (
	"context"
	"errors"
	log "github.com/sirupsen/logrus" // Structured logging
	"net/url"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	defaultHeartbeat          = 10 * time.Second
	defaultConnectionAttempts = 1
	defaultRetryDelay         = 2.0 * time.Second
	defaultCapacity           = 500 // Default consumer prefetch/capacity/QoS
)

// ErrUnsupported is returned when the NewConnection URL specifies a protocol
// that is currently not supported. Currently only AMQP 0.9.1 is supported
type ErrUnsupported string

func (e ErrUnsupported) Error() string {
	return "unsupported messaging protocol: " + string(e)
}

var errDeliveryNotInitialized = errors.New("delivery not initialized")

//------------------------------------------------------------------------------

// Holds optional fields used by the NewConnection factory.
// Use this struct and the functional options that reference it to pass
// variable options of different types to the NewConnection() factory,
// for example:
// connection, err := messaging.NewConnection("amqp://localhost:5672",
// messaging.Context(context.Background()))
type connectionOpts struct {
	ctx  context.Context
	name string
}

// Apply the supplied Context to the Connection to enable cancellation.
func Context(ctx context.Context) func(*connectionOpts) {
	return func(c *connectionOpts) {
		c.ctx = ctx
	}
}

// Apply a name to the Connection that may make log messages more meaningful,
// the default name is "Connection".
func ConnectionName(name string) func(*connectionOpts) {
	return func(c *connectionOpts) {
		c.name = name
	}
}

// Holds optional fields used by implementations of the Session interface.
// Use this struct and the functional options that reference it to pass variable
// options of different types to the Session() factory method of Connection,
// for example:
// session, err := connection.Session(messaging.AutoAck)
// session, err := connection.Session(messaging.AutoAck, messaging.Name("test"))
// https://commandcenter.blogspot.com/2014/01/self-referential-functions-and-design.html
type sessionOpts struct {
	name          string
	transactional bool // Not currently implemented
	autoAck       bool
}

// Apply a name to the Session that may make log messages more meaningful,
// the default name is "Session".
func SessionName(name string) func(*sessionOpts) {
	return func(s *sessionOpts) {
		s.name = name
	}
}

func Transactional(s *sessionOpts) {
	s.transactional = true
}

func AutoAck(s *sessionOpts) {
	s.autoAck = true
}

// Holds optional fields used by Session's Producer() and Consumer() factories.
// Use this struct and the functional options that reference it to pass variable
// options of different types to the Producer() and Consumer() factories.
// for example:
// producer, err = session.Producer("SomeQueue", messaging.Pool(4))
type pool struct {
	size    int // Number of connections in the pool
	bufSize int // Size of the chan buffer used to multiplex the connections.
}

// Number of connections that will be used in the pool (default is one)
func Pool(size int) func(*pool) {
	return func(p *pool) {
		p.size = size
	}
}

// Size of the chan buffer used to multiplex the connections.
func BufferSize(size int) func(*pool) {
	return func(p *pool) {
		p.bufSize = size
	}
}

//------------------------------------------------------------------------------

type Connection interface {
	// Creates a Session, a context for producing and consuming messages.
	Session(opts ...func(*sessionOpts)) (Session, error)
	Close()
	IsClosed() bool

	// CloseNotify registers a listener for error events due to connection
	// failures. Normally NewConnection will block the calling goroutine until
	// a connection is established and will return an error if the specified
	// connectionAttempts is exceeded. If, however, the connection succeeds it
	// is possible that the established connection may subsequently fail. If
	// that happens there is an attempt to transparently reconnect, but if that
	// reconnection fails after connectionAttempts it becomes necessary to
	// notify client applications and this, by necessity, is asynchronous which
	// is where this method fits in. Example usage:
	//
	//	ch := conn.CloseNotify()
	//	go func() {
	//		err := <-ch // Blocks goroutine until notified of an error
	//		// Do something with err
	//	}()
	//
	// or more succinctly as follow, though note that CloseNotify() creates and
	// returns the notification chan and registers it as a listener, so it is
	// important not to call it in a loop as each call creates a new chan.
	//
	//	go func() {
	//		err := <-conn.CloseNotify()
	//		// Do something with err
	//	}()
	//
	CloseNotify() <-chan error

	Start() // Starts (or restarts) a connection's delivery of incoming messages.
	Stop()  // Temporarily stops a connection's delivery of incoming messages.
}

type Session interface {
	// Creates a Message Producer to send messages to the specified destination.
	// Optional arguments configure a connection Pool and associated BufferSize.
	// producer, err = session.Producer("SomeQueue", messaging.Pool(4))
	Producer(address string, opts ...func(*pool)) (Producer, error)

	// Creates a Message Consumer for the specified destination.
	// Optional arguments configure a connection Pool and associated BufferSize.
	// consumer, err = session.Consumer("SomeQueue", messaging.Pool(4))
	Consumer(address string, opts ...func(*pool)) (Consumer, error)

	// Blocks waiting for Session to connect (or reconnect)
	Wait()
	Close()
	IsClosed() bool
}

type Producer interface {
	// Returns the Producer name, which represents to the name of the exchange
	// that the Producer will be publishing to.
	Name() string

	// Send Messages. If the configured pool size is greater than one this may
	// queue/buffer locally to optimise Message dispatch throughput.
	Send(msg Message)

	// Returns a channel to receive returned unroutable Messages.
	// Optional argument configures the channel's BufferSize.
	Return(opts ...func(*pool)) <-chan Message

	// Wait for any outstanding messages to be dispatched and close the
	// Return channel, if set. It is not necessary to call Close() if the
	// configured pool size is one and no Return receiver has been set.
	Close()
}

type Consumer interface {
	// Returns the Consumer name, which represents to the name of the queue
	// that the Consumer will be consuming from.
	Name() string

	// Get the Consumer Capacity, AKA prefetch size or QoS
	Capacity() int

	// Set the Consumer Capacity, AKA prefetch size or QoS
	SetCapacity(capacity int) error

	// Consume returns a go channel that Messages will be dispatched to.
	// Consume immediately starts delivering queued messages. Example usage:
	//
	// messages := consumer.Consume()
	// for message := range messages {
	//     <Do stuff with message here>
	// }
	Consume() <-chan Message
}

//------------------------------------------------------------------------------

// Creates an open connection.
// The supported URI scheme is based on the Pika Python RabbitMQ scheme:
// https://pika.readthedocs.io/en/stable/examples/using_urlparameters.html
// and currently supports heartbeat, connection_attempts and retry_delay
// URI query string values, for example:
// amqp://localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0
func NewConnection(uri string, opts ...func(*connectionOpts)) (Connection, error) {
	u, err := url.Parse(uri)
	if err != nil {
		log.Errorf("url.Parse() error: %s", err)
		return nil, err
	}

	// Iterate through any option arguments, which are implemented as functions.
	c := connectionOpts{ctx: context.Background(), name: "Connection"}
	for _, applyOptionTo := range opts {
		applyOptionTo(&c)
	}

	// Wait until after url.Parse() as we want to log the *redacted* URI.
	log.Infof("Creating %s with url: %s", c.name, u.Redacted())

	scheme := u.Scheme
	if strings.Contains(scheme, "amqp") { // TODO support other protocols
		// Process Connection URI Query parameters
		params := u.Query()

		heartbeat := defaultHeartbeat
		if hb, ok := params["heartbeat"]; ok && len(hb) > 0 {
			if heartbeat, err = time.ParseDuration(hb[0] + "s"); err != nil {
				heartbeat = defaultHeartbeat
			}
		}

		connectionAttempts := defaultConnectionAttempts
		if ca, ok := params["connection_attempts"]; ok && len(ca) > 0 {
			if value, err := strconv.ParseFloat(ca[0], 64); err == nil {
				connectionAttempts = int(value)
			}
		}

		retryDelay := defaultRetryDelay
		if rd, ok := params["retry_delay"]; ok && len(rd) > 0 {
			if retryDelay, err = time.ParseDuration(rd[0] + "s"); err != nil {
				retryDelay = defaultRetryDelay
			}
		}

		connection := &connectionAMQP091{
			ctx:                c.ctx,
			name:               c.name,
			uri:                uri,
			heartbeat:          heartbeat,
			connectionAttempts: connectionAttempts,
			retryDelay:         retryDelay,
		}

		err := connection.reconnect()
		return connection, err
	} else {
		return nil, ErrUnsupported(scheme)
	}
}

// Message. This is basically amqp.Delivery with some extra fields. Not sure
// why the amqp library chose to have different types for received messages
// (amqp.Delivery) and published messages (amqp.Publisher) or at least a common
// "base" given the extensive overlap between the two.
// This wrapper uses a common Message type for both publishers and consumers.
// The struct alignment has been adjusted for optimal field packing.
type Message struct {
	Acknowledger amqp.Acknowledger // the channel from which this delivery arrived

	Headers amqp.Table // Application or header exchange table

	// Properties
	ContentType     string    // MIME content type
	ContentEncoding string    // MIME content encoding
	CorrelationID   string    // application use - correlation identifier
	ReplyTo         string    // application use - address to reply to (ex: RPC)
	Expiration      string    // String value in milliseconds e.g. "1000"
	MessageID       string    // application use - message identifier
	Timestamp       time.Time // application use - message timestamp
	Type            string    // application use - message type name
	UserId          string    // application use - creating user
	AppId           string    // application use - creating application id

	ConsumerTag string // Valid only with Channel.Consume

	// Unlike AMQP 1.0 AMQP 0.9.1 doesn't have a Message subject, but it is a
	// useful concept as often we wish to publish messages to a generic producer
	// Node such as a topic exchange and have messages delivered based on their
	// subject. Although basic_publish allows one to achieve the same result it
	// is much more coupled with AMQP 0.9.1 (and AMQP 0.10) protocol details
	// than if it were a property of the Message, which is also more intuitive.
	Subject string

	Body []byte

	DeliveryTag uint64

	MessageCount uint32 // Valid only with Channel.Get
	Priority     uint8  // queue implementation use - 0 to 9

	Redelivered bool // Set if Message has been redelivered by broker

	// Setting true will map to DeliveryMode = 2, but Durable is clearer
	Durable bool

	// This flag tells the server how to react if a message cannot be routed to
	// a queue. Specifically, if mandatory is set and after running the bindings
	// the message was placed on zero queues then the message is returned to the
	// sender (with a basic.return). If mandatory had not been set under the
	// same circumstances the server would silently drop the message.
	// See also  https://www.rabbitmq.com/publishers.html
	Mandatory bool

	// For a message published with immediate set, if a matching queue has
	// ready consumers then one of them will have the message routed to it.
	// If the lucky consumer crashes before ack'ing receipt the message will be
	// requeued and/or delivered to other consumers on that queue (if there's
	// no crash the messaged is ack'ed and it's all done as per normal). If,
	// however, a matching queue has zero ready consumers the message will not
	// be enqueued for subsequent redelivery on from that queue. Only if all of
	// the matching queues have no ready consumers that the message is returned
	// to the sender (via basic.return).
	Immediate bool
}

type Multiple bool

// If a client attempts to call Acknowledge when AutoAck is set it will fail
// with "PRECONDITION_FAILED - unknown delivery tag 1". It is more "friendly"
// to instead simply ignore any explicit Acknowledge, so we set this dummy
// Acknowledger instead of the regular one when AutoAck is set.
type autoAcknowledger struct{}

func (a autoAcknowledger) Ack(tag uint64, multiple bool) error {
	return nil
}

func (a autoAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	return nil
}

func (a autoAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

// Acknowledge acknowledges the Message. By default this acknowledges this
// message and all prior unacknowledged messages on the same Session. This is
// consistent with JMS and useful for batch processing of deliveries.
// This behaviour may be changed by explicitly setting Multiple(false) e.g.
// message.Acknowledge(messaging.Multiple(false))
func (msg *Message) Acknowledge(opts ...Multiple) error {
	if msg.Acknowledger == nil {
		return errDeliveryNotInitialized
	}

	multiple := true // Default to acknowledge all prior unacknowledged messages
	if len(opts) == 1 && opts[0] == false {
		multiple = false
	}

	return msg.Acknowledger.Ack(msg.DeliveryTag, multiple)
}
