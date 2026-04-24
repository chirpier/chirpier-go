// Package chirpier provides functionality for monitoring and tracking events.
package chirpier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
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
		case "/v1.0/events":
			if r.Method == http.MethodPost {
				_, _ = io.WriteString(w, `{"event_id":"evt_new","event":"tool.errors.count","public":false,"timezone":"UTC"}`)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		case "/v1.0/events/evt_123/analytics":
			if got := r.URL.Query().Get("view"); got != "window" {
				t.Fatalf("expected view window, got %s", got)
			}
			if got := r.URL.Query().Get("period"); got != "1h" {
				t.Fatalf("expected period 1h, got %s", got)
			}
			if got := r.URL.Query().Get("previous"); got != "previous_window" {
				t.Fatalf("expected previous previous_window, got %s", got)
			}
			_, _ = io.WriteString(w, `{"event_id":"evt_123","view":"window","period":"1h","previous":"previous_window","data":null}`)
		case "/v1.0/events/evt_123":
			_, _ = io.WriteString(w, `{"event_id":"evt_123","event":"tool.errors.count","public":false,"timezone":"UTC"}`)
		case "/v1.0/policies/pol_123":
			if r.Method == http.MethodPut {
				_, _ = io.WriteString(w, `{"policy_id":"pol_123","event_id":"evt_123","title":"Updated","channel":"ops","period":"hour","aggregate":"sum","condition":"gt","threshold":1,"severity":"warning","enabled":true}`)
				return
			}
			_, _ = io.WriteString(w, `{"policy_id":"pol_123","event_id":"evt_123","title":"Policy","channel":"ops","period":"hour","aggregate":"sum","condition":"gt","threshold":1,"severity":"warning","enabled":true}`)
		case "/v1.0/alerts/alrt_123":
			_, _ = io.WriteString(w, `{"alert_id":"alrt_123","policy_id":"pol_123","event_id":"evt_123","event":"tool.errors.count","title":"Alert","channel":"ops","period":"hour","aggregate":"sum","condition":"gt","threshold":1,"severity":"warning","status":"triggered","value":2,"count":2,"min":1,"max":1}`)
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
		case "/v1.0/destinations":
			if r.Method == http.MethodPost {
				_, _ = io.WriteString(w, `{"destination_id":"dst_123","channel":"slack","scope":"all","enabled":true}`)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		case "/v1.0/destinations/dst_123":
			if r.Method == http.MethodPut {
				_, _ = io.WriteString(w, `{"destination_id":"dst_123","channel":"slack","scope":"all","enabled":false}`)
				return
			}
			_, _ = io.WriteString(w, `{"destination_id":"dst_123","channel":"slack","scope":"all","enabled":true}`)
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
		case "/v1.0/destinations/whk_123/test":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			_, _ = io.WriteString(w, `{"alert_id":"alrt_test_123","destination_id":"whk_123","status":"sent"}`)
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
	if createdEvent, err := client.CreateEvent(context.Background(), CreateEventPayload{Event: "tool.errors.count"}); err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	} else if createdEvent.EventID != "evt_new" {
		t.Fatalf("expected event id evt_new, got %s", createdEvent.EventID)
	}
	if eventDef, err := client.GetEvent(context.Background(), "evt_123"); err != nil {
		t.Fatalf("GetEvent failed: %v", err)
	} else if eventDef.EventID != "evt_123" {
		t.Fatalf("expected event id evt_123, got %s", eventDef.EventID)
	}
	if analytics, err := client.GetEventAnalytics(context.Background(), "evt_123", AnalyticsWindowQuery{View: "window", Period: "1h", Previous: "previous_window"}); err != nil {
		t.Fatalf("GetEventAnalytics failed: %v", err)
	} else if analytics.EventID != "evt_123" {
		t.Fatalf("expected event id evt_123, got %s", analytics.EventID)
	}
	if policy, err := client.GetPolicy(context.Background(), "pol_123"); err != nil {
		t.Fatalf("GetPolicy failed: %v", err)
	} else if policy.PolicyID != "pol_123" {
		t.Fatalf("expected policy id pol_123, got %s", policy.PolicyID)
	}
	if updatedPolicy, err := client.UpdatePolicy(context.Background(), "pol_123", Policy{Title: "Updated"}); err != nil {
		t.Fatalf("UpdatePolicy failed: %v", err)
	} else if updatedPolicy.Title != "Updated" {
		t.Fatalf("expected updated title, got %s", updatedPolicy.Title)
	}
	if alert, err := client.GetAlert(context.Background(), "alrt_123"); err != nil {
		t.Fatalf("GetAlert failed: %v", err)
	} else if alert.AlertID != "alrt_123" {
		t.Fatalf("expected alert id alrt_123, got %s", alert.AlertID)
	}
	if _, err := client.GetAlertDeliveries(context.Background(), "alrt_123", 20, 10, "test"); err != nil {
		t.Fatalf("GetAlertDeliveries failed: %v", err)
	}
	if _, err := client.ListDestinations(context.Background()); err != nil {
		t.Fatalf("ListDestinations failed: %v", err)
	}
	if destination, err := client.CreateDestination(context.Background(), Destination{Channel: "slack", Scope: "all", Enabled: true}); err != nil {
		t.Fatalf("CreateDestination failed: %v", err)
	} else if destination.DestinationID != "dst_123" {
		t.Fatalf("expected destination id dst_123, got %s", destination.DestinationID)
	}
	if destination, err := client.GetDestination(context.Background(), "dst_123"); err != nil {
		t.Fatalf("GetDestination failed: %v", err)
	} else if destination.DestinationID != "dst_123" {
		t.Fatalf("expected destination id dst_123, got %s", destination.DestinationID)
	}
	if destination, err := client.UpdateDestination(context.Background(), "dst_123", Destination{Enabled: false}); err != nil {
		t.Fatalf("UpdateDestination failed: %v", err)
	} else if destination.Enabled {
		t.Fatal("expected destination to be disabled")
	}
	if _, err := client.ArchiveAlert(context.Background(), "alrt_123"); err != nil {
		t.Fatalf("ArchiveAlert failed: %v", err)
	}
	if result, err := client.TestDestination(context.Background(), "whk_123"); err != nil {
		t.Fatalf("TestDestination failed: %v", err)
	} else if result.AlertID != "alrt_test_123" {
		t.Fatalf("expected alert id alrt_test_123, got %s", result.AlertID)
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
			err := instance.Log(ctx, Log{Agent: uuid.New().String(), Event: "test", Value: float64(i)})
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

		err := instance.Log(ctx, Log{Agent: uuid.New().String(), Event: "test", Value: 42})
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

	t.Run("log without agent", func(t *testing.T) {
		err := instance.Log(ctx, Log{Event: "test-no-agent", Value: 7})
		if err != nil {
			t.Fatalf("Expected log without agent to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].Agent != "" {
				t.Errorf("Expected empty agent, got %q", entries[0].Agent)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log without agent")
		}
	})

	t.Run("log with whitespace agent omits field", func(t *testing.T) {
		err := instance.Log(ctx, Log{Agent: "   ", Event: "test-whitespace-agent", Value: 11})
		if err != nil {
			t.Fatalf("Expected log with whitespace agent to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].Agent != "" {
				t.Errorf("Expected whitespace-only agent to be omitted, got %q", entries[0].Agent)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with whitespace agent")
		}
	})

	t.Run("log with meta payload", func(t *testing.T) {
		err := instance.Log(ctx, Log{
			Agent: "api.worker",
			Event: "request_finished",
			Value: 200,
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
			LogID:      uuid.New().String(),
			Agent:      "api.worker",
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

	t.Run("log generates log_id when omitted", func(t *testing.T) {
		err := instance.Log(ctx, Log{Event: "generated-log-id", Value: 1})
		if err != nil {
			t.Fatalf("Expected log without log_id to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			parsed, err := uuid.Parse(entries[0].LogID)
			if err != nil {
				t.Fatalf("Expected generated log_id to be a UUID, got %q", entries[0].LogID)
			}
			if parsed.Version() != 7 {
				t.Fatalf("Expected generated log_id to be UUIDv7, got version %d", parsed.Version())
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with generated log_id")
		}
	})

	t.Run("log preserves provided log_id", func(t *testing.T) {
		logID := uuid.New().String()
		err := instance.Log(ctx, Log{LogID: logID, Event: "provided-log-id", Value: 1})
		if err != nil {
			t.Fatalf("Expected log with log_id to succeed, got: %v", err)
		}

		select {
		case entries := <-receivedLogs:
			if len(entries) != 1 {
				t.Errorf("Expected 1 log, got %d", len(entries))
			}
			if entries[0].LogID != logID {
				t.Fatalf("Expected log_id %q, got %q", logID, entries[0].LogID)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("Timeout waiting for log with provided log_id")
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
			w.WriteHeader(http.StatusBadGateway)
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
		return client.sendLogs(ctx, []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}})
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
	invalidEntry := Log{Agent: "invalid-uuid", Event: "", Value: 0}

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

	err = client.Log(ctx, Log{Agent: "plain-text-agent", Event: "valid-event", Value: 1})
	if err != nil {
		t.Fatalf("Expected plain-text agent to be accepted, got %v", err)
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

	err = client.Log(ctx, Log{LogID: "not-a-uuid", Event: "invalid-log-id", Value: 1})
	if err == nil {
		t.Fatal("Expected error for invalid log_id, got nil")
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
		monitorErr = client.Log(ctx, Log{Agent: uuid.New().String(), Event: "test", Value: 42})
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
		err := LogEvent(ctx, Log{Agent: uuid.New().String(), Event: "test", Value: float64(i)})
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
		err := LogEvent(ctx, Log{Agent: uuid.New().String(), Event: "test", Value: float64(i)})
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
	entries := []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}}
	err = client.sendLogs(ctx, entries)

	if err == nil {
		t.Fatal("Expected error when sending logs, got nil")
	}
}

func TestSendLogsUnauthorizedDoesNotRetry(t *testing.T) {
	requestCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "invalid api key")
	}))
	defer mockServer.Close()

	client, err := newClient(Options{
		Key:         "chp_test_unauthorized_key",
		APIEndpoint: mockServer.URL,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 5

	err = client.sendLogs(context.Background(), []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}})
	if err == nil {
		t.Fatal("Expected unauthorized error when sending logs, got nil")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("Expected unauthorized error to include body text, got %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("Expected exactly 1 request for unauthorized response, got %d", requestCount)
	}
}

func TestSendLogsForbiddenDoesNotRetryAndLogsBody(t *testing.T) {
	requestCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "forbidden project")
	}))
	defer mockServer.Close()

	client, err := newClient(Options{
		Key:         "chp_test_forbidden_log_key",
		APIEndpoint: mockServer.URL,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.logLevel = LogLevelInfo

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalWriter)

	err = client.sendLogs(context.Background(), []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}})
	if err == nil {
		t.Fatal("Expected forbidden error when sending logs, got nil")
	}
	if requestCount != 1 {
		t.Fatalf("Expected exactly 1 request for forbidden response, got %d", requestCount)
	}
	if !strings.Contains(err.Error(), "forbidden project") {
		t.Fatalf("Expected forbidden error to include body text, got %v", err)
	}
	if !strings.Contains(buf.String(), "Chirpier API returned 403") || !strings.Contains(buf.String(), "forbidden project") {
		t.Fatalf("Expected forbidden log message to include response body, got %q", buf.String())
	}
}

func TestSendLogsStatusPolicy(t *testing.T) {
	testCases := []struct {
		name         string
		status       int
		body         string
		retries      int
		wantRequests int
		wantBody     bool
	}{
		{name: "not found is non retryable", status: http.StatusNotFound, retries: 5, wantRequests: 1},
		{name: "internal server error is non retryable", status: http.StatusInternalServerError, retries: 5, wantRequests: 1},
		{name: "service unavailable is non retryable", status: http.StatusServiceUnavailable, retries: 5, wantRequests: 1},
		{name: "bad gateway retries", status: http.StatusBadGateway, retries: 2, wantRequests: 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requestCount := 0
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = io.WriteString(w, tc.body)
				}
			}))
			defer mockServer.Close()

			client, err := newClient(Options{
				Key:         "chp_test_status_policy_key",
				APIEndpoint: mockServer.URL,
			}, nil)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}
			client.retries = tc.retries

			err = client.sendLogs(context.Background(), []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}})
			if err == nil {
				t.Fatal("Expected sendLogs error, got nil")
			}
			if requestCount != tc.wantRequests {
				t.Fatalf("Expected %d requests, got %d", tc.wantRequests, requestCount)
			}
			if tc.wantBody && !strings.Contains(err.Error(), tc.body) {
				t.Fatalf("Expected error to include body text %q, got %v", tc.body, err)
			}
		})
	}
}

