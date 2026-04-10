# Chirpier SDK

The Chirpier SDK for Go emits OpenClaw-friendly flat events to Chirpier/Ingres and can also manage events, policies, alerts, and destinations.

## Features

- Easy-to-use API for sending logs to Chirpier
- Automatic batching of logs for improved performance
- Automatic retry mechanism with exponential backoff
- Thread-safe operations
- Periodic flushing of the log queue

## Installation

Install Chirpier SDK using `go get`:

<!-- docs:start install -->
```bash
go get github.com/chirpier/chirpier-go
```
<!-- docs:end install -->

## Getting Started

To start using the SDK, you need to initialize it with your API key.
If `Key` is not provided, key resolution order is:

1. `CHIRPIER_API_KEY` from process environment
2. `CHIRPIER_API_KEY` loaded from local `.env` file

Here's a quick OpenClaw-oriented example:

<!-- docs:start quickstart -->
```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/chirpier/chirpier-go"
)

func main() {
    // Initialize the Chirpier client
    err := chirpier.Initialize(chirpier.Options{})
    if err != nil {
        fmt.Printf("Error initializing Chirpier: %v\n", err)
        return
    }

    // Send OpenClaw events
	 err = chirpier.LogEvent(
	     context.Background(),
	     chirpier.Log{
	         LogID:   "9f97d65f-fb30-4062-b4d0-8617c03fe4f6",
	         Agent: "openclaw.main",
	         Event:   "tool.errors.count",
	         Value:   1,
            Meta: map[string]any{
                "tool_name": "browser.open",
            },
        },
    )
    if err != nil {
        fmt.Printf("Error logging entry: %v\n", err)
        return
    }

    // Create a context with timeout to ensure proper flush/shutdown
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    _ = chirpier.Flush(ctx)
    _ = chirpier.Stop(ctx)
}
```
<!-- docs:end quickstart -->

## API Reference

### Initialize

Initialize the Chirpier client with your API key. Find your API key in the Chirpier Integration page.

```go
err := chirpier.Initialize(chirpier.Options{
    Key: "your-api-key",
    APIEndpoint: "https://logs.chirpier.co/v1.0/logs",
    ServicerEndpoint: "https://api.chirpier.co/v1.0",
})
```

- `your-api-key` (str): Your Chirpier integration key
- `CHIRPIER_API_KEY` (env/.env, optional): used when `Key` is omitted
- `APIEndpoint` (str, optional): Override the full ingestion endpoint
- `ServicerEndpoint` (str, optional): Override the control-plane endpoint; defaults to `https://api.chirpier.co/v1.0`

API keys must be bearer tokens that start with `chp_`.
The same bearer token works for ingest and servicer APIs.

### NewClient (Recommended)

Create a standalone client instance instead of using global state.

```go
client, err := chirpier.NewClient(chirpier.Options{Key: "chp_your_api_key"})
if err != nil {
    return err
}
defer client.Close(context.Background())

_ = client.Log(context.Background(), chirpier.Log{Agent: "openclaw.main", Event: "task.duration_ms", Value: 420})
_ = client.Flush(context.Background())
```

### Log

All logs emitted to Chirpier must be of type `Log`.

```go
entry := chirpier.Log{
    Agent: "openclaw.main",
    Event:   "task.duration_ms",
    Value:   780,
    OccurredAt: time.Now().UTC(),
    Meta: map[string]any{
        "task_name": "daily_digest",
        "result":    "success",
    },
}
```

- `agent` (str, optional): Free-form agent identifier text
- `log_id` (uuid, optional): Idempotency key for the log; generated automatically when omitted
- `event` (str): Name of the event
- `value` (float): Numeric value to record
- `occurred_at` (timestamp, optional): Event occurrence time in UTC
- `meta` (json, optional): Additional JSON-encodable metadata for the log

`agent` is optional. Whitespace-only values are treated as omitted.
`log_id` is optional. If omitted or blank, the SDK generates a UUIDv4 automatically.
`occurred_at` is optional. If provided, it must be at most 30 days in the past and 1 day in the future.
Use RFC3339/ISO8601 UTC timestamps (for example, `2026-03-05T14:30:00Z`).
Unknown events are auto-created in Ingres as event definitions.

### Event Definitions

Use the same client and bearer token to manage event definitions:

