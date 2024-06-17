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
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus" // Structured logging
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	defaultLocale = "en_GB"
)

var errConnectionClosed = errors.New("connection has been closed")
var errConnectionCancelled = errors.New("connection has been cancelled")
var errSessionClosed = errors.New("session has been closed")

var dummyAck = autoAcknowledger{} // Dummy Acknowledger instance used for autoAck

//------------------------------------------------------------------------------

// Implements Connection interface
type connectionAMQP091 struct {
	ctx                context.Context
	connections        []*amqp.Connection // Pool of Connections
	err                error              // Final error state after all connectionAttempts
	name               string             // Primarily used for logging
	uri                string             // Connection URL to AMQP broker
	closeListeners     []chan error       // Used to notify of fatal close event
	connectionAttempts int
	heartbeat          time.Duration
	retryDelay         time.Duration
	m                  sync.Mutex
	// Will be atomically set to 1 if Connection has been started, 0 otherwise.
	started int32
}

// The reconnect() method lets us re-open the underlying connection
// without requiring a new Connection instance to be created, which allows
// reconnection to be made transparent to applications.
//
// The underlying connection is actually a *pool* of AMQP connections so that
// we may maximise throughput if required. The default pool size, however, is
// one and we lazily increase its size only on demand from Producer or Consumer
// instances. This is because although using multiple connections can increase
// raw throughput it also increases required system resources and, moreover,
// using multiple connections ***sacrifices strict FIFO ordering of messages***
// as messages are dispatched across all available connections. For many
// applications this is not important, but it must be considered before using
// Producers or Consumers with a pool size greater than one.
func (conn *connectionAMQP091) reconnect() error {
	conn.m.Lock()
	defer conn.m.Unlock()

	// The conn.err == nil test prevents reconnection after all
	// connectionAttempts have been tried. That condition is viewed as fatal
	// and applications should terminate. Connections in this state are "dead"
	// and at the very least a new Connection instance will need to be created
	// via NewConnection.
	if conn.IsClosed() && conn.err == nil {
		var newconn *amqp.Connection
		for i := 0; i < conn.connectionAttempts; i++ {
			if i == 0 {
				log.Infof("Opening %s", conn.name)
			} else {
				log.Infof("Opening %s retry %d", conn.name, i)
			}

			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#DialConfig
			newconn, conn.err = amqp.DialConfig(conn.uri, amqp.Config{
				Heartbeat: conn.heartbeat,
				Locale:    defaultLocale,
			})
			if conn.err == nil {
				break
			}

			// Cancellable Sleep, equivalent to time.Sleep(conn.retryDelay)
			select {
			case <-time.After(conn.retryDelay): // Timeout
			case <-conn.ctx.Done(): // Cancelled
				conn.err = errConnectionCancelled
				return conn.err
			}
		}

		// Close the current connection (if any) and set to new one
		if conn.connections != nil {
			log.Info("Closing stale Connection")
		}
		conn.Close()
		conn.connections = []*amqp.Connection{newconn}

		if conn.err == nil {
			log.Infof("%s established", conn.name)
		} else {
			// If all connectionAttempts have been tried and we still haven't
			// connected then notify.
			log.Infof("%s failed with %s", conn.name, conn.err)
			for _, c := range conn.closeListeners {
				c <- conn.err
				close(c)
			}
		}
	}
	return conn.err
}

// Creates a pool of AMQP connections of the specified size.
// This may be called multiple times such that the pool will grow to a new
// size larger than the current size, but will not shrink unless a
// reconnection event occurs.
func (conn *connectionAMQP091) createPool(size int) {
	if size > 1 {
		log.Infof("Creating Connection Pool with %d Connections", size)

		// The reconnect() call creates connections[0], the primary/main
		// connection in the pool, and we use that as a "sentinel" to determine
		// if connections have failed or are closed. In this call we create any
		//  additional connections that will be "managed" by the lifecycle of
		// connections[0]. If len(conn.connections) is less than the specified
		// size then additional connections are added to the pool otherwise no
		// new connections are added.
		initialSize := len(conn.connections)
		for i := initialSize; i < size; i++ {
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#DialConfig
			c, err := amqp.DialConfig(conn.uri, amqp.Config{
				Heartbeat: conn.heartbeat,
				Locale:    defaultLocale,
			})
			if err == nil {
				conn.connections = append(conn.connections, c)
			} else {
				log.Errorf("Error %s Creating Connection pool Connection instance: %d", err, i)
			}
		}
	}
}

// Creates a Session, a context for producing and consuming messages.
// When messaging.AutoAck is set, the server will acknowledge deliveries to
// this consumer prior to writing the delivery to the network, e.g.
// session, err := connection.Session(messaging.AutoAck)
func (conn *connectionAMQP091) Session(opts ...func(*sessionOpts)) (Session, error) {
	sess := &sessionAMQP091{
		connection: conn,
	}

	// Iterate through any option arguments, which are implemented as functions.
	s := sessionOpts{name: "Session"}
	for _, applyOptionTo := range opts {
		applyOptionTo(&s)
	}
	sess.sessionOpts = s

	// To implement a way for applications to wait until a Session is connected
	// we use the fact that closing go channels "broadcasts" that event to all
	// waiters. Unfortunately once closed the "barrier" channel can't be reused,
	// so we use a second buffered channel of size 1 to hold the barrier channel
	// and allow us to replace the closed barrier with a new one in a safe way.
	sess.barriers = make(chan chan struct{}, 1) // Single item barrier store
	sess.barriers <- make(chan struct{})        // Add barrier to barrier store

	err := sess.reconnect()
	return sess, err
}

// Close requests and waits for the response to close the AMQP connection.
func (conn *connectionAMQP091) Close() {
	// Iterate and close the underlying connections.
	for i := range conn.connections {
		if conn.connections[i] != nil {
			conn.connections[i].Close()
		}
	}
	if conn.connections != nil {
		log.Infof("%s closed", conn.name)
	}
	conn.connections = nil // Reset connections slice
}

// IsClosed returns true if the Connection is marked as closed, otherwise false.
func (conn *connectionAMQP091) IsClosed() bool {
	// connections[0] is the primary/main connection in the pool and we use that
	// as a "sentinel" to determine if connections have failed or are closed.
	return len(conn.connections) == 0 || conn.connections[0].IsClosed()
}

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
func (conn *connectionAMQP091) CloseNotify() <-chan error {
	conn.m.Lock()
	defer conn.m.Unlock()

	// Use buffer to ensure close event is sent even if receiver is blocked.
	ch := make(chan error, 1)
	conn.closeListeners = append(conn.closeListeners, ch)
	return ch
}