func TestSendLogsRateLimitedRetriesWithDelay(t *testing.T) {
	requestCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer mockServer.Close()

	client, err := newClient(Options{
		Key:         "chp_test_rate_limit_key",
		APIEndpoint: mockServer.URL,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	client.retries = 2

	err = client.sendLogs(context.Background(), []Log{{Agent: uuid.New().String(), Event: "test", Value: 42}})
	if err == nil {
		t.Fatal("Expected rate limit error, got nil")
	}
	if requestCount != 3 {
		t.Fatalf("Expected 3 requests for rate limited response, got %d", requestCount)
	}
}

func TestFlushDropsNonRetryableBatches(t *testing.T) {
	statuses := []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}

	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requestCount := 0
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				w.WriteHeader(status)
				if status == http.StatusUnauthorized || status == http.StatusForbidden {
					_, _ = io.WriteString(w, "chirpier auth failure")
				}
			}))
			defer mockServer.Close()

			client, err := NewClient(Options{
				Key:         "chp_test_non_retryable_drop_key",
				APIEndpoint: mockServer.URL,
				BatchSize:   1,
				FlushDelay:  10 * time.Second,
				MaxWorkers:  1,
			})
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}
			defer client.Close(context.Background())
			client.retries = 5

			if err := client.Log(context.Background(), Log{Event: "non.retryable.drop", Value: 1}); err != nil {
				t.Fatalf("Failed to enqueue log: %v", err)
			}

			if err := client.Flush(context.Background()); err != nil {
				t.Fatalf("Expected flush to complete after dropping non-retryable batch, got %v", err)
			}

			timeout := time.After(1 * time.Second)
			for requestCount != 1 {
				select {
				case <-timeout:
					t.Fatalf("Expected exactly 1 request for %d batch, got %d", status, requestCount)
				default:
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !client.isIdle() {
				t.Fatalf("Expected client to be idle after dropping non-retryable status %d", status)
			}
		})
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

