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
//	    GroupID:    "f3438ee9-b964-48aa-b938-a803df440a3c",
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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Default configuration values used by the client
const (
	defaultAPIEndpoint = "https://events.chirpier.co/v1.0/events"
	defaultRetries     = 5
	defaultTimeout     = 10 * time.Second
	defaultBatchSize   = 50
	defaultFlushDelay  = 500 * time.Millisecond
	defaultBufferSize  = 1000
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
		return errors.New("Chirpier SDK is already initialized")
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
		return errors.New("Chirpier SDK is not initialized. Please call Initialize() first")
	}

	return client.Monitor(ctx, event)
}

// Stop gracefully shuts down the Chirpier client, flushing any remaining events.
// It returns an error if the shutdown fails or times out.
// After stopping, the client must be reinitialized before sending more events.
func Stop(ctx context.Context) error {
	mu.Lock()
	defer mu.Unlock()

	if instance == nil {
		return nil
	}

	// Create a local copy of the instance
	localInstance := instance

	// Set the global instance to nil before stopping
	instance = nil

	// Release the lock before calling Stop to avoid potential deadlock
	mu.Unlock()

	// Add a small delay to help mitigate potential race conditions
	time.Sleep(500 * time.Millisecond)

	err := localInstance.Stop(ctx)

	// Add another small delay before re-acquiring the lock
	time.Sleep(500 * time.Millisecond)

	// Re-acquire the lock to ensure thread-safety
	mu.Lock()

	return err
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

	c := &Client{
		apiKey:      options.Key,
		apiEndpoint: defaultAPIEndpoint,
		retries:     defaultRetries,
		timeout:     defaultTimeout,
		client:      httpClient,
		eventQueue:  make([]Event, 0, defaultBatchSize),
		batchSize:   defaultBatchSize,
		flushDelay:  defaultFlushDelay,
		eventChan:   make(chan Event, defaultBufferSize),
		stopChan:    make(chan struct{}),
		doneChan:    make(chan struct{}),
	}

	if options.APIEndpoint != "" {
		c.apiEndpoint = options.APIEndpoint
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
		return &Error{"Invalid event format. Must include group_id, stream, and numeric value"}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		select {
		case c.eventChan <- event:
			log.Printf("Event added to channel: %+v", event)
			return nil
		default:
			return &Error{"Event buffer is full"}
		}
	}
}

// Stop gracefully shuts down the client, ensuring all queued events are sent.
func (c *Client) Stop(ctx context.Context) error {
	// Signal stop by closing the stopChan
	close(c.stopChan)

	// Wait for processing to complete or context to timeout
	select {
	case <-c.doneChan:
		// Ensure final flush
		c.flushEvents()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %w", ctx.Err())
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
			// Drain any remaining events from the channel
			for len(c.eventChan) > 0 {
				event := <-c.eventChan
				c.queueEvent(event)
			}
			c.flushEvents()
			return
		}
	}
}

// queueEvent adds an event to the queue and triggers a flush if the batch size is reached.
func (c *Client) queueEvent(event Event) {
	c.queueMutex.Lock()
	defer c.queueMutex.Unlock()

	c.eventQueue = append(c.eventQueue, event)
	if len(c.eventQueue) >= c.batchSize {
		go c.flushEvents()
	}
}

// flushEvents sends all queued events to the Chirpier API.
// Failed events are re-queued for retry.
func (c *Client) flushEvents() {
	c.queueMutex.Lock()
	events := c.eventQueue
	c.eventQueue = c.eventQueue[:0]
	c.queueMutex.Unlock()

	if len(events) > 0 {
		log.Printf("Flushing %d events", len(events))
		if err := c.sendEvents(events); err != nil {
			log.Printf("Failed to send events: %v", err)
			// Re-queue failed events
			c.queueMutex.Lock()
			c.eventQueue = append(c.eventQueue, events...)
			c.queueMutex.Unlock()
		} else {
			log.Printf("Successfully sent %d events", len(events))
		}
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
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
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
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
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