// Starts (or restarts) a connection's delivery of incoming messages.
// TODO actually use the value to stop/start delivery.
func (conn *connectionAMQP091) Start() {
	// Test value with: if atomic.LoadInt32(&conn.started) == 1
	atomic.StoreInt32(&conn.started, 1)
}

// Temporarily stops a connection's delivery of incoming messages.
// TODO actually use the value to stop/start delivery.
func (conn *connectionAMQP091) Stop() {
	atomic.StoreInt32(&conn.started, 0)
}

//------------------------------------------------------------------------------

// Implements Session interface
type sessionAMQP091 struct {
	connection  *connectionAMQP091 // To navigate back to parent Connection
	channels    []*amqp.Channel    // Pool of Channels
	barriers    chan chan struct{} // Barrier store - chan of chan with one entry.
	sessionOpts                    // Embed the session options
	m           sync.Mutex
	// A "hard close" via the Close() method, used to prevent reconnection.
	// Will be atomically set to 1 if session has been closed, 0 otherwise.
	closed int32
}

// The reconnect() method lets us re-open the underlying channel and connection
// without requiring a new Session and Connection instance to be created,
// which allows reconnection to be made transparent to applications.
//
// The underlying channel is actually a *pool* of AMQP channels so that we may
// maximise throughput if required. The default pool size, however, is one and
// we lazily increase its size only on demand from Producer or Consumer instances.
func (sess *sessionAMQP091) reconnect() error {
	// Don't attempt to reconnect an explicitly closed Session.
	if atomic.LoadInt32(&sess.closed) == 1 {
		return errSessionClosed
	}

	sess.m.Lock()
	defer sess.m.Unlock()

	if sess.IsClosed() {
		// When attempting to reconnect a session we first ensure that the
		// underlying connection has been established.
		if sess.connection.IsClosed() {
			log.Infof("Opening %s with closed Connection, reconnecting...", sess.name)
			err := sess.connection.reconnect()
			if err != nil {
				// Set closed to prevent reconnection when reconnect() errors.
				atomic.StoreInt32(&sess.closed, 1)
				return err
			}
		} else {
			log.Infof("Opening %s", sess.name)
		}

		// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Connection.Channel
		channel, err := sess.connection.connections[0].Channel()
		if err != nil {
			// Set closed to prevent reconnection when reconnect() errors.
			atomic.StoreInt32(&sess.closed, 1)
			return err
		}
		sess.channels = []*amqp.Channel{channel}
		go sess.closeListener() // closeListener blocks, so launch in goroutine

		// Broadcast reconnected state to all waiters.
		b := <-sess.barriers                 // Acquire barrier from barrier store.
		close(b)                             // Broadcast to all waiters.
		sess.barriers <- make(chan struct{}) // Add new barrier for future calls.
	}
	return nil
}

// Creates a pool of AMQP channels of the specified size.
// This may be called multiple times such that the pool will grow to a new
// size larger than the current size, but will not shrink unless a
// reconnection event occurs.
func (sess *sessionAMQP091) createPool(size int) {
	if size > 1 {
		// First ensure that connection pool is in place.
		sess.connection.createPool(size)

		log.Infof("Creating Session Pool with %d Channels", size)

		// The reconnect() call creates channels[0], the primary/main channel
		// in the pool, and we use that as a "sentinel" to determine if channels
		// have failed or are closed. In this call we create any additional
		// channels that will be "managed" by the lifecycle of channels[0].
		// If len(sess.channels) is less than the specified size then additional
		// channels are added to the pool otherwise no new channels are added.
		initialSize := len(sess.channels)
		for i := initialSize; i < size; i++ {
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Connection.Channel
			ch, err := sess.connection.connections[i].Channel()
			if err == nil {
				sess.channels = append(sess.channels, ch)
			} else {
				log.Errorf("Error %s Creating Session pool Channel instance: %d", err, i)
			}
		}
	}
}

// Blocks awaiting notification that the Session has reconnected.
func (sess *sessionAMQP091) Wait() {
	for sess.IsClosed() && atomic.LoadInt32(&sess.closed) == 0 {
		log.Debugf("Waiting for %s to reconnect", sess.name)

		// Wait for reconnect notification.
		b := <-sess.barriers // Acquire barrier from barrier store.
		sess.barriers <- b   // Release barrier.
		<-b                  // Wait on barrier.

		log.Debugf("%s has successfully reconnected", sess.name)
	}
}

// Listen on the underlying channel's "close notifier" go channel.
// Consuming from that go channel will block until the AMQP channel is closed,
// possibly due to a broker event, whereupon the blocked consume will return
// and an attempt will be made to reconnect the Session.
func (sess *sessionAMQP091) closeListener() {
	// Block on the primary/main AMQP channel's "close notifier" channel.
	// Use buffer to ensure close event is sent even if receiver is blocked.
	err := <-sess.channels[0].NotifyClose(make(chan *amqp.Error, 1))
	// Prevent reconnection when Close() is explicitly called.
	if atomic.LoadInt32(&sess.closed) == 0 {
		if err == nil {
			log.Infof("%s closed", sess.name)
		} else {
			log.Infof("%s closed with %s", sess.name, err)
		}

		sess.reconnect() // Will block until Connection and Session are connected.
	}
}

// Creates a Message Producer to send messages to the specified destination.
// Optional arguments configure a connection Pool and associated BufferSize.
// producer, err = session.Producer("SomeQueue", messaging.Pool(4))
func (sess *sessionAMQP091) Producer(address string, opts ...func(*pool)) (Producer, error) {
	producer := &producerAMQP091{}
	producer.session = sess
	producer.address = address

	// Iterate through any option arguments, which are implemented as functions.
	p := pool{size: 1, bufSize: 0}
	for _, applyOptionTo := range opts {
		applyOptionTo(&p)
	}

	if p.size < 1 { // Sanity check on pool size, set it to 1.
		p.size = 1
	}

	if p.bufSize == 0 { // Default buffer to pool size * 1000
		p.bufSize = p.size * 1000
	}

	producer.poolSize = p.size
	producer.bufSize = p.bufSize

	err := producer.open()
	if err == nil {
		producer.start()
	}
	return producer, err
}

