// Package chirpier provides functionality for monitoring and tracking events.
package chirpier

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mockRoundTripper is a custom http.RoundTripper for mocking HTTP requests
type mockRoundTripper struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func TestServicerHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/events/evt_123/logs":
			if got := r.URL.Query().Get("period"); got != "day" {
				t.Fatalf("expected period day, got %s", got)
			}
			if got := r.URL.Query().Get("limit"); got != "10" {
				t.Fatalf("expected limit 10, got %s", got)
			}
			if got := r.URL.Query().Get("offset"); got != "5" {
				t.Fatalf("expected offset 5, got %s", got)
			}
			_, _ = io.WriteString(w, `[]`)
		case "/v1.0/alerts/alrt_123/deliveries":
			if got := r.URL.Query().Get("kind"); got != "test" {
				t.Fatalf("expected kind test, got %s", got)
			}
			if got := r.URL.Query().Get("limit"); got != "20" {
				t.Fatalf("expected limit 20, got %s", got)
			}
			if got := r.URL.Query().Get("offset"); got != "10" {
				t.Fatalf("expected offset 10, got %s", got)
			}
			_, _ = io.WriteString(w, `[]`)
		case "/v1.0/alerts/alrt_123/archive":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			_, _ = io.WriteString(w, `{}`)
		case "/v1.0/webhooks/whk_123/test":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Options{Key: "chp_test_key", APIEndpoint: server.URL + "/v1.0/logs", ServicerEndpoint: server.URL + "/v1.0"})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close(context.Background())

	if _, err := client.GetEventLogs(context.Background(), "evt_123", "day", 10, 5); err != nil {
		t.Fatalf("GetEventLogs failed: %v", err)
	}
	if _, err := client.GetAlertDeliveries(context.Background(), "alrt_123", 20, 10, "test"); err != nil {
		t.Fatalf("GetAlertDeliveries failed: %v", err)
	}
	if _, err := client.ArchiveAlert(context.Background(), "alrt_123"); err != nil {
		t.Fatalf("ArchiveAlert failed: %v", err)
	}
	if err := client.TestWebhook(context.Background(), "whk_123"); err != nil {
		t.Fatalf("TestWebhook failed: %v", err)
	}
}

// RoundTrip implements the http.RoundTripper interface
func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

// setupMockEnvironment creates a test environment with a mock HTTP server and client.
// Returns the mock server, a channel for receiving logs, and a configured HTTP client.
func setupMockEnvironment() (*httptest.Server, chan []Log, *http.Client) {
	receivedLogs := make(chan []Log, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var entries []Log
		json.NewDecoder(r.Body).Decode(&entries)
		receivedLogs <- entries
		w.WriteHeader(http.StatusOK)
	}))

	mockRoundTripper := &mockRoundTripper{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			// Ensure the request is sent to the mock server
			req.URL.Host = strings.TrimPrefix(server.URL, "http://")
			return http.DefaultTransport.RoundTrip(req)
		},
	}

	mockClient := &http.Client{
		Transport: mockRoundTripper,
	}

	return server, receivedLogs, mockClient
}

// TestInitialize verifies the Initialize function works correctly and handles errors appropriately.
func TestInitialize(t *testing.T) {
	// Test the public Initialize function
	key := "chp_test_init_key"

	opts := Options{
		Key: key,
	}

	err := Initialize(opts)
	if err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if instance == nil {
		t.Fatal("Instance is nil after initialization")
	}

	// Test double initialization
	err = Initialize(opts)
	if err == nil {
		t.Fatal("Expected error on double initialization, got nil")
	}

	// Clean up
	Stop(context.Background())
}