<!-- docs:start common-tasks -->
```go
events, err := client.ListEvents(ctx)
created, err := client.CreateEvent(ctx, chirpier.CreateEventPayload{Event: "tool.errors.count"})
eventDef, err := client.GetEvent(ctx, events[0].EventID)
updated, err := client.UpdateEvent(ctx, eventDef.EventID, map[string]any{
    "title": "OpenClaw Tool Errors",
})
analytics, err := client.GetEventAnalytics(ctx, eventDef.EventID, chirpier.AnalyticsWindowQuery{
    View:     "window",
    Period:   "1h",
    Previous: "previous_window",
})
_, _, _, _ = created, updated, analytics, err
```

### Policies And Alerts

```go
policies, err := client.ListPolicies(ctx)
policy, err := client.GetPolicy(ctx, "pol_123")
updatedPolicy, err := client.UpdatePolicy(ctx, "pol_123", chirpier.Policy{Title: "Updated"})
createdPolicy, err := client.CreatePolicy(ctx, chirpier.Policy{
    EventID:   "evt_123",
    Title:     "OpenClaw tool errors spike",
    Condition: "gt",
    Threshold: 5,
    Enabled:   true,
    Period:    "hour",
    Aggregate: "sum",
})
alerts, err := client.ListAlerts(ctx, "triggered")
alert, err := client.GetAlert(ctx, alerts[0].AlertID)
deliveries, err := client.GetAlertDeliveries(ctx, alerts[0].AlertID, 20, 0, "alert")
rollups, err := client.GetEventLogs(ctx, "evt_123", "hour", 25, 0)
acknowledged, err := client.AcknowledgeAlert(ctx, alerts[0].AlertID)
resolved, err := client.ResolveAlert(ctx, acknowledged.AlertID)
archived, err := client.ArchiveAlert(ctx, resolved.AlertID)
destinations, err := client.ListDestinations(ctx)
destination, err := client.CreateDestination(ctx, chirpier.Destination{Channel: "slack", URL: "https://hooks.slack.com/services/T000/B000/secret", Scope: "all", PolicyIDs: []string{}, Enabled: true})
destinationDetails, err := client.GetDestination(ctx, destination.DestinationID)
updatedDestination, err := client.UpdateDestination(ctx, destination.DestinationID, chirpier.Destination{Enabled: false})
testResult, err := client.TestDestination(ctx, destination.DestinationID)
testDeliveries, err := client.GetAlertDeliveries(ctx, testResult.AlertID, 20, 0, "test")
_, _, _, _, _, _, _, _, _, _, _, _, _ = policies, policy, updatedPolicy, createdPolicy, alerts, alert, deliveries, rollups, archived, destinations, destinationDetails, updatedDestination, testDeliveries
```
<!-- docs:end common-tasks -->

### LogEvent

Send a log to Chirpier using the `LogEvent` function.

```go
err = chirpier.LogEvent(ctx, entry)
```

Example with `occurred_at`:

```go
err = chirpier.LogEvent(ctx, chirpier.Log{
    Agent:    "openclaw.main",
    Event:      "heartbeat.missed.count",
    Value:      1,
    OccurredAt: time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC),
})
```

### Flush

Flush queued logs for the global initialized client.

```go
err = chirpier.Flush(ctx)
```

### Close / Stop

For standalone clients, call `client.Close(ctx)`.
For global SDK usage, call `chirpier.Stop(ctx)`.

## Destination Setup Examples

Create a Slack destination for OpenClaw alerts:

```go
destination, err := client.CreateDestination(ctx, chirpier.Destination{
    Channel:   "slack",
    URL:       "https://hooks.slack.com/services/T000/B000/secret",
    Scope:     "all",
    PolicyIDs: []string{},
    Enabled:   true,
})
```

Create a Telegram destination for OpenClaw alerts:

```go
destination, err := client.CreateDestination(ctx, chirpier.Destination{
    Channel:   "telegram",
    Scope:     "all",
    PolicyIDs: []string{},
    Enabled:   true,
    Credentials: map[string]any{
        "bot_token": "123456:telegram-bot-token",
        "chat_id":   "987654321",
    },
})
```

Send a test notification:

```go
testResult, err := client.TestDestination(ctx, destination.DestinationID)
deliveries, err := client.GetAlertDeliveries(ctx, testResult.AlertID, 20, 0, "test")
```

## Test

Run the test suite to ensure everything works as expected:

```bash
go test -v
```

## Contributing

We welcome contributions! To contribute:

1. Fork this repository.
2. Create a new branch for your feature or bug fix.
3. Submit a pull request with a clear explanation of your changes.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

If you encounter any problems or have any questions, please open an issue on the GitHub repository or contact us at <contact@chirpier.co>.

---

Start tracking your events seamlessly with Chirpier SDK!