// Creates a Message Consumer for the specified destination.
// Optional arguments configure a connection Pool and associated BufferSize.
// consumer, err = session.Consumer("SomeQueue", messaging.Pool(4))
func (sess *sessionAMQP091) Consumer(address string, opts ...func(*pool)) (Consumer, error) {
	consumer := &consumerAMQP091{}
	consumer.session = sess
	consumer.address = address

	// Iterate through any option arguments, which are implemented as functions.
	p := pool{size: 1}
	for _, applyOptionTo := range opts {
		applyOptionTo(&p)
	}

	if p.size < 1 { // Sanity check on pool size, set it to 1.
		p.size = 1
	}

	if p.bufSize == 0 { // Default buffer to pool size * 1000
		p.bufSize = p.size * 1000
	}

	consumer.poolSize = p.size
	consumer.bufSize = p.bufSize // Not currently used

	consumer.capacity = defaultCapacity // Set default capacity/prefetch to 500
	err := consumer.open()
	return consumer, err
}

// Cleanly closes the Session.
func (sess *sessionAMQP091) Close() {
	// Set closed flag to prevent reconnection when Close() is explicitly called.
	atomic.StoreInt32(&sess.closed, 1)

	// Iterate and close the underlying channels.
	for i := range sess.channels {
		if sess.channels[i] != nil {
			sess.channels[i].Close()
		}
	}
	if sess.channels != nil {
		log.Infof("%s closed", sess.name)
	}
	sess.channels = nil // Reset channels slice
}

// IsClosed returns true if the Session is marked as closed, otherwise false.
func (sess *sessionAMQP091) IsClosed() bool {
	// channels[0] is the primary/main channel in the pool and we use that
	// as a "sentinel" to determine if channels have failed or are closed.
	return len(sess.channels) == 0 || sess.channels[0].IsClosed()
}

//------------------------------------------------------------------------------

// The declare, bindings, node, subscribe, link and options types represent
// the schema used for the "address string" described in the comments for
// destinationAMQP091. The idea is to describe all of the AMQP configuration
// as a string as we want Producer() and Consumer() to be agnostic of the
// underlying messaging implementation, but we also want to be able to use
// as many capabilities as possible given an implementation agnostic API.
//
// The names of these types deliberately begin with a lower case following go's
// approach of using lowe case first letters to represent package local
// scope. The field names start upper case because they need to be "exportable"
// fields for json.Unmarshal to be able to deserialise them. It looks "odd"
// if viewed through the lens of other languages but it's how go works.
// https://go.dev/ref/spec#Exported_identifiers

// Describes fields from "x-declare" maps that are used to configure
// AMQP QueueDeclare and ExchangeDeclare settings.
type declare struct {
	Arguments    amqp.Table `json:"arguments,omitempty"`
	Queue        string     `json:"queue,omitempty"`
	Exchange     string     `json:"exchange,omitempty"`
	ExchangeType string     `json:"exchange-type,omitempty"`
	Passive      bool       `json:"passive,omitempty"`
	Internal     bool       `json:"internal,omitempty"`
	Durable      bool       `json:"durable,omitempty"`
	Exclusive    bool       `json:"exclusive,omitempty"`
	AutoDelete   bool       `json:"auto-delete,omitempty"`
}

// Describes bindings between exchanges and queues
type bindings struct {
	Arguments amqp.Table `json:"arguments,omitempty"`
	Exchange  string     `json:"exchange,omitempty"`
	Queue     string     `json:"queue,omitempty"`
	Key       string     `json:"key,omitempty"`
}

// Describes an AMQP "node" like a queue or exchange
type node struct {
	Type       string     `json:"type,omitempty"`
	Declare    declare    `json:"x-declare,omitempty"`
	Bindings   []bindings `json:"x-bindings,omitempty"`
	Durable    bool       `json:"durable,omitempty"`
	AutoDelete bool       `json:"auto-delete,omitempty"`
}

// The x-subscribe map of a link controls the exclusive and arguments
// fields of a subscription, which relates to the AMQP 0.9.1 basic.consume
// or the AMQP 0.10 message.subscribe protocol commands. This is mainly
// used to request an exclusive subscription. This prevents other
// subscribers from subscribing to the queue.
type subscribe struct {
	Arguments amqp.Table `json:"arguments,omitempty"`
	Exclusive bool       `json:"exclusive,omitempty"`
}

// A "link" allows concepts like subscription queues and cpmsumer exclusive
// and arguments fields to be described.
// Subscription queues are the queues associated with topic subscriptions
// where semantically consumers subscribe to topics, but in practice there
// is a queue, usually with a server generated name. The x-declare options
// of link allow properties of the subscription queue to be configured.
type link struct {
	Name        string     `json:"name,omitempty"`
	Reliability string     `json:"reliability,omitempty"` // Not implemented
	Declare     declare    `json:"x-declare,omitempty"`
	Subscribe   subscribe  `json:"x-subscribe,omitempty"`
	Bindings    []bindings `json:"x-bindings,omitempty"`
	Durable     bool       `json:"durable,omitempty"`
}

type options struct {
	Node node `json:"node,omitempty"`
	Link link `json:"link,omitempty"`
}

//------------------------------------------------------------------------------

type destinationAMQP091 struct {
	session  *sessionAMQP091 // To navigate back to parent Session
	options  *options
	address  string
	name     string
	subject  string
	poolSize int
	bufSize  int
	m        sync.Mutex
	closed   bool
}

