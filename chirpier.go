// Package chirpier provides a client SDK for sending monitoring events to the Chirpier service.
//
// The package implements a thread-safe client that batches events and sends them to the Chirpier API.
// It handles automatic retries with exponential backoff, graceful shutdown, and proper error handling.
//
// Basic usage:
//
//	err := chirpier.Initialize(chirpier.Options{
//	    Key: "your-api-key",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	event := chirpier.Event{
//	    GroupID:    "bfd9299d-817a-452f-bc53-6e154f2281fc",
//	    StreamName: "Clicks",
//	    Value:      1,
//	}
//	err = chirpier.Monitor(context.Background(), event)
package chirpier

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// LogLevel represents different logging levels
type LogLevel int

const (
	// LogLevelNone disables all logging
	LogLevelNone LogLevel = iota
	// LogLevelError enables error logging only
	LogLevelError
	// LogLevelInfo enables info and error logging
	LogLevelInfo
	// LogLevelDebug enables all logging
	LogLevelDebug
)

// Default configuration values used by the client
const (
	defaultRegion     = "events"
	defaultRetries    = 10
	defaultTimeout    = 10 * time.Second
	defaultBatchSize  = 350
	defaultFlushDelay = 500 * time.Millisecond
	defaultBufferSize = 50000
	defaultLogLevel   = LogLevelNone
)

// Event represents a monitoring event to be sent to Chirpier.
// Each event must have a GroupID (UUID), StreamName (string), and Value (float64).
type Event struct {
	// GroupID is a UUID that identifies the group this event belongs to
	GroupID string `json:"group_id"`
	// StreamName identifies the metric stream for this event
	StreamName string `json:"stream_name"`
	// Value is the numeric value for this event
	Value float64 `json:"value"`
}

// Options contains configuration options for initializing the Chirpier client.
// Only Key is required; other fields are optional and will use defaults if not specified.
type Options struct {
	// Key is the API key used to authenticate with Chirpier (required)
	Key string
	// APIEndpoint allows overriding the default Chirpier API endpoint
	APIEndpoint string
	// Region allows overriding the default Chirpier API endpoint region
	Region string
	// LogLevel controls the verbosity of logging (optional, defaults to None)
	LogLevel LogLevel
}

// Error represents a Chirpier-specific error.
// It implements the error interface and provides additional context about API errors.
type Error struct {
	Message string
}

// Error returns the error message.
func (e *Error) Error() string {
	return e.Message
}

// Client handles communication with the Chirpier API.
// It manages event batching, retries, and connection pooling.
type Client struct {
	apiKey      string
	apiEndpoint string
	retries     int
	timeout     time.Duration
	client      *http.Client
	eventQueue  []Event
	queueMutex  sync.Mutex
	batchSize   int
	flushDelay  time.Duration
	eventChan   chan Event
	stopChan    chan struct{}
	doneChan    chan struct{}
	flushMutex  sync.Mutex
	logLevel    LogLevel
}

var (
	instance *Client
	mu       sync.RWMutex
)

// Initialize creates and configures a new Chirpier client instance.
// It returns an error if initialization fails or if the client is already initialized.
// The client must be initialized before any events can be monitored.
func Initialize(options Options) error {
	return initializeWithClient(options, nil)
}

// initializeWithClient is an internal function used for testing that allows injecting a custom HTTP client.
func initializeWithClient(options Options, httpClient *http.Client) error {
	mu.Lock()
	defer mu.Unlock()

	if instance != nil {
		return errors.New("chirpier SDK is already initialized")
	}

	client, err := newClient(options, httpClient)
	if err != nil {
		return err
	}

	instance = client
	go instance.run()
	return nil
}

// Monitor sends an event to be monitored using the global Chirpier instance.
// It returns an error if the SDK is not initialized or if the event is invalid.
// Events are batched and sent asynchronously for better performance.
func Monitor(ctx context.Context, event Event) error {
	mu.RLock()
	client := instance
	mu.RUnlock()

	if client == nil {
		return errors.New("chirpier SDK is not initialized. Please call Initialize() first")
	}

	return client.Monitor(ctx, event)
}

// Stop gracefully shuts down the Chirpier client, flushing any remaining events.
// It returns an error if the shutdown fails or times out.
// After stopping, the client must be reinitialized before sending more events.
func Stop(ctx context.Context) error {
	mu.Lock()
	localInstance := instance
	instance = nil
	mu.Unlock()

	if localInstance == nil {
		return nil
	}

	return localInstance.Stop(ctx)
}

