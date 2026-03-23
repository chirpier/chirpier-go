// Package chirpier-go provides a client SDK for sending logs to the Chirpier service.
//
// The package implements a thread-safe client that batches logs and sends them to the Chirpier API.
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
//	entry := chirpier.Log{
//	    AgentID: "openclaw.main",
//	    Event:   "http_requests",
//	    Value:   1,
//	}
//	err = chirpier.LogEvent(context.Background(), entry)
package chirpier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
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
	defaultRetries    = 10
	defaultTimeout    = 10 * time.Second
	defaultBatchSize  = 500
	defaultFlushDelay = 500 * time.Millisecond
	defaultBufferSize = 5000
	defaultLogLevel   = LogLevelNone
	defaultMaxWorkers = 3
)

// Log represents a log entry to be sent to Chirpier.
// Event and Value are required; AgentID is optional.
type Log struct {
	// AgentID is an optional string that identifies the agent this log belongs to
	AgentID string `json:"agent_id,omitempty"`
	// Event identifies the event for this log
	Event string `json:"event"`
	// Value is the numeric value for this log
	Value float64 `json:"value"`
	// Meta is an optional JSON-encodable map of key-value pairs that can be used to store additional metadata about the log
	Meta any `json:"meta,omitempty"`
	// OccurredAt is an optional timestamp for when the log occurred
	OccurredAt time.Time `json:"occurred_at,omitempty"`
}

type EventDefinition struct {
	EventID          string `json:"event_id"`
	AgentID          string `json:"agent_id,omitempty"`
	Event            string `json:"event"`
	Title            string `json:"title,omitempty"`
	Public           bool   `json:"public"`
	Description      string `json:"description,omitempty"`
	Unit             string `json:"unit,omitempty"`
	SemanticClass    string `json:"semantic_class"`
	DefaultAggregate string `json:"default_aggregate"`
	Enabled          bool   `json:"enabled"`
	Origin           string `json:"origin"`
	ArchivedAt       string `json:"archived_at,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
}

type Policy struct {
	PolicyID    string  `json:"policy_id,omitempty"`
	EventID     string  `json:"event_id"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Channel     string  `json:"channel,omitempty"`
	Period      string  `json:"period,omitempty"`
	Aggregate   string  `json:"aggregate,omitempty"`
	Condition   string  `json:"condition"`
	Threshold   float64 `json:"threshold"`
	Severity    string  `json:"severity,omitempty"`
	Enabled     bool    `json:"enabled"`
}

type Alert struct {
	AlertID        string  `json:"alert_id"`
	PolicyID       string  `json:"policy_id"`
	EventID        string  `json:"event_id"`
	AgentID        string  `json:"agent_id,omitempty"`
	Event          string  `json:"event"`
	Title          string  `json:"title"`
	Period         string  `json:"period"`
	Aggregate      string  `json:"aggregate"`
	Condition      string  `json:"condition"`
	Threshold      float64 `json:"threshold"`
	Severity       string  `json:"severity"`
	Status         string  `json:"status"`
	Value          float64 `json:"value"`
	Count          int     `json:"count"`
	Min            float64 `json:"min"`
	Max            float64 `json:"max"`
	TriggeredAt    string  `json:"triggered_at,omitempty"`
	AcknowledgedAt string  `json:"acknowledged_at,omitempty"`
	ResolvedAt     string  `json:"resolved_at,omitempty"`
}