// Parses an address string with the following format:
//
// <name> [ / <subject> ] [ ; <options> ]
//
// Where options is of the form: { <key> : <value>, ... }
//
// # And values may be numbers, strings, maps (dictionaries) or lists
//
// The options map permits the following parameters:
//
//	<name> [ / <subject> ] ; {
//	    node: {
//	        type: queue | topic,
//	        durable: True | False,
//	        auto-delete: True | False,
//	        x-declare: { ... <declare-overrides> ... },
//	        x-bindings: [<binding_1>, ... <binding_n>]
//	    },
//	    link: {
//	        name: <link-name>,
//	        durable: True | False,
//	        reliability: unreliable | at-most-once | at-least-once | exactly-once,
//	        x-declare: { ... <declare-overrides> ... },
//	        x-bindings: [<binding_1>, ... <binding_n>]
//	    }
//	}
//
// The node refers to the AMQP node e.g. a queue or exchange being referred
// to in the address, whereas the link allows configuration of a logical
// "subscriber queue" that will be created when the address node is an
// exchange such as, for example, news-service/sports.
//
// Bindings are specified as a map with the following options:
//
//	{
//	    exchange: <exchange>,
//	    queue: <queue>,
//	    key: <key>,
//	    arguments: <arguments>
//	}
//
// The x-declare map permits protocol specific keys and values to be
// specified when exchanges or queues are declared. These keys and
// values are passed through when creating a node or asserting facts
// about an existing node.
//
// For Producers the node implicitly defaults to a topic and for Consumers
// it implicitly defaults to a queue, but this may be overridden by setting
// exchange or queue in the x-declare object or by setting node type to
// queue or topic.
//
// Examples:
// myqueue; {"node": {"x-declare": {"durable": true, "exclusive": true, "auto-delete": true}}}'
// myqueue; {"node": {"x-declare": {"exchange": "test-headers", "exchange-type": "headers", "durable": true, "auto-delete": true}}}
//
// myqueue; {"node": {"x-declare": {"durable": true, "auto-delete": true}, "x-bindings": [{"exchange": "amq.match", "queue": "myqueue", "key": "data1", "arguments": {"x-match": "all", "data-service": "amqp-delivery", "item-owner": "Sauron"}}]}}
//
// myqueue; {"node": {"durable": true, "x-bindings": [{"exchange": "amq.match", "queue": "myqueue", "key": "data1", "arguments": {"x-match": "all", "data-service": "amqp-delivery", "item-owner": "Sauron"}}, {"exchange": "amq.match", "queue": "myqueue", "key": "data2", "arguments": {"x-match": "all", "data-service": "amqp-delivery", "item-owner": "Gandalf"}}]}}
//
// myqueue; {"node": {"x-declare": {"durable": true, "auto-delete": false}}, "link": {"x-subscribe": {"exclusive": true}}}'
//
// news-service/sports
//
// news-service/sports; {"node": {"x-declare": {"exchange": "news-service", "exchange-type": "topic"}}}
//
// news-service/sports; {"node": {"x-declare": {"exchange": "news-service", "exchange-type": "topic", "auto-delete": true}}, "link": {"x-declare": {"queue": "news-queue", "exclusive": false}}}
//
// Topic exchanges can be declared by message producers as follows:
// ; {"node": {"x-declare": {"exchange": "news-service", "exchange-type": "topic"}}}
//
// For the case where the address comprises just the options string
// the leading semicolon is optional e.g. the following is also valid:
// {"node": {"x-declare": {"exchange": "news-service", "exchange-type": "topic"}}}
func (dst *destinationAMQP091) parseAddress(address string) error {
	// Explicitly set default options values. Could have just set the non-zero,
	// non-empty or true values for brevity, but it's useful to document the
	// complete options structure.
	dst.options = &options{
		Node: node{
			Type: "",
			// Default values taken from exchange_declare, queue_declare, queue_bind
			Declare: declare{
				Arguments:    amqp.Table{},
				Queue:        "",
				Exchange:     "",
				ExchangeType: "direct",
				Passive:      false,
				Internal:     false,
				Durable:      false,
				Exclusive:    false,
				AutoDelete:   false,
			},
			Bindings:   []bindings{},
			Durable:    false,
			AutoDelete: false,
		},
		Link: link{
			Name:        "",
			Reliability: "",
			// Defaults for subscription queues (exclusive and autodelete True)
			Declare: declare{
				Arguments:    amqp.Table{},
				Queue:        "",
				Exchange:     "",
				ExchangeType: "",
				Passive:      false,
				Internal:     false,
				Durable:      false,
				Exclusive:    true,
				AutoDelete:   true,
			},
			Subscribe: subscribe{
				Arguments: amqp.Table{},
				Exclusive: false,
			},
			Bindings: []bindings{},
			Durable:  false,
		},
	}

	// Given an address of the form: <name> [ / <subject> ] [ ; <options> ]
	// extract the name and subject parts by splitting the address on semicolon
	kv := strings.Split(address, ";")
	optionsString := "{}"
	if len(kv) == 2 {
		optionsString = kv[1]
	}

	kv = strings.Split(kv[0], "/")
	dst.subject = ""
	if len(kv) == 2 {
		dst.subject = strings.TrimSpace(kv[1])
	}
	dst.name = strings.TrimSpace(kv[0])

	// Handle edge case where address comprises just the options string.
	if len(dst.name) >= 2 && string(dst.name[0]) == "{" {
		optionsString = dst.name
		dst.name = ""
	}

	// Unmarshall the options string into the Options struct.
	if err := json.Unmarshal([]byte(optionsString), dst.options); err != nil {
		// The err type being returned is *json.SyntaxError. It might be
		// possible to use err.Offset to create a more useful error message
		// fmt.Println((err.(*json.SyntaxError)).Offset)
		log.Errorf("json.Unmarshal error: %s from unmarshalling: %s", err, optionsString)
		return err
	}

	declare := &dst.options.Node.Declare // Get reference to Declare
	NodeType := dst.options.Node.Type
	if dst.name == "" {
		// Handle case where the address comprises just the options string.
		if NodeType == "queue" {
			dst.name = declare.Queue
		}
		if NodeType == "topic" {
			dst.name = declare.Exchange
		}
		// Handle edge cases where node type is not explicitly set
		if dst.name == "" {
			dst.name = declare.Exchange
		}
		if dst.name == "" {
			dst.name = declare.Queue
		}
	} else {
		// If node type is set then set queue or exchange name in
		// declare if not already explicitly set in x-declare
		if NodeType == "queue" && declare.Queue == "" {
			declare.Queue = dst.name
		}
		if NodeType == "topic" && declare.Exchange == "" {
			declare.Exchange = dst.name
		}
	}

	// We can set durable and auto-delete on node as a shortcut if we don't
	// need any other declare overrides, but we read those values from the
	// Declare structure later so reflect those values in the Declare structure.
	// Note don't copy the node Durable/AutoDelete values only set if explicitly
	// true as we don't want to overwrite values already set in Declare.
	if dst.options.Node.Durable {
		declare.Durable = true
	}
	if dst.options.Node.AutoDelete {
		declare.AutoDelete = true
	}

	// By default, to unmarshall JSON into an interface value, json.Unmarshal
	// maps JSON numbers to float64 https://pkg.go.dev/encoding/json#Unmarshal
	// The Arguments fields for Declare, Subscribe etc. are fairly unstructured
	// and are generally quite broker specific so really need to be a Table
	// (map[string]interface{}). Unfortunately, the numeric types for things
	// like "x-priority": 10, "x-max-length": 10 etc. are expected to be
	// integers and the broker will send PRECONDITION_FAILED if set as double.
	// As a workaround we coerce numeric values stored in Arguments into ints.
	// This aproach if fairly simple and obvious, but lacks some finesse and
	// in particular if any Argument numeric values *need* to be represented
	// as float64 it will fail. TODO a more complete approach might be a custom
	// Unmarshal that represents values with a period e.g. 10.0 as float64 and
	// those without e.g. 10 as int.
	coerceJSONNumbersToInt := func(t amqp.Table) {
		for k, v := range t {
			if value, ok := v.(float64); ok {
				t[k] = int(value)
			}
		}
	}

	coerceJSONNumbersToInt(dst.options.Node.Declare.Arguments)
	coerceJSONNumbersToInt(dst.options.Link.Declare.Arguments)
	coerceJSONNumbersToInt(dst.options.Link.Subscribe.Arguments)

	/*fmt.Println("optionsString")
	fmt.Println(optionsString)
	fmt.Println()

	fmt.Println("options")
	fmt.Println(dst.options)
	fmt.Println()*/

	return nil
}