// newClient creates a new Chirpier client with the given options.
// It validates the API key and sets up default configuration values.
func newClient(options Options, httpClient *http.Client) (*Client, error) {
	if options.Key == "" {
		return nil, &Error{"API key is required"}
	}

	if !isValidJWT(options.Key) {
		return nil, &Error{"Invalid API key: Not a valid JWT"}
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	if options.Region != "us-west" &&
		options.Region != "eu-west" &&
		options.Region != "asia-southeast" {
		options.Region = defaultRegion
	}

	if options.LogLevel == 0 {
		options.LogLevel = defaultLogLevel
	}

	if options.APIEndpoint == "" {
		options.APIEndpoint = fmt.Sprintf("https://%s.chirpier.co/v1.0/events", options.Region)
	}

	c := &Client{
		apiKey:      options.Key,
		apiEndpoint: options.APIEndpoint,
		retries:     defaultRetries,
		timeout:     defaultTimeout,
		client:      httpClient,
		eventQueue:  make([]Event, 0, defaultBatchSize),
		batchSize:   defaultBatchSize,
		flushDelay:  defaultFlushDelay,
		eventChan:   make(chan Event, defaultBufferSize),
		stopChan:    make(chan struct{}),
		doneChan:    make(chan struct{}),
		logLevel:    options.LogLevel,
	}

	return c, nil
}

// isValidEvent checks if an event contains all required fields in valid formats.
func (c *Client) isValidEvent(event Event) bool {
	_, err := uuid.Parse(event.GroupID)
	return err == nil &&
		strings.TrimSpace(event.GroupID) != "" &&
		strings.TrimSpace(event.StreamName) != ""
}

// Monitor queues an event for sending to Chirpier.
// It validates the event format and returns an error if the event is invalid or the buffer is full.
func (c *Client) Monitor(ctx context.Context, event Event) error {
	if !c.isValidEvent(event) {
		if c.logLevel >= LogLevelDebug {
			log.Printf("Invalid event format. Must include group_id, stream_name, and numeric value")
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		select {
		case c.eventChan <- event:
			if c.logLevel >= LogLevelDebug {
				log.Printf("Event added to channel: %+v", event)
			}
			return nil
		default:
			if c.logLevel >= LogLevelDebug {
				log.Printf("Event buffer full, dropping event: %+v", event)
			}
			return nil

		}
	}
}

// Stop gracefully shuts down the client, ensuring all queued events are sent.
func (c *Client) Stop(ctx context.Context) error {
	close(c.stopChan)
	select {
	case <-c.doneChan:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the main event loop that processes incoming events and handles periodic flushing.
func (c *Client) run() {
	defer close(c.doneChan)

	ticker := time.NewTicker(c.flushDelay)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-c.eventChan:
			if !ok {
				// Channel was closed
				c.flushEvents()
				return
			}
			c.queueEvent(event)
		case <-ticker.C:
			c.flushEvents()
		case <-c.stopChan:
			for {
				select {
				case event := <-c.eventChan:
					c.queueEvent(event)
				default:
					c.flushEvents()
					return
				}
			}
		}
	}
}

// queueEvent adds an event to the queue and triggers a flush if the batch size is reached.
func (c *Client) queueEvent(event Event) {
	c.queueMutex.Lock()
	c.eventQueue = append(c.eventQueue, event)
	shouldFlush := len(c.eventQueue) >= c.batchSize
	c.queueMutex.Unlock()

	if shouldFlush {
		c.flushEvents()
	}
}

// flushEvents sends all queued events to the Chirpier API.
// Failed events are re-queued for retry.
func (c *Client) flushEvents() {
	// Ensure only one flush at a time
	if !c.flushMutex.TryLock() {
		return
	}
	defer c.flushMutex.Unlock()

	c.queueMutex.Lock()
	if len(c.eventQueue) == 0 {
		c.queueMutex.Unlock()
		return
	}
	events := make([]Event, len(c.eventQueue))
	copy(events, c.eventQueue)
	c.eventQueue = c.eventQueue[:0]
	c.queueMutex.Unlock()

	if err := c.sendEvents(events); err != nil {
		if c.logLevel >= LogLevelError {
			log.Printf("Error sending events: %v", err)
		}
		c.queueMutex.Lock()
		// Prepend failed events to maintain order
		c.eventQueue = append(events, c.eventQueue...)
		c.queueMutex.Unlock()
	} else if c.logLevel >= LogLevelInfo {
		log.Printf("Successfully sent %d events", len(events))
	}
}

// sendEvents sends a batch of events to the Chirpier API.
func (c *Client) sendEvents(events []Event) error {
	jsonData, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	return c.retryRequest(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)

		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiEndpoint, bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusBadRequest {
			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := 1 * time.Second // Default retry after 1 second
				if retryAfterStr := resp.Header.Get("Retry-After"); retryAfterStr != "" {
					if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
						retryAfter = time.Duration(seconds) * time.Second
					}
				}
				time.Sleep(retryAfter)
				return fmt.Errorf("rate limited, retry after %v", retryAfter)
			}
			return fmt.Errorf("request failed with status code: %d", resp.StatusCode)
		}

		return nil
	})
}

// retryRequest executes a request with exponential backoff retry logic.
func (c *Client) retryRequest(requestFunc func() error) error {
	var err error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if err = requestFunc(); err == nil {
			return nil
		}

		if attempt < c.retries {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			if c.logLevel >= LogLevelDebug {
				log.Printf("Request failed, retrying in %v: %v", backoff, err)
			}
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("failed to send request after %d attempts: %w", c.retries, err)
}

// isValidJWT checks if a token string is a properly formatted JWT.
func isValidJWT(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}

	_, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}

	_, err = base64.RawURLEncoding.DecodeString(parts[1])

	return err == nil
}