func TestFlushRespectsBatchSize(t *testing.T) {
	receivedLogs := make(chan []Log, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var entries []Log
		if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		receivedLogs <- entries
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(Options{
		Key:         "chp_test_batch_size_limit",
		APIEndpoint: server.URL,
		BatchSize:   10,
		FlushDelay:  10 * time.Second,
		MaxWorkers:  1,
	})
	if err != nil {
		t.Fatalf("Failed to create standalone client: %v", err)
	}
	defer client.Close(context.Background())

	for i := 0; i < 25; i++ {
		err = client.Log(context.Background(), Log{Event: "batch.limit", Value: float64(i)})
		if err != nil {
			t.Fatalf("Failed to enqueue log %d: %v", i, err)
		}
	}

	err = client.Flush(context.Background())
	if err != nil {
		t.Fatalf("Failed to flush standalone client: %v", err)
	}

	totalLogs := 0
	batchCount := 0
	timeout := time.After(2 * time.Second)

	for totalLogs < 25 {
		select {
		case entries := <-receivedLogs:
			batchCount++
			if len(entries) > 10 {
				t.Fatalf("Expected batch size <= 10, got %d", len(entries))
			}
			totalLogs += len(entries)
		case <-timeout:
			t.Fatalf("Timeout waiting for flushed batches; received %d logs across %d batches", totalLogs, batchCount)
		}
	}

	if totalLogs != 25 {
		t.Fatalf("Expected 25 logs, got %d", totalLogs)
	}

	if batchCount < 3 {
		t.Fatalf("Expected at least 3 batches, got %d", batchCount)
	}
}

func TestLogBlocksUntilBufferAvailable(t *testing.T) {
	client, err := newClient(Options{
		Key:        "chp_test_blocking_log",
		BufferSize: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.Log(context.Background(), Log{Event: "first", Value: 1}); err != nil {
		t.Fatalf("Failed to enqueue first log: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Log(context.Background(), Log{Event: "second", Value: 2})
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Expected second log to block, returned early with %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case <-client.logChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out draining first log from buffer")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Expected second log to enqueue after buffer drained: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for blocked log to enqueue")
	}

	select {
	case entry := <-client.logChan:
		if entry.Event != "second" {
			t.Fatalf("Expected second log in buffer, got %s", entry.Event)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out reading second log from buffer")
	}
}

func TestLogReturnsContextErrorWhenBufferStaysFull(t *testing.T) {
	client, err := newClient(Options{
		Key:        "chp_test_blocking_log_ctx",
		BufferSize: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.Log(context.Background(), Log{Event: "first", Value: 1}); err != nil {
		t.Fatalf("Failed to enqueue first log: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = client.Log(ctx, Log{Event: "second", Value: 2})
	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("Expected context deadline exceeded, got %v", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatalf("Expected Log to wait for buffer space before failing, returned too quickly in %v", time.Since(start))
	}
}