// reconnect() blocks waiting for Connection and Session to reconnect.
func (dst *destinationAMQP091) reconnect() {
	dst.session.Wait()
	dst.closed = false
}

//------------------------------------------------------------------------------

// Implements Producer interface
type producerAMQP091 struct {
	queue   chan *Message
	returns chan Message
	waiter  chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	destinationAMQP091
}

// Start any dispatcher goroutines required to service the connection pool.
func (prod *producerAMQP091) start() {
	// Context used to cancel pool dispatchers when Close() called.
	prod.ctx, prod.cancel = context.WithCancel(prod.session.connection.ctx)

	// If the producer pool size is greater than one launch goroutines to read
	// messages from an internal channel and distribute to the pool. If the
	// producer pool size is one we don't use this approach and just delegate
	// directly from Send() to publish().
	if prod.queue == nil && prod.poolSize > 1 {
		// Create a queue/buffer used to multiplex Messages from the Send()
		// method across the available pool of AMQP Channels thence Connections.
		log.Infof("Creating Producer pool queue of size %d", prod.bufSize)
		prod.queue = make(chan *Message, prod.bufSize)
	}

	for i := 0; i < prod.poolSize; i++ {
		i := i
		go func() {
			logmsg := "Producer dispatcher"
			if prod.poolSize > 1 {
				logmsg = fmt.Sprintf("Producer pool dispatcher %d", i)
			}

			log.Infof("Starting %s", logmsg)

			// Use buffer to ensure close event is sent even if receiver is blocked.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.NotifyClose
			cl := prod.session.channels[i].NotifyClose(make(chan *amqp.Error, 1))
			var err *amqp.Error
		loop:
			for {
				select {
				// When prod.poolSize is 1 Send() will delegate directly to
				// publish() and prod.queue will be nil, but we still run a
				// dispatcher goroutine to get notified of channel close events.
				case message := <-prod.queue:
					prod.publish(i, *message)
					// Waiter will only be non nil when Close() is called.
					if prod.waiter != nil && len(prod.queue) == 0 {
						prod.waiter <- struct{}{} // Signal waiter
						break loop
					}
				case err = <-cl:
					prod.closed = true
					break loop
				case <-prod.ctx.Done():
					break loop
				}
			}

			if err == nil {
				log.Infof("Stopping %s", logmsg)
			} else {
				log.Infof("Stopping %s with %s", logmsg, err)
			}
		}()
	}
}

// Start any dispatcher goroutines required to service the Return receivers.
func (prod *producerAMQP091) startReceivers() {
	// Return immediately if Return() hasn't been called on this Producer.
	if prod.returns == nil {
		return
	}

	// We use a WaitGroup to allow us to block until the all the dispatchers
	// are actually running and their Return listeners are available to receive
	// message returns before returning from startReceivers(). This in turn
	// means that the Producer Return() method will block until the returned
	// channel is available to receive Messages.
	var wg sync.WaitGroup

	// Launch goroutines to read Returns off the channel returned by Return.
	// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Return
	for i := 0; i < prod.poolSize; i++ {
		wg.Add(1)
		i := i
		go func() {
			log.Infof("Starting Producer Returns dispatcher %d", i)
			// Use buffer to ensure close event is sent even if receiver is blocked.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.NotifyClose
			cl := prod.session.channels[i].NotifyClose(make(chan *amqp.Error, 1))

			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.NotifyReturn
			ret := prod.session.channels[i].NotifyReturn(make(chan amqp.Return, 1))
			var err *amqp.Error

			// Signal that the dispatcher is running and the NotifyReturn is
			// available to receive message returns.
			wg.Done()
		loop:
			for {
				select {
				case err = <-cl:
					break loop
				case <-prod.ctx.Done():
					break loop
				case msg := <-ret:
					// "Adapt" amqp.Delivery to messaging.Message.
					message := Message{
						Headers:         msg.Headers,
						ContentType:     msg.ContentType,
						ContentEncoding: msg.ContentEncoding,
						CorrelationID:   msg.CorrelationId,
						ReplyTo:         msg.ReplyTo,
						Expiration:      msg.Expiration,
						MessageID:       msg.MessageId,
						Timestamp:       msg.Timestamp,
						Type:            msg.Type,
						UserId:          msg.UserId,
						AppId:           msg.AppId,

						Subject: msg.RoutingKey,

						Body:     msg.Body,
						Priority: msg.Priority,
					}

					if msg.DeliveryMode == 2 {
						message.Durable = true
					}

					select {
					case prod.returns <- message:
						// Send Message to returns channel
					case err = <-cl:
						break loop
					case <-prod.ctx.Done():
						break loop
					default:
						// If prod.returns channel is full or not being consumed
						// sending to it would block, which can result in
						// deadlocking the underlying AMQP library. Rather than
						// risk that we have a do nothing default, noting that
						// Returns will then be transparently discarded. To
						// avoid that scenario a sufficiently large BufferSize
						// should be specified when calling Return()
					}
				}
			}

			if err == nil {
				log.Infof("Stopping Producer Returns dispatcher %d", i)
			} else {
				log.Infof("Stopping Producer Returns dispatcher %d with %s", i, err)
			}
		}()
	}

	// Block until all dispatchers are running and their NotifyReturns are
	// available to receive message returns.
	wg.Wait()
}