type AlertDelivery struct {
	AttemptID      string `json:"attempt_id"`
	AlertID        string `json:"alert_id"`
	WebhookID      string `json:"webhook_id,omitempty"`
	Channel        string `json:"channel"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	ResponseStatus *int   `json:"response_status,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type EventLogPoint struct {
	EventID    string  `json:"event_id"`
	AgentID    string  `json:"agent_id,omitempty"`
	Event      string  `json:"event"`
	Period     string  `json:"period"`
	OccurredAt string  `json:"occurred_at"`
	Count      int     `json:"count"`
	Value      float64 `json:"value"`
	Squares    float64 `json:"squares"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
}

// Options contains configuration options for initializing the Chirpier client.
// Only Key is required; other fields are optional and will use defaults if not specified.
type Options struct {
	// Key is the API key used to authenticate with Chirpier (required)
	Key string
	// APIEndpoint allows overriding the default Chirpier API endpoint
	APIEndpoint string
	// ServicerEndpoint allows overriding the default Chirpier control-plane endpoint
	ServicerEndpoint string
	// LogLevel controls the verbosity of logging (optional, defaults to None)
	// Use pointer to distinguish between unset and explicitly set to LogLevelNone
	LogLevel *LogLevel
	// BufferSize sets the size of the log channel buffer (optional, defaults to 5000)
	BufferSize int
	// Retries sets the number of retry attempts for failed requests (optional, defaults to 10)
	Retries int
	// BatchSize sets the number of logs to batch before sending (optional, defaults to 500)
	BatchSize int
	// FlushDelay sets how often to flush batched logs (optional, defaults to 500ms)
	FlushDelay time.Duration
	// Timeout sets the HTTP request timeout (optional, defaults to 10s)
	Timeout time.Duration
	// MaxWorkers sets the number of concurrent workers for sending batches (optional, defaults to 3)
	MaxWorkers int
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
// It manages log batching, retries, and connection pooling.
type Client struct {
	apiKey      string
	apiEndpoint string
	servicerURL string
	retries     int
	timeout     time.Duration
	client      *http.Client
	logQueue    []Log
	queueMutex  sync.Mutex
	batchSize   int
	flushDelay  time.Duration
	logChan     chan Log
	stopChan    chan struct{}
	doneChan    chan struct{}
	flushMutex  sync.Mutex
	logLevel    LogLevel
	// Worker pool fields
	batchQueue chan []Log
	maxWorkers int
	workerWg   sync.WaitGroup
	inFlight   int32
}

var (
	instance *Client
	mu       sync.RWMutex
)

// Initialize creates and configures a new Chirpier client instance.
// It returns an error if initialization fails or if the client is already initialized.
// The client must be initialized before logs can be sent via package-level helpers.
func Initialize(options Options) error {
	return initializeWithClient(options, nil)
}

// NewClient creates a standalone Chirpier client instance.
// This is the recommended API for libraries and applications that avoid global state.
func NewClient(options Options) (*Client, error) {
	client, err := newClient(options, nil)
	if err != nil {
		return nil, err
	}

	client.startWorkers()
	go client.run()

	return client, nil
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
	instance.startWorkers()
	go instance.run()
	return nil
}

// LogEvent sends a log using the global Chirpier instance.
// It returns an error if the SDK is not initialized or if the log is invalid.
// Logs are batched and sent asynchronously for better performance.
func LogEvent(ctx context.Context, entry Log) error {
	mu.RLock()
	client := instance
	mu.RUnlock()

	if client == nil {
		return errors.New("chirpier SDK is not initialized. Please call Initialize() first")
	}

	return client.Log(ctx, entry)
}

// Stop gracefully shuts down the Chirpier client, flushing any remaining logs.
// It returns an error if the shutdown fails or times out.
// After stopping, the client must be reinitialized before sending more logs.
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

// Flush synchronously flushes pending logs for the global Chirpier instance.
func Flush(ctx context.Context) error {
	mu.RLock()
	client := instance
	mu.RUnlock()

	if client == nil {
		return errors.New("chirpier SDK is not initialized. Please call Initialize() first")
	}

	return client.Flush(ctx)
}

// newClient creates a new Chirpier client with the given options.
// It validates the API key and sets up default configuration values.
func newClient(options Options, httpClient *http.Client) (*Client, error) {
	options.Key = strings.TrimSpace(options.Key)
	if options.Key == "" {
		options.Key = strings.TrimSpace(os.Getenv("CHIRPIER_API_KEY"))
	}

	if options.Key == "" {
		_ = godotenv.Load(".env")
		options.Key = strings.TrimSpace(os.Getenv("CHIRPIER_API_KEY"))
	}

	if options.Key == "" {
		return nil, &Error{"API key is required"}
	}

	if !isValidAPIKey(options.Key) {
		return nil, &Error{"Invalid API key: must start with 'chp_'"}
	}

	// Set defaults for optional configuration
	if options.APIEndpoint == "" {
		options.APIEndpoint = "https://logs.chirpier.co/v1.0/logs"
	}
	if options.ServicerEndpoint == "" {
		options.ServicerEndpoint = "https://api.chirpier.co/v1.0"
	}

	parsedEndpoint, err := url.Parse(options.APIEndpoint)
	if err != nil || !parsedEndpoint.IsAbs() || parsedEndpoint.Host == "" {
		return nil, &Error{"apiEndpoint must be a valid absolute URL"}
	}
	if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return nil, &Error{"apiEndpoint must use http or https"}
	}
	parsedServicerEndpoint, err := url.Parse(options.ServicerEndpoint)
	if err != nil || !parsedServicerEndpoint.IsAbs() || parsedServicerEndpoint.Host == "" {
		return nil, &Error{"servicerEndpoint must be a valid absolute URL"}
	}
	if parsedServicerEndpoint.Scheme != "http" && parsedServicerEndpoint.Scheme != "https" {
		return nil, &Error{"servicerEndpoint must use http or https"}
	}

	logLevel := defaultLogLevel
	if options.LogLevel != nil {
		logLevel = *options.LogLevel
	}

	bufferSize := defaultBufferSize
	if options.BufferSize > 0 {
		bufferSize = options.BufferSize
	}

	retries := defaultRetries
	if options.Retries > 0 {
		retries = options.Retries
	}

	batchSize := defaultBatchSize
	if options.BatchSize > 0 {
		batchSize = options.BatchSize
	}

	flushDelay := defaultFlushDelay
	if options.FlushDelay > 0 {
		flushDelay = options.FlushDelay
	}

	timeout := defaultTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}

	maxWorkers := defaultMaxWorkers
	if options.MaxWorkers > 0 {
		maxWorkers = options.MaxWorkers
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	c := &Client{
		apiKey:      options.Key,
		apiEndpoint: options.APIEndpoint,
		servicerURL: strings.TrimRight(options.ServicerEndpoint, "/"),
		retries:     retries,
		timeout:     timeout,
		client:      httpClient,
		logQueue:    make([]Log, 0, batchSize),
		batchSize:   batchSize,
		flushDelay:  flushDelay,
		logChan:     make(chan Log, bufferSize),
		stopChan:    make(chan struct{}),
		doneChan:    make(chan struct{}),
		logLevel:    logLevel,
		batchQueue:  make(chan []Log, maxWorkers*2), // Buffer for worker queue
		maxWorkers:  maxWorkers,
	}

	return c, nil
}

func (c *Client) ListEvents(ctx context.Context) ([]EventDefinition, error) {
	var events []EventDefinition
	if err := c.doJSON(ctx, http.MethodGet, c.servicerURL+"/events", nil, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (c *Client) GetEvent(ctx context.Context, eventID string) (*EventDefinition, error) {
	var event EventDefinition
	if err := c.doJSON(ctx, http.MethodGet, c.servicerURL+"/events/"+strings.TrimSpace(eventID), nil, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *Client) UpdateEvent(ctx context.Context, eventID string, payload map[string]any) (*EventDefinition, error) {
	var event EventDefinition
	if err := c.doJSON(ctx, http.MethodPut, c.servicerURL+"/events/"+strings.TrimSpace(eventID), payload, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *Client) ListPolicies(ctx context.Context) ([]Policy, error) {
	var policies []Policy
	if err := c.doJSON(ctx, http.MethodGet, c.servicerURL+"/policies", nil, &policies); err != nil {
		return nil, err
	}
	return policies, nil
}

func (c *Client) CreatePolicy(ctx context.Context, payload Policy) (*Policy, error) {
	var policy Policy
	if err := c.doJSON(ctx, http.MethodPost, c.servicerURL+"/policies", payload, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

func (c *Client) ListAlerts(ctx context.Context, status string) ([]Alert, error) {
	endpoint := c.servicerURL + "/alerts"
	if strings.TrimSpace(status) != "" {
		endpoint += "?status=" + url.QueryEscape(strings.TrimSpace(status))
	}
	var alerts []Alert
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &alerts); err != nil {
		return nil, err
	}
	return alerts, nil
}

func (c *Client) AcknowledgeAlert(ctx context.Context, alertID string) (*Alert, error) {
	var alert Alert
	if err := c.doJSON(ctx, http.MethodPost, c.servicerURL+"/alerts/"+strings.TrimSpace(alertID)+"/acknowledge", nil, &alert); err != nil {
		return nil, err
	}
	return &alert, nil
}

func (c *Client) ResolveAlert(ctx context.Context, alertID string) (*Alert, error) {
	var alert Alert
	if err := c.doJSON(ctx, http.MethodPost, c.servicerURL+"/alerts/"+strings.TrimSpace(alertID)+"/resolve", nil, &alert); err != nil {
		return nil, err
	}
	return &alert, nil
}

func (c *Client) ArchiveAlert(ctx context.Context, alertID string) (*Alert, error) {
	var alert Alert
	if err := c.doJSON(ctx, http.MethodPost, c.servicerURL+"/alerts/"+strings.TrimSpace(alertID)+"/archive", nil, &alert); err != nil {
		return nil, err
	}
	return &alert, nil
}

func (c *Client) TestWebhook(ctx context.Context, webhookID string) error {
	return c.doJSON(ctx, http.MethodPost, c.servicerURL+"/webhooks/"+strings.TrimSpace(webhookID)+"/test", nil, nil)
}

func (c *Client) GetAlertDeliveries(ctx context.Context, alertID string, limit int, offset int, kind string) ([]AlertDelivery, error) {
	endpoint := c.servicerURL + "/alerts/" + strings.TrimSpace(alertID) + "/deliveries"
	query := url.Values{}
	if strings.TrimSpace(kind) != "" {
		query.Set("kind", strings.TrimSpace(kind))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var deliveries []AlertDelivery
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &deliveries); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (c *Client) GetEventLogs(ctx context.Context, eventID, period string, limit int, offset int) ([]EventLogPoint, error) {
	endpoint := c.servicerURL + "/events/" + strings.TrimSpace(eventID) + "/logs"
	query := url.Values{}
	if strings.TrimSpace(period) != "" {
		query.Set("period", strings.TrimSpace(period))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var logs []EventLogPoint
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload any, output any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(resp.Body)
		return &Error{Message: fmt.Sprintf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
	}

	if output == nil {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(output)
}

// isValidLog checks if a log contains all required fields in valid formats.
func (c *Client) isValidLog(entry Log) bool {
	now := time.Now().UTC()
	oldestAllowed := now.Add(-30 * 24 * time.Hour)
	newestAllowed := now.Add(24 * time.Hour)

	if strings.TrimSpace(entry.Event) == "" {
		return false
	}

	if math.IsNaN(entry.Value) || math.IsInf(entry.Value, 0) {
		return false
	}

	if entry.Meta != nil {
		if _, err := json.Marshal(entry.Meta); err != nil {
			return false
		}
	}

	if !entry.OccurredAt.IsZero() {
		occurredAt := entry.OccurredAt.UTC()
		if occurredAt.Before(oldestAllowed) || occurredAt.After(newestAllowed) {
			return false
		}
	}

	return true
}

// Log queues a log for sending to Chirpier.
// It validates the log format and returns an error if the log is invalid or the buffer is full.
func (c *Client) Log(ctx context.Context, entry Log) error {
	if !c.isValidLog(entry) {
		return &Error{Message: "invalid log: event must not be empty, value must be finite, meta must be JSON-encodable when provided, and occurred_at must be within the last 30 days and no more than 1 day in the future"}
	}

	entry.Event = strings.TrimSpace(entry.Event)
	entry.AgentID = strings.TrimSpace(entry.AgentID)
	if !entry.OccurredAt.IsZero() {
		entry.OccurredAt = entry.OccurredAt.UTC()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		select {
		case c.logChan <- entry:
			if c.logLevel >= LogLevelDebug {
				log.Printf("Log added to channel: %+v", entry)
			}
			return nil
		default:
			if c.logLevel >= LogLevelError {
				log.Printf("Log buffer full, dropping log: %+v", entry)
			}
			return &Error{Message: "log buffer full, log dropped"}
		}
	}
}

// Stop gracefully shuts down the client, ensuring all queued logs are sent.
func (c *Client) Stop(ctx context.Context) error {
	close(c.stopChan)
	select {
	case <-c.doneChan:
		// Wait for all workers to finish with context timeout
		workerDone := make(chan struct{})
		go func() {
			c.workerWg.Wait()
			close(workerDone)
		}()

		select {
		case <-workerDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close gracefully shuts down the client.
// It is an alias for Stop and is provided for API ergonomics.
func (c *Client) Close(ctx context.Context) error {
	return c.Stop(ctx)
}

// Flush attempts to deliver all currently queued logs before returning.
func (c *Client) Flush(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		for {
			select {
			case entry := <-c.logChan:
				c.queueLog(entry)
			default:
				goto drained
			}
		}

	drained:
		c.flushLogs()

		if c.isIdle() {
			return nil
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// startWorkers initializes and starts the worker pool for sending batches concurrently.
func (c *Client) startWorkers() {
	for i := 0; i < c.maxWorkers; i++ {
		c.workerWg.Add(1)
		go c.worker(i)
	}
}

// worker processes batches from the batch queue and sends them to the API.
func (c *Client) worker(id int) {
	defer c.workerWg.Done()

	if c.logLevel >= LogLevelDebug {
		log.Printf("Worker %d started", id)
	}

	for batch := range c.batchQueue {
		atomic.AddInt32(&c.inFlight, 1)
		if c.logLevel >= LogLevelDebug {
			log.Printf("Worker %d processing batch of %d logs", id, len(batch))
		}

		ctx := context.Background()
		if err := c.sendLogs(ctx, batch); err != nil {
			if c.logLevel >= LogLevelError {
				log.Printf("Worker %d: Error sending batch: %v", id, err)
			}
			// Re-queue failed logs
			c.queueMutex.Lock()
			c.logQueue = append(batch, c.logQueue...)
			c.queueMutex.Unlock()
		} else if c.logLevel >= LogLevelInfo {
			log.Printf("Worker %d: Successfully sent batch of %d logs", id, len(batch))
		}
		atomic.AddInt32(&c.inFlight, -1)
	}

	if c.logLevel >= LogLevelDebug {
		log.Printf("Worker %d stopped", id)
	}
}

func (c *Client) isIdle() bool {
	c.queueMutex.Lock()
	queuedLogs := len(c.logQueue)
	c.queueMutex.Unlock()

	return queuedLogs == 0 && len(c.logChan) == 0 && len(c.batchQueue) == 0 && atomic.LoadInt32(&c.inFlight) == 0
}

// run is the main loop that processes incoming logs and handles periodic flushing.
func (c *Client) run() {
	defer func() {
		close(c.batchQueue) // Close batch queue to signal workers to stop
		close(c.doneChan)
	}()

	ticker := time.NewTicker(c.flushDelay)
	defer ticker.Stop()

	for {
		select {
		case entry, ok := <-c.logChan:
			if !ok {
				// Channel was closed
				c.flushLogs()
				return
			}
			c.queueLog(entry)
		case <-ticker.C:
			c.flushLogs()
		case <-c.stopChan:
			for {
				select {
				case entry := <-c.logChan:
					c.queueLog(entry)
				default:
					c.flushLogs()
					return
				}
			}
		}
	}
}

// queueLog adds a log to the queue and triggers a flush if the batch size is reached.
func (c *Client) queueLog(entry Log) {
	c.queueMutex.Lock()
	c.logQueue = append(c.logQueue, entry)
	shouldFlush := len(c.logQueue) >= c.batchSize
	c.queueMutex.Unlock()

	if shouldFlush {
		c.flushLogs()
	}
}

// flushLogs sends all queued logs to the worker pool for processing.
// Logs are submitted as batches to workers for concurrent sending.
func (c *Client) flushLogs() {
	// Ensure only one flush at a time
	if !c.flushMutex.TryLock() {
		return
	}
	defer c.flushMutex.Unlock()

	c.queueMutex.Lock()
	if len(c.logQueue) == 0 {
		c.queueMutex.Unlock()
		return
	}
	entries := make([]Log, len(c.logQueue))
	copy(entries, c.logQueue)
	c.logQueue = c.logQueue[:0]
	c.queueMutex.Unlock()

	// Submit batch to worker pool for concurrent processing
	select {
	case c.batchQueue <- entries:
		if c.logLevel >= LogLevelDebug {
			log.Printf("Submitted batch of %d logs to worker pool", len(entries))
		}
	default:
		// Worker queue is full, re-queue logs
		if c.logLevel >= LogLevelError {
			log.Printf("Worker queue full, re-queueing %d logs", len(entries))
		}
		c.queueMutex.Lock()
		c.logQueue = append(entries, c.logQueue...)
		c.queueMutex.Unlock()
	}
}

// sendLogs sends a batch of logs to the Chirpier API.
func (c *Client) sendLogs(ctx context.Context, entries []Log) error {
	jsonData, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal logs: %w", err)
	}

	return c.retryRequest(ctx, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.apiEndpoint, bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
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

// retryRequest executes a request with exponential backoff retry logic and jitter.
// It respects context cancellation during backoff periods.
func (c *Client) retryRequest(ctx context.Context, requestFunc func() error) error {
	var err error
	for attempt := 0; attempt <= c.retries; attempt++ {
		// Check context before attempting
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err = requestFunc(); err == nil {
			return nil
		}

		if attempt < c.retries {
			// Calculate exponential backoff with jitter
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			// Add jitter: randomize up to 20% of backoff time to prevent thundering herd
			jitter := time.Duration(float64(backoff) * 0.2 * (0.5 + (float64(attempt%10) / 20.0)))
			backoffWithJitter := backoff + jitter

			if c.logLevel >= LogLevelDebug {
				log.Printf("Request failed, retrying in %v: %v", backoffWithJitter, err)
			}

			// Use context-aware sleep
			timer := time.NewTimer(backoffWithJitter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("failed to send request after %d attempts: %w", c.retries, err)
}

// isValidAPIKey checks if a token has the expected Chirpier prefix.
func isValidAPIKey(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "chp_") && len(token) > len("chp_")
}
