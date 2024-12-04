# Chirpier SDK

The Chirpier SDK for Go provides a simple and efficient way to integrate Chirpier's event tracking functionality into your Go applications.

## Features

- Easy-to-use API for sending events to Chirpier
- Automatic batching of events for improved performance
- Automatic retry mechanism with exponential backoff
- Thread-safe operations
- Periodic flushing of the event queue

## Installation

To install the Chirpier SDK, use `go get`:

```
go get github.com/chirpier/chirpier-go
```

## Usage

Here's a quick example of how to use the Chirpier SDK:

```go
package main

import (
    "fmt"
    "time"
    "github.com/chirpier/chirpier-go"
)

func main() {
    // Initialize the Chirpier client
    err := chirpier.Initialize(chirpier.Options{
        Key: "your-api-key",
    })
    if err != nil {
        fmt.Printf("Error initializing Chirpier: %v\n", err)
        return
    }

    // Create and send an event
    event := chirpier.Event{
        GroupID: "f3438ee9-b964-48aa-b938-a803df440a3c",
        Stream:  "test-stream",
        Value:   42.0,
    }
    err = chirpier.Monitor(event)
    if err != nil {
        fmt.Printf("Error monitoring event: %v\n", err)
        return
    }
}
```

## Initialization

```go
err := chirpier.Initialize(chirpier.Options{
    Key: "your-api-key",
})
if err != nil {
    // Handle error
}

err = client.Monitor(event)
```

## Running Tests

To run the test suite:

```
go test -v
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

If you encounter any problems or have any questions, please open an issue on the GitHub repository.
