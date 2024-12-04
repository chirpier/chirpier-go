// Package chirpier provides functionality for monitoring and tracking events.
package chirpier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mockRoundTripper is a custom http.RoundTripper for mocking HTTP requests
type mockRoundTripper struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

// RoundTrip implements the http.RoundTripper interface
func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

// setupMockEnvironment creates a test environment with a mock HTTP server and client.
// Returns the mock server, a channel for receiving events, and a configured HTTP client.
func setupMockEnvironment() (*httptest.Server, chan []Event, *http.Client) {
	receivedEvents := make(chan []Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var events []Event
		json.NewDecoder(r.Body).Decode(&events)
		receivedEvents <- events
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

	return server, receivedEvents, mockClient
}

// TestInitialize verifies the Initialize function works correctly and handles errors appropriately.
func TestInitialize(t *testing.T) {
	// Test the public Initialize function
	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTUxNjIzOTAyMn0.drt_po6bHhDOF3MRkcEZdUERaj-gGDAEVLslkdSRXoQ"

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
	t.Run("with valid options", func(t *testing.T) {
		key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
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
	})

	t.Run("with missing API key", func(t *testing.T) {
		opts := Options{}
		_, err := newClient(opts, nil)
		if err == nil {
			t.Fatal("Expected error for missing API key, got nil")
		}
		if err.Error() != "API key is required" {
			t.Errorf("Expected error message 'API key is required', got '%s'", err.Error())
		}
	})

	t.Run("with invalid JWT", func(t *testing.T) {
		opts := Options{
			Key: "invalid-jwt",
		}
		_, err := newClient(opts, nil)
		if err == nil {
			t.Fatal("Expected error for invalid JWT, got nil")
		}
		if !strings.Contains(err.Error(), "Invalid API key") {
			t.Errorf("Expected error message to contain 'Invalid API key', got '%s'", err.Error())
		}
	})
}

// TestMonitor verifies event monitoring functionality including batch and time-based flushing.
func TestMonitor(t *testing.T) {
	mockServer, receivedEvents, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
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
			err := instance.Monitor(ctx, Event{GroupID: uuid.New().String(), StreamName: "test", Value: float64(i)})
			if err != nil {
				t.Fatalf("Failed to monitor event%d: %v", i+1, err)
			}
		}

		select {
		case events := <-receivedEvents:
			if len(events) != 2 {
				t.Errorf("Expected 2 events, got %d", len(events))
			}
		case <-time.After(1 * time.Second): // Reduced timeout for faster tests
			t.Fatal("Timeout waiting for events")
		}
	})

	t.Run("time-based flush", func(t *testing.T) {
		// Set a smaller flush delay for testing
		instance.flushDelay = 100 * time.Millisecond

		err := instance.Monitor(ctx, Event{GroupID: uuid.New().String(), StreamName: "test", Value: 42})
		if err != nil {
			t.Fatalf("Failed to monitor event: %v", err)
		}

		select {
		case events := <-receivedEvents:
			if len(events) != 1 {
				t.Errorf("Expected 1 event, got %d", len(events))
			}
		case <-time.After(200 * time.Millisecond): // Slightly longer than flush delay
			t.Fatal("Timeout waiting for event")
		}
	})
}