// Open parses the "address string", which is a JSON description of the
// broker topology, the schema for which is described in the comments for
// Destination parseAddress(). Using an address string in this way provides a
// a way to abstract the underlying messaging implementation details like the
// AMQP exchange_declare, queue_declare, etc. where those details are instead
// represented by the address string schema.
func (prod *producerAMQP091) open() error {
	if prod.address == "" {
		log.Info("Creating Producer")
	} else {
		log.Infof("Creating Producer with address: %s", prod.address)
	}

	prod.session.createPool(prod.poolSize)
	err := prod.parseAddress(prod.address)
	if err == nil {
		declare := &prod.options.Node.Declare // Get reference to Declare

		// Check if an exchange with the name of this destination exists.
		if prod.name != "" {
			// Use temporary channel as the channel gets closed on an exception.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Connection.Channel
			tempCh, err := prod.session.connection.connections[0].Channel()
			if err != nil {
				return err
			}

			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.ExchangeDeclarePassive
			if err = tempCh.ExchangeDeclarePassive(
				prod.name,            // name
				declare.ExchangeType, // kind
				declare.Durable,      // durable
				declare.AutoDelete,   // autoDelete
				declare.Internal,     // internal
				false,                // noWait
				nil,                  // arguments
			); err != nil {
				brokerException := err.(*amqp.Error)
				// If 404 NOT_FOUND the specified exchange doesn't exist.
				if brokerException.Code == 404 {
					// If no exchange declared assume default direct exchange.
					if prod.name != declare.Exchange {
						prod.subject = prod.name
						prod.name = "" // Set exchange to default direct.
					}
				}
			}
			tempCh.Close()
		}

		if declare.Exchange != "" {
			// Exchange declare.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.ExchangeDeclare
			prod.session.channels[0].ExchangeDeclare(
				declare.Exchange,     // name
				declare.ExchangeType, // kind
				declare.Durable,      // durable
				declare.AutoDelete,   // autoDelete
				declare.Internal,     // internal
				false,                // noWait
				declare.Arguments,    // arguments
			)
		}
	}

	// fmt.Println("prod.name " + prod.name)
	// fmt.Println("prod.subject " + prod.subject)

	return err
}

func (prod *producerAMQP091) enqueue(msg Message) {
	prod.queue <- &msg
}

func (prod *producerAMQP091) publish(ch int, msg Message) {
	// If the Message Subject is set use that as the routingKey, otherwise use
	// the Producer subject parsed from address string.
	routingKey := prod.subject
	subject := msg.Subject
	if subject != "" {
		routingKey = subject
	}

	// If message.expiration is set to an invalid value (like a
	// non-numeric or a negative value) we "clamp" it to a "0"
	clampedExpiration := ""
	if msg.Expiration != "" {
		if value, err := strconv.ParseFloat(msg.Expiration, 64); err == nil {
			intValue := int(value)
			if intValue > 0 {
				clampedExpiration = strconv.Itoa(intValue)
			} else {
				clampedExpiration = "0"
			}
		} else {
			clampedExpiration = "0"
		}
	}

	// Use Durable flag from Message to set the appropriate DeliveryMode.
	var deliveryMode uint8 = 1
	if msg.Durable {
		deliveryMode = 2
	}

	// Directly use PublishWithDeferredConfirmWithContext https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.PublishWithDeferredConfirmWithContext
	// as Publish is marked Deprecated and PublishWithContext immediately
	// delegates to PublishWithDeferredConfirmWithContext anyway.
	// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.PublishWithContext
	prod.session.channels[ch].PublishWithDeferredConfirmWithContext(
		prod.ctx,
		prod.name,     // exchange
		routingKey,    // routing key
		msg.Mandatory, // mandatory
		msg.Immediate, // immediate
		amqp.Publishing{
			Headers:         msg.Headers,
			ContentType:     msg.ContentType,
			ContentEncoding: msg.ContentEncoding,
			DeliveryMode:    deliveryMode,
			Priority:        msg.Priority,
			CorrelationId:   msg.CorrelationID,
			ReplyTo:         msg.ReplyTo,
			Expiration:      clampedExpiration,
			MessageId:       msg.MessageID,
			Timestamp:       msg.Timestamp,
			Type:            msg.Type,
			UserId:          msg.UserId,
			AppId:           msg.AppId,
			Body:            msg.Body,
		})
}

// Returns the Producer name, which represents to the name of the exchange
// that the Producer will be publishing to.
func (prod *producerAMQP091) Name() string {
	return prod.name
}

// Send publishes a Message from the client to an exchange on the server.
// Message is passed by value partly to be consistent with the various Publish
// methods from the underlying amqp091-go library that is doing the heavy
// lifting, but also as it turns out to be marginally faster.
// https://austinmorlan.com/posts/pass_by_value_vs_pointer/
// https://medium.com/@meeusdylan/when-to-use-pointers-in-go-44c15fe04eac
func (prod *producerAMQP091) Send(msg Message) {
	// If the Session is closed attempt to reconnect.
	//for prod.session.IsClosed() {
	for prod.closed {
		// This lock prevents concurrent calls to Send() from all attempting to
		// reconnect simultaneously if the Session is closed.
		prod.m.Lock()
		//if prod.session.IsClosed() {
		if prod.closed {
			prod.reconnect() // Will block until Connection and Session are connected.
			prod.open()
			prod.start()
			prod.startReceivers()

			// Brokers can occasionally glitch on startup, so delay a little
			// before resuming publishing. Using a delay here is a little hacky,
			// but there isn't a reliable way to detect the glitching and the
			// delay is faster, cleaner and less obtrusive than the reconnect
			// that would otherwise occur if the glitch closed the Session.
			time.Sleep(1 * time.Second)
		}
		prod.m.Unlock()

		// fmt.Println(prod.session.IsClosed())
		// TODO if prod.session.IsClosed() *after* session.Wait() then it means
		// Close() has been called so errSessionClosed should be returned.
		// Holding fire on this for now as Send() with pool > 1 sends
		// to an internal channel with a pool of goroutines doing the actual
		// PublishWithDeferredConfirmWithContext call so need to think whether
		// checking for prod.session.IsClosed() after prod.open() is useful.
	}

	// If the producer pool size is 1 (the default) then delegate directly to
	// publish() otherwise enqueue message to an internal go channel to be
	// dispatched by the pool handler goroutines across multiple AMQP channels.
	// Note that delegating to a separate enqueue() method is, perhaps
	// counterintuitively, faster than directly calling prod.queue <- &msg
	// here. The reason is because the compiler performs escape analysis
	// https://en.wikipedia.org/wiki/Escape_analysis
	// https://medium.com/a-journey-with-go/go-introduction-to-the-escape-analysis-f7610174e890
	// and including &msg in this method likely persuades it to use the heap
	// irrespective of whether the pool size is 1 whereas delegating to enqueue
	// causes the compiler to use the stack for the pool size == 1 case.
	if prod.poolSize == 1 {
		prod.publish(0, msg)
	} else {
		prod.enqueue(msg)
	}
}

