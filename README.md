# Chirpier SDK

The Chirpier SDK for Go is a simple, lightweight, and efficient SDK to emit event data to Chirpier direct from your Go applications.

## Features

- Easy-to-use API for sending events to Chirpier
- Automatic batching of events for improved performance
- Automatic retry mechanism with exponential backoff
- Thread-safe operations
- Periodic flushing of the event queue

## Installation

Install Chirpier SDK using `go get`:

```bash
go get github.com/chirpier/chirpier-go
```

## Getting Started

To start using the SDK, you need to initialize it with your API key.

Here's a quick example of how to use the Chirpier SDK:

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
    err := chirpier.Initialize(chirpier.Options{
        Key: "your-api-key",
        Region: "us-west",
    })
    if err != nil {
        fmt.Printf("Error initializing Chirpier: %v\n", err)
        return
    }

    // Create and send an event
    err = chirpier.Monitor(
        context.Background(),
        chirpier.Event{
            GroupID:    "bfd9299d-817a-452f-bc53-6e154f2281fc",
            StreamName: "My measurement",
            Value:      1,
        },
    )
    if err != nil {
        fmt.Printf("Error monitoring event: %v\n", err)
        return
    }

    // Create a context with timeout to ensure proper shutdown
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    // Wait for any pending events to be sent
    <-ctx.Done()
}
```

## API Reference

### Initialize

Initialize the Chirpier client with your API key and region. Find your API key in the Chirpier Integration page.

```go
err := chirpier.Initialize(chirpier.Options{
    Key: "your-api-key",
    Region: "us-west",
})
```

- `your-api-key` (str): Your Chirpier integration key
- `region` (str): Your local region - options are `us-west`, `eu-west`, `asia-southeast`

### Event

All events emitted to Chirpier must be of type `Event`.

```go
event := chirpier.Event{
    GroupID:    "bfd9299d-817a-452f-bc53-6e154f2281fc",
    StreamName: "My measurement",
    Value:      1,
}
```

- `group_id` (str): UUID of the monitoring group
- `stream_name` (str): Name of the measurement stream
- `value` (float): Numeric value to record

### Monitor

Send an event to Chirpier using the `Monitor` function.

```go
err = chirpier.Monitor(ctx, event)
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