// TestRetryRequest verifies the retry mechanism for failed requests.
func TestRetryRequest(t *testing.T) {
	attempts := 0
	mockServer, receivedEvents, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	// Override the mock server's handler for this test
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]Event{})
		}
	})

	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	opts := Options{
		Key:         key,
		APIEndpoint: mockServer.URL,
	}
	client, err := newClient(opts, mockClient)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 3

	err = client.retryRequest(func() error {
		return client.sendEvents([]Event{{GroupID: uuid.New().String(), StreamName: "test", Value: 42}})
	})

	if err != nil {
		t.Errorf("Expected no error after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	// Ensure no events were actually received
	select {
	case <-receivedEvents:
		t.Error("Unexpected event received")
	default:
		// This is the expected behavior
	}
}

// TestInvalidEvent verifies error handling for invalid event data.
func TestInvalidEvent(t *testing.T) {
	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	httpClient := &http.Client{}
	opts := Options{
		Key: key,
	}
	client, err := newClient(opts, httpClient)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	invalidEvent := Event{GroupID: "invalid-uuid", StreamName: "", Value: 0}

	err = client.Monitor(ctx, invalidEvent)
	if err == nil {
		t.Fatal("Expected error for invalid event, got nil")
	}

	expectedError := "Invalid event format. Must include group_id, stream, and numeric value"

	if err.Error() != expectedError {
		t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
	}
}

// TestContextCancellation verifies that monitoring respects context cancellation.
func TestContextCancellation(t *testing.T) {
	opts := Options{
		Key: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTUxNjIzOTAyMn0.drt_po6bHhDOF3MRkcEZdUERaj-gGDAEVLslkdSRXoQ",
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
		monitorErr = client.Monitor(ctx, Event{GroupID: uuid.New().String(), StreamName: "test", Value: 42})
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
	mockServer, receivedEvents, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	t.Logf("Mock server URL: %s", mockServer.URL)

	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTUxNjIzOTAyMn0.drt_po6bHhDOF3MRkcEZdUERaj-gGDAEVLslkdSRXoQ"
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

	// Add some events to the queue
	for i := 0; i < 3; i++ {
		err := Monitor(ctx, Event{GroupID: uuid.New().String(), StreamName: "test", Value: float64(i)})
		if err != nil {
			t.Fatalf("Failed to monitor event: %v", err)
		}
		t.Logf("Added event %d to queue", i)
	}

	t.Log("Calling Stop()")
	// Call Stop and verify that it flushes remaining events
	err = Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop client: %v", err)
	}
	t.Log("Stop() called")

	// Wait for events with retries and longer timeout
	timeout := time.After(5 * time.Second)
	for {
		select {
		case events := <-receivedEvents:
			t.Logf("Received %d events", len(events))
			if len(events) != 3 {
				t.Errorf("Expected 3 events to be flushed, got %d", len(events))
			}
			return
		case <-timeout:
			t.Error("Timeout waiting for events")
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestRunMethod verifies the background event processing functionality.
func TestRunMethod(t *testing.T) {
	mockServer, receivedEvents, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
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

	// Test that events are processed in the background
	totalEvents := 5
	for i := 0; i < totalEvents; i++ {
		err := Monitor(ctx, Event{GroupID: uuid.New().String(), StreamName: "test", Value: float64(i)})
		if err != nil {
			t.Fatalf("Failed to monitor event: %v", err)
		}
	}

	// Wait for events to be processed
	receivedEventCount := 0
	timeout := time.After(5 * time.Second)

	for receivedEventCount < totalEvents {
		select {
		case events := <-receivedEvents:
			receivedEventCount += len(events)
			t.Logf("Received batch of %d events, total now: %d", len(events), receivedEventCount)
		case <-timeout:
			t.Fatalf("Timeout waiting for events. Expected %d, got %d", totalEvents, receivedEventCount)
		}
	}

	// Stop the client after all events have been processed
	Stop(ctx)

	if receivedEventCount != totalEvents {
		t.Errorf("Expected %d events, got %d", totalEvents, receivedEventCount)
	}
}

// TestQueueEvent verifies the event queueing functionality.
func TestQueueEvent(t *testing.T) {
	mockServer, _, mockClient := setupMockEnvironment()
	defer mockServer.Close()

	key := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
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

	// Queue events
	for i := 0; i < 5; i++ {
		instance.queueEvent(Event{GroupID: uuid.New().String(), StreamName: "test", Value: float64(i)})
	}

	if len(instance.eventQueue) != 5 {
		t.Errorf("Expected 5 events in queue after flushing, got %d", len(instance.eventQueue))
	}
}

// TestFlushEventsEmpty verifies that flushing an empty event queue doesn't cause errors.
func TestFlushEventsEmpty(t *testing.T) {
	opts := Options{
		Key: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTUxNjIzOTAyMn0.drt_po6bHhDOF3MRkcEZdUERaj-gGDAEVLslkdSRXoQ",
	}
	client, err := newClient(opts, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Flush empty queue
	client.flushEvents()

	// No error should occur
}

// TestSendEventsError verifies error handling when sending events fails.
func TestSendEventsError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	opts := Options{
		Key:         "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTUxNjIzOTAyMn0.drt_po6bHhDOF3MRkcEZdUERaj-gGDAEVLslkdSRXoQ",
		APIEndpoint: mockServer.URL,
	}
	client, err := newClient(opts, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 1

	events := []Event{{GroupID: uuid.New().String(), StreamName: "test", Value: 42}}
	err = client.sendEvents(events)

	if err == nil {
		t.Fatal("Expected error when sending events, got nil")
	}
}