// Return returns a go channel that undeliverable Messages will be returned to.
// These may be asynchronously sent from the broker via the basic.return method
// when a publish is undeliverable and either the mandatory or immediate flags
// are set on the published Message.
// An optional argument configures the channel's BufferSize which should be set
// large enough to avoid the channel blocking, as that will result in Returns
// being (deliberately) discarded to avoid the underlying AMQP library from
// deadlocking, which would block sends and also make reconnection unresponsive.
//
// returns := producer.Return() // or producer.Return(messaging.BufferSize(500000))
//
//	for message := range returns {
//	    <Do stuff with returned message here>
//	}
func (prod *producerAMQP091) Return(opts ...func(*pool)) <-chan Message {
	prod.m.Lock() // Avoid potential race on creating prod.returns chan
	defer prod.m.Unlock()

	if prod.returns == nil {
		// Iterate through any option arguments, which are implemented as functions.
		p := pool{}
		for _, applyOptionTo := range opts {
			applyOptionTo(&p)
		}

		if p.bufSize == 0 { // Default buffer to pool size * 5000
			p.bufSize = prod.poolSize * 5000
		}

		log.Infof("Creating Return queue of size %d", p.bufSize)
		prod.returns = make(chan Message, p.bufSize)
		prod.startReceivers()
	}

	return prod.returns
}

// Wait for any outstanding messages to be dispatched and close the
// Return channel, if set. It is not necessary to call Close() if the
// configured pool size is one and no Return receiver has been set.
func (prod *producerAMQP091) Close() {
	prod.m.Lock()
	defer prod.m.Unlock()

	// Wait for any outstanding Messages queued by the pool to be published.
	if prod.poolSize > 1 && len(prod.queue) > 0 {
		prod.waiter = make(chan struct{}, prod.poolSize)
		<-prod.waiter
	}
	prod.cancel() // Cancel all pool dispatchers.
}

//------------------------------------------------------------------------------

// Implements Consumer interface
type consumerAMQP091 struct {
	queue chan Message
	destinationAMQP091
	capacity int
}

// Starts any dispatcher goroutines that may be required to service the
// connection pool or additional registered receivers.
func (cons *consumerAMQP091) start() {
	// Return immediately if Consume() hasn't been called on this Consumer.
	if cons.queue == nil {
		return
	}

	// We use a WaitGroup to allow us to block until the all the dispatchers
	// are actually running and their Consumers are available to receive
	// message deliveries before returning from start(). This in turn means
	// that the Consumer Consume() method will block until the returned channel
	// is available to receive Messages.
	var wg sync.WaitGroup

	// Launch goroutines to read Deliveries off the channel returned by Consume.
	// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Delivery
	// This acts as an "adapter" to convert amqp.Delivery to messaging.Message,
	// which is similar, but intended to be a generic Message format and also
	// the same type for both producers and consumers unlike the underlying
	// amqp library where producers and consumers have different message types
	// amqp.Publishing and amqp.Delivery respectively. The second purpose is
	// to allow us to Consume() across a pool of AMQP channels and dispatch
	// those messages to the Consumer's go channel
	linkSubscribe := &cons.options.Link.Subscribe // Get reference to Link.Subscribe
	for i := 0; i < cons.poolSize; i++ {
		wg.Add(1)
		i := i
		go func() {
			logmsg := "Consumer dispatcher"
			if cons.poolSize > 1 {
				logmsg = fmt.Sprintf("Consumer pool dispatcher %d", i)
			}

			log.Infof("Starting %s", logmsg)

			// Use buffer to ensure close event is sent even if receiver is blocked.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.NotifyClose
			cl := cons.session.channels[i].NotifyClose(make(chan *amqp.Error, 1))

			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.Consume
			deliveries, _ := cons.session.channels[i].Consume(
				cons.name,               // queue
				"",                      // consumer
				cons.session.autoAck,    // auto-ack
				linkSubscribe.Exclusive, // exclusive
				false,                   // no-local
				false,                   // no-wait
				linkSubscribe.Arguments, // args
			)
			// Signal that the dispatcher is running and the Consumer is
			// available to receive message deliveries.
			wg.Done()
		loop:
			for {
				select {
				case <-cons.session.connection.ctx.Done(): // Receive cancel event
					log.Infof("Cancelling %s", logmsg)
					// Prevent reopening the Consumer if cancelled.
					atomic.StoreInt32(&cons.session.closed, 1)
					break loop
				case err := <-cl: // Receive channel close event
					if err == nil {
						log.Infof("Stopping %s", logmsg)
					} else {
						log.Infof("Stopping %s with %s", logmsg, err)
					}
					cons.closed = true
					break loop
				case msg := <-deliveries: // Receive message delivery
					// "Adapt" amqp.Delivery to messaging.Message.
					message := Message{
						Acknowledger:    msg.Acknowledger,
						Headers:         msg.Headers,
						ContentType:     msg.ContentType,
						ContentEncoding: msg.ContentEncoding,
						CorrelationID:   msg.CorrelationId,
						ReplyTo:         msg.ReplyTo,
						Expiration:      msg.Expiration,
						MessageID:       msg.MessageId,
						Timestamp:       msg.Timestamp,
						Type:            msg.Type,
						UserId:          msg.UserId,
						AppId:           msg.AppId,

						ConsumerTag: msg.ConsumerTag,

						Subject: msg.RoutingKey,

						Body:        msg.Body,
						DeliveryTag: msg.DeliveryTag,

						Priority:    msg.Priority,
						Redelivered: msg.Redelivered,
					}

					if msg.DeliveryMode == 2 {
						message.Durable = true
					}

					// If autoAck is set then use the dummy Acknowledger.
					// this lets us "ignore" any explicit Acknowledge rather
					// than failing because of the broker sending:
					// "PRECONDITION_FAILED - unknown delivery tag 1"
					if cons.session.autoAck {
						message.Acknowledger = dummyAck
					}

					select {
					case cons.queue <- message:
						// Send Message to cons.queue channel
					case err := <-cl:
						if err == nil {
							log.Infof("Stopping %s", logmsg)
						} else {
							log.Infof("Stopping %s with %s", logmsg, err)
						}
						cons.closed = true
						break loop
					}
				}
			}

			// If the select has exited it means that the AMQP channel has
			// closed. We test if if was due to a "hard close" via the Close()
			// method on Session and if not we reopen the Consumer to trigger
			// Session and Connection reconnect.
			if i == 0 && atomic.LoadInt32(&cons.session.closed) == 0 {
				for cons.closed {
					cons.m.Lock()
					if cons.closed {
						cons.reconnect() // Block until Connection & Session are connected.
						cons.open()
						cons.start()
					}
					cons.m.Unlock()
				}
			}
		}()
	}

	// Block until all dispatchers are running and their Consumers are
	// available to receive message deliveries.
	wg.Wait()
}