// TestNewClient verifies client creation with various configurations.
func TestNewClient(t *testing.T) {
	withTempWorkdir := func(t *testing.T) string {
		t.Helper()
		tmpDir := t.TempDir()
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Failed to get cwd: %v", err)
		}
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Failed to change dir: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
		return tmpDir
	}

	unsetAPIKeyEnv := func(t *testing.T) {
		t.Helper()
		oldValue, hadValue := os.LookupEnv("CHIRPIER_API_KEY")
		_ = os.Unsetenv("CHIRPIER_API_KEY")
		t.Cleanup(func() {
			if hadValue {
				_ = os.Setenv("CHIRPIER_API_KEY", oldValue)
				return
			}
			_ = os.Unsetenv("CHIRPIER_API_KEY")
		})
	}

	t.Run("with valid options", func(t *testing.T) {
		key := "chp_test_valid_key"
		httpClient := &http.Client{}
		opts := Options{
			Key: key,
		}
		client, err := newClient(opts, httpClient)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if client == nil {
			t.Fatal("Expected client, got nil")
		}
		if client.apiKey != key {
			t.Errorf("Expected API key %s, got %s", key, client.apiKey)
		}
		if client.client != httpClient {
			t.Errorf("Expected http client %v, got %v", httpClient, client.client)
		}
		if client.apiEndpoint != "https://logs.chirpier.co/v1.0/logs" {
			t.Errorf("Expected default API endpoint to use /v1.0/logs, got %s", client.apiEndpoint)
		}
	})

	t.Run("with custom base URL", func(t *testing.T) {
		key := "chp_test_custom_endpoint_key"
		opts := Options{
			Key:         key,
			APIEndpoint: "https://localhost:3001/v1.0/logs",
		}

		client, err := newClient(opts, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client.apiEndpoint != "https://localhost:3001/v1.0/logs" {
			t.Errorf("Expected custom API endpoint to use /v1.0/logs, got %s", client.apiEndpoint)
		}
	})

	t.Run("with custom API endpoint", func(t *testing.T) {
		key := "chp_test_api_endpoint_key"
		opts := Options{
			Key:         key,
			APIEndpoint: "http://localhost:3002/v1.0/logs",
		}

		client, err := newClient(opts, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if client.apiEndpoint != "http://localhost:3002/v1.0/logs" {
			t.Errorf("Expected API endpoint override to be used, got '%s'", client.apiEndpoint)
		}
	})

	t.Run("with invalid API endpoint", func(t *testing.T) {
		opts := Options{
			Key:         "chp_test_api_endpoint_key",
			APIEndpoint: "not-a-url",
		}

		_, err := newClient(opts, nil)
		if err == nil {
			t.Fatal("Expected error for invalid API endpoint, got nil")
		}
		if !strings.Contains(err.Error(), "apiEndpoint must be a valid absolute URL") {
			t.Errorf("Expected API endpoint validation error, got '%s'", err.Error())
		}
	})

	t.Run("with missing API key", func(t *testing.T) {
		_ = withTempWorkdir(t)
		unsetAPIKeyEnv(t)

		opts := Options{}
		_, err := newClient(opts, nil)
		if err == nil {
			t.Fatal("Expected error for missing API key, got nil")
		}
		if err.Error() != "API key is required" {
			t.Errorf("Expected error message 'API key is required', got '%s'", err.Error())
		}
	})

	t.Run("with invalid API key prefix", func(t *testing.T) {
		opts := Options{
			Key: "invalid-key",
		}
		_, err := newClient(opts, nil)
		if err == nil {
			t.Fatal("Expected error for invalid API key, got nil")
		}
		if !strings.Contains(err.Error(), "must start with 'chp_'") {
			t.Errorf("Expected key format error, got '%s'", err.Error())
		}
	})

	t.Run("loads API key from .env", func(t *testing.T) {
		key := "chp_test_dotenv_key"
		tmpDir := withTempWorkdir(t)
		unsetAPIKeyEnv(t)

		envContent := "CHIRPIER_API_KEY=" + key + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0o600); err != nil {
			t.Fatalf("Failed to write .env: %v", err)
		}

		client, err := newClient(Options{}, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client.apiKey != key {
			t.Errorf("Expected API key from .env, got %s", client.apiKey)
		}
	})

	t.Run("uses env API key when options key missing", func(t *testing.T) {
		_ = withTempWorkdir(t)
		keyFromEnv := "chp_test_env_key"
		t.Setenv("CHIRPIER_API_KEY", keyFromEnv)

		client, err := newClient(Options{}, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client.apiKey != keyFromEnv {
			t.Errorf("Expected API key from environment, got %s", client.apiKey)
		}
	})

	t.Run("options key takes precedence over env and .env", func(t *testing.T) {
		tmpDir := withTempWorkdir(t)
		keyFromOptions := "chp_test_options_key"
		keyFromEnv := "chp_test_env_override_key"
		keyFromDotenv := "chp_test_dotenv_override_key"

		t.Setenv("CHIRPIER_API_KEY", keyFromEnv)
		envContent := "CHIRPIER_API_KEY=" + keyFromDotenv + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0o600); err != nil {
			t.Fatalf("Failed to write .env: %v", err)
		}

		client, err := newClient(Options{Key: keyFromOptions}, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client.apiKey != keyFromOptions {
			t.Errorf("Expected API key from options, got %s", client.apiKey)
		}
	})

	t.Run("env API key takes precedence over .env", func(t *testing.T) {
		tmpDir := withTempWorkdir(t)
		keyFromEnv := "chp_test_env_priority_key"
		keyFromDotenv := "chp_test_dotenv_priority_key"

		t.Setenv("CHIRPIER_API_KEY", keyFromEnv)
		envContent := "CHIRPIER_API_KEY=" + keyFromDotenv + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0o600); err != nil {
			t.Fatalf("Failed to write .env: %v", err)
		}

		client, err := newClient(Options{}, nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if client.apiKey != keyFromEnv {
			t.Errorf("Expected API key from env to win over .env, got %s", client.apiKey)
		}
	})
}

// TestLog verifies log functionality including batch and time-based flushing.
func TestMonitor(t *testing.T) {
	mockServer, receivedLogs, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	key := "chp_test_monitor_key"
	opts := Options{
		Key:         key,
		APIEndpoint: mockServer.URL,
	}
	err := initializeWithClient(opts, mockClient)
	if err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}
	defer Stop(context.Background())

	// Modify the client configuration
	mu.Lock()
	instance.batchSize = 2
	instance.flushDelay = 100 * time.Millisecond
	mu.Unlock()

	ctx := context.Background()

	t.Run("batch flush", func(t *testing.T) {
		// Set a smaller batch size for testing
		instance.batchSize = 2

		for i := 0; i < 2; i++ {
			err := instance.Log(ctx, Log{AgentID: uuid.New().String(), Event: "test", Value: float64(i)})
			if err != nil {
				t.Fatalf("Failed to log entry %d: %v", i+1, err)
			}
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 2 {
				t.Errorf("Expected 2 logs, got %d", len(entries))
			}
		case <-time.After(1 * time.Second): // Reduced timeout for faster tests
			t.Fatal("Timeout waiting for logs")
		}
	})

	t.Run("time-based flush", func(t *testing.T) {
		// Set a smaller flush delay for testing
		instance.flushDelay = 100 * time.Millisecond

		err := instance.Log(ctx, Log{AgentID: uuid.New().String(), Event: "test", Value: 42})
		if err != nil {
			t.Fatalf("Failed to log entry: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
		case <-time.After(200 * time.Millisecond): // Slightly longer than flush delay
			t.Fatal("Timeout waiting for log")
		}
	})

	t.Run("log without agent_id", func(t *testing.T) {
		err := instance.Log(ctx, Log{Event: "test-no-agent", Value: 7})
		if err != nil {
			t.Fatalf("Expected log without agent_id to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].AgentID != "" {
				t.Errorf("Expected empty agent_id, got %q", entries[0].AgentID)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log without agent_id")
		}
	})

	t.Run("log with whitespace agent_id omits field", func(t *testing.T) {
		err := instance.Log(ctx, Log{AgentID: "   ", Event: "test-whitespace-agent", Value: 11})
		if err != nil {
			t.Fatalf("Expected log with whitespace agent_id to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].AgentID != "" {
				t.Errorf("Expected whitespace-only agent_id to be omitted, got %q", entries[0].AgentID)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with whitespace agent_id")
		}
	})

	t.Run("log with meta payload", func(t *testing.T) {
		err := instance.Log(ctx, Log{
			AgentID: "api.worker",
			Event:   "request_finished",
			Value:   200,
			Meta: map[string]any{
				"path":   "/v1.0/logs",
				"status": "ok",
			},
		})
		if err != nil {
			t.Fatalf("Expected log with meta to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			meta, ok := entries[0].Meta.(map[string]any)
			if !ok {
				t.Fatalf("Expected meta to decode as map[string]any, got %T", entries[0].Meta)
			}
			if meta["path"] != "/v1.0/logs" {
				t.Errorf("Expected meta.path to be /v1.0/logs, got %v", meta["path"])
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with meta")
		}
	})

	t.Run("log with occurred_at", func(t *testing.T) {
		occurredAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		err := instance.Log(ctx, Log{
			AgentID:    "api.worker",
			Event:      "request_finished",
			Value:      200,
			OccurredAt: occurredAt,
		})
		if err != nil {
			t.Fatalf("Expected log with occurred_at to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].OccurredAt.UTC().Truncate(time.Second) != occurredAt {
				t.Errorf("Expected occurred_at %v, got %v", occurredAt, entries[0].OccurredAt)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with occurred_at")
		}
	})
}

// TestRetryRequest verifies the retry mechanism for failed requests.
func TestRetryRequest(t *testing.T) {
	attempts := 0
	mockServer, receivedLogs, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	// Override the mock server's handler for this test
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]Log{})
		}
	})

	key := "chp_test_retry_key"
	opts := Options{
		Key:         key,
		APIEndpoint: mockServer.URL,
	}
	client, err := newClient(opts, mockClient)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 3

	ctx := context.Background()
	err = client.retryRequest(ctx, func() error {
		return client.sendLogs(ctx, []Log{{AgentID: uuid.New().String(), Event: "test", Value: 42}})
	})

	if err != nil {
		t.Errorf("Expected no error after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	// Ensure no events were actually received
	select {
	case <-receivedLogs:
		t.Error("Unexpected log received")
	default:
		// This is the expected behavior
	}
}

// TestInvalidLog verifies error handling for invalid log data.
func TestInvalidEvent(t *testing.T) {
	key := "chp_test_invalid_log_key"
	httpClient := &http.Client{}
	opts := Options{
		Key: key,
	}
	client, err := newClient(opts, httpClient)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	ctx := context.Background()
	invalidEntry := Log{AgentID: "invalid-uuid", Event: "", Value: 0}

	// Set log level to debug to test logging
	debugLevel := LogLevelDebug
	client.logLevel = debugLevel

	err = client.Log(ctx, invalidEntry)
	if err == nil {
		t.Fatal("Expected error for invalid log, got nil")
	}

	// Verify the error message
	if !strings.Contains(err.Error(), "invalid log") {
		t.Errorf("Expected error message to contain 'invalid log', got: %s", err.Error())
	}

	err = client.Log(ctx, Log{AgentID: "plain-text-agent", Event: "valid-event", Value: 1})
	if err != nil {
		t.Fatalf("Expected plain-text agent_id to be accepted, got %v", err)
	}

	err = client.Log(ctx, Log{Event: "invalid-nan", Value: math.NaN()})
	if err == nil {
		t.Fatal("Expected error for non-finite value, got nil")
	}

	err = client.Log(ctx, Log{Event: "invalid-meta", Value: 1, Meta: make(chan int)})
	if err == nil {
		t.Fatal("Expected error for non-JSON-encodable meta, got nil")
	}

	err = client.Log(ctx, Log{Event: "too-old", Value: 1, OccurredAt: time.Now().UTC().Add(-31 * 24 * time.Hour)})
	if err == nil {
		t.Fatal("Expected error for occurred_at older than 30 days, got nil")
	}

	err = client.Log(ctx, Log{Event: "too-far-future", Value: 1, OccurredAt: time.Now().UTC().Add(25 * time.Hour)})
	if err == nil {
		t.Fatal("Expected error for occurred_at more than 1 day in the future, got nil")
	}
}

// TestContextCancellation verifies that monitoring respects context cancellation.
func TestContextCancellation(t *testing.T) {
	opts := Options{
		Key: "chp_test_ctx_cancel_key",
	}
	client, err := newClient(opts, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	// Use a channel to synchronize the goroutine
	done := make(chan struct{})
	var monitorErr error

	go func() {
		monitorErr = client.Log(ctx, Log{AgentID: uuid.New().String(), Event: "test", Value: 42})
		close(done)
	}()

	// Wait for the Monitor call to complete or timeout
	select {
	case <-done:
		if monitorErr == nil {
			t.Error("Expected error due to context cancellation, got nil")
		} else if monitorErr != context.Canceled {
			t.Errorf("Expected context.Canceled error, got: %v", monitorErr)
		}
	case <-time.After(1 * time.Second):
		t.Error("Test timed out")
	}
}

// TestStop verifies that the Stop function properly flushes remaining events.
func TestStop(t *testing.T) {
	mockServer, receivedLogs, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	t.Logf("Mock server URL: %s", mockServer.URL)

	key := "chp_test_stop_key"
	opts := Options{
		Key:         key,
		APIEndpoint: mockServer.URL,
	}
	err := initializeWithClient(opts, mockClient)
	if err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	// Modify the client configuration
	mu.Lock()
	instance.batchSize = 5
	instance.flushDelay = 100 * time.Millisecond
	t.Logf("Client configuration: apiEndpoint=%s, batchSize=%d, flushDelay=%v",
		instance.apiEndpoint, instance.batchSize, instance.flushDelay)
	mu.Unlock()

	ctx := context.Background()

	// Add some logs to the queue
	for i := 0; i < 3; i++ {
		err := LogEvent(ctx, Log{AgentID: uuid.New().String(), Event: "test", Value: float64(i)})
		if err != nil {
			t.Fatalf("Failed to log entry: %v", err)
		}
		t.Logf("Added log %d to queue", i)
	}

	t.Log("Calling Stop()")
	// Call Stop and verify that it flushes remaining events
	err = Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop client: %v", err)
	}
	t.Log("Stop() called")

	// Wait for logs with retries and longer timeout
	timeout := time.After(5 * time.Second)
	for {
		select {
		case entries := <-receivedLogs:
			t.Logf("Received %d logs", len(entries))
			if len(entries) != 3 {
				t.Errorf("Expected 3 logs to be flushed, got %d", len(entries))
			}
			return
		case <-timeout:
			t.Error("Timeout waiting for logs")
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestRunMethod verifies the background event processing functionality.
func TestRunMethod(t *testing.T) {
	mockServer, receivedLogs, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	key := "chp_test_run_key"
	opts := Options{
		Key:         key,
		APIEndpoint: mockServer.URL,
	}
	err := initializeWithClient(opts, mockClient)
	if err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	// Modify the client configuration
	mu.Lock()
	instance.batchSize = 2
	instance.flushDelay = 100 * time.Millisecond
	mu.Unlock()

	ctx := context.Background()

	// Test that logs are processed in the background
	totalLogs := 5
	for i := 0; i < totalLogs; i++ {
		err := LogEvent(ctx, Log{AgentID: uuid.New().String(), Event: "test", Value: float64(i)})
		if err != nil {
			t.Fatalf("Failed to log entry: %v", err)
		}
	}

	// Wait for logs to be processed
	receivedLogCount := 0
	timeout := time.After(5 * time.Second)

	for receivedLogCount < totalLogs {
		select {
		case entries := <-receivedLogs:
			receivedLogCount += len(entries)
			t.Logf("Received batch of %d logs, total now: %d", len(entries), receivedLogCount)
		case <-timeout:
			t.Fatalf("Timeout waiting for logs. Expected %d, got %d", totalLogs, receivedLogCount)
		}
	}

	// Stop the client after all events have been processed
	Stop(ctx)

	if receivedLogCount != totalLogs {
		t.Errorf("Expected %d logs, got %d", totalLogs, receivedLogCount)
	}
}

// TestFlushLogsEmpty verifies that flushing an empty log queue doesn't cause errors.
func TestFlushEventsEmpty(t *testing.T) {
	opts := Options{
		Key: "chp_test_flush_empty_key",
	}
	client, err := newClient(opts, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Flush empty queue
	client.flushLogs()

	// No error should occur
}

// TestSendLogsError verifies error handling when sending logs fails.
func TestSendEventsError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	opts := Options{
		Key:         "chp_test_send_error_key",
		APIEndpoint: mockServer.URL,
	}
	client, err := newClient(opts, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 1

	ctx := context.Background()
	entries := []Log{{AgentID: uuid.New().String(), Event: "test", Value: 42}}
	err = client.sendLogs(ctx, entries)

	if err == nil {
		t.Fatal("Expected error when sending logs, got nil")
	}
}

func TestStandaloneClientAPI(t *testing.T) {
	mockServer, receivedLogs, _ := setupMockEnvironment()
	defer mockServer.Close()

	client, err := NewClient(Options{
		Key:         "chp_test_standalone_client",
		APIEndpoint: mockServer.URL,
		BatchSize:   10,
		FlushDelay:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create standalone client: %v", err)
	}
	defer client.Close(context.Background())

	err = client.Log(context.Background(), Log{Event: "standalone.log", Value: 1})
	if err != nil {
		t.Fatalf("Failed to enqueue log: %v", err)
	}

	err = client.Flush(context.Background())
	if err != nil {
		t.Fatalf("Failed to flush standalone client: %v", err)
	}

	select {
	case entries := <-receivedLogs:
		if len(entries) != 1 {
			t.Fatalf("Expected 1 log, got %d", len(entries))
		}
		if entries[0].Event != "standalone.log" {
			t.Fatalf("Expected event standalone.log, got %s", entries[0].Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for flushed standalone log")
	}
}

func TestGlobalFlush(t *testing.T) {
	mockServer, receivedLogs, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	err := initializeWithClient(Options{
		Key:         "chp_test_global_flush",
		APIEndpoint: mockServer.URL,
		BatchSize:   10,
		FlushDelay:  10 * time.Second,
	}, mockClient)
	if err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}
	defer Stop(context.Background())

	err = LogEvent(context.Background(), Log{Event: "global.flush", Value: 1})
	if err != nil {
		t.Fatalf("Failed to enqueue log: %v", err)
	}

	err = Flush(context.Background())
	if err != nil {
		t.Fatalf("Failed to flush global client: %v", err)
	}

	select {
	case entries := <-receivedLogs:
		if len(entries) != 1 {
			t.Fatalf("Expected 1 log, got %d", len(entries))
		}
		if entries[0].Event != "global.flush" {
			t.Fatalf("Expected event global.flush, got %s", entries[0].Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for flushed global log")
	}
}