// Open parses the "address string", which is a JSON description of the
// broker topology, the schema for which is described in the comments for
// Destination parseAddress(). Using an address string in this way provides a
// a way to abstract the underlying messaging implementation details like the
// AMQP exchange_declare, queue_declare, etc. where those details are instead
// represented by the address string schema.
func (cons *consumerAMQP091) open() error {
	log.Infof("Creating Consumer with address: %s", cons.address)

	cons.session.createPool(cons.poolSize)
	err := cons.parseAddress(cons.address)
	if err == nil {
		declare := &cons.options.Node.Declare      // Get reference to Node.Declare
		linkDeclare := &cons.options.Link.Declare  // Get reference to Link.Declare
		nodeBindings := cons.options.Node.Bindings // Get Node.Bindings slice

		// Set default (or reset configured) capacity/message prefetch.
		err := cons.SetCapacity(cons.capacity)
		if err != nil {
			return err
		}

		// Check if an exchange with the name of this destination exists.
		exchange := ""
		if cons.name != "" {
			// Use temporary channel as the channel gets closed on an exception.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Connection.Channel
			tempCh, err := cons.session.connection.connections[0].Channel()
			if err != nil {
				return err
			}

			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.ExchangeDeclarePassive
			if err = tempCh.ExchangeDeclarePassive(
				cons.name,            // name
				declare.ExchangeType, // kind
				declare.Durable,      // durable
				declare.AutoDelete,   // autoDelete
				declare.Internal,     // internal
				false,                // noWait
				nil,                  // arguments
			); err == nil {
				// If broker doesn't exception an exchange with this name exists.
				exchange = cons.name
			} else {
				// If the broker exceptions the exchange doesn't exist.
				if cons.subject != "" {
					// If the consumer name is set in the Node.Declare we'll
					// be declaring the exchange later so ignore the broker
					// exception otherwise return it as an error
					if cons.name == declare.Exchange {
						exchange = cons.name
					} else {
						return err
					}
				}
				// Otherwise we assume default direct exchange
			}
			tempCh.Close()
		}

		if exchange != "" { // Is this address an exchange?
			// Destination is an exchange, create subscription queue and
			// add binding between exchange and queue with subject as key.
			if declare.Queue != "" {
				cons.name = declare.Queue
			} else if linkDeclare.Queue != "" {
				cons.name = linkDeclare.Queue
			} else {
				cons.name = "" // Name will be created by broker.
			}

			if len(nodeBindings) == 0 && cons.subject != "" {
				nodeBindings = append(nodeBindings, bindings{
					Queue:    cons.name,
					Exchange: exchange,
					Key:      cons.subject,
				})
			}
		}

		// Declare queue, exchange and bindings as necessary
		if declare.Exchange != "" {
			// Exchange declare - unusual scenario for Consumer, but handle it.
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.ExchangeDeclare
			cons.session.channels[0].ExchangeDeclare(
				declare.Exchange,     // name
				declare.ExchangeType, // kind
				declare.Durable,      // durable
				declare.AutoDelete,   // autoDelete
				declare.Internal,     // internal
				false,                // noWait
				declare.Arguments,    // arguments
			)
		}

		// Queue declare
		if exchange != "" && declare.Queue == "" {
			// If Node.Declare was an exchange use Link.Declare for Queue config
			declare = linkDeclare
		}
		if cons.name == "" {
			// If a broker assigned queue name explicitly set to AutoDelete.
			declare.AutoDelete = true
		}

		// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.QueueDeclare
		result, err := cons.session.channels[0].QueueDeclare(
			cons.name,          // name
			declare.Durable,    // durable
			declare.AutoDelete, // autoDelete
			declare.Exclusive,  // exclusive
			false,              // noWait
			declare.Arguments,  // arguments
		)
		if err != nil {
			return err
		}

		// Get the queue name from the result of the QueueDeclare to deal with
		// the case of server created names when we name "" to QueueDeclare
		cons.name = result.Name

		for _, binding := range nodeBindings {
			if binding.Exchange == "" {
				continue // Can't bind to default
			}
			// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.QueueBind
			cons.session.channels[0].QueueBind(
				cons.name,         // name
				binding.Key,       // key
				binding.Exchange,  // exchange
				false,             // noWait
				binding.Arguments, // arguments
			)
		}
	}

	// fmt.Println("cons.name " + cons.name)
	// fmt.Println("cons.subject " + cons.subject)

	return err
}

// Returns the Consumer name, which represents to the name of the queue
// that the Consumer will be consuming from.
func (cons *consumerAMQP091) Name() string {
	return cons.name
}

// Gets the number of Messages that the server will deliver before receiving
// delivery acknowledgements, e.g. the prefetch capacity.
func (cons *consumerAMQP091) Capacity() int {
	return cons.capacity
}

// Sets the number of Messages that the server will deliver before receiving
// delivery acknowledgements, e.g. the prefetch capacity.
func (cons *consumerAMQP091) SetCapacity(capacity int) error {
	cons.capacity = capacity
	// https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Channel.Qos
	// Question, is it even useful to have a Consumer pool?
	// With Qos set messages will be unevenly distributed across connections
	// in batches of Qos size - need to performance test.
	for i := range cons.session.channels {
		err := cons.session.channels[i].Qos(
			cons.capacity, // prefetchCount
			0,             // prefetchSize
			false,         // global
		)
		if err != nil {
			log.Errorf("SetCapacity error: %s when setting Qos", err)
			return err
		}
	}

	return nil
}

// Consume returns a go channel that Messages will be dispatched to.
// messages := consumer.Consume()
//
//	for message := range messages {
//	    <Do stuff with message here>
//	}
func (cons *consumerAMQP091) Consume() <-chan Message {
	cons.m.Lock() // Avoid potential race on creating cons.queue chan
	defer cons.m.Unlock()

	if cons.queue == nil {
		cons.queue = make(chan Message)
		cons.start()
	}

	return cons.queue
}
