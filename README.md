# xk6-multipart-http

A k6 extension that adds support for testing HTTP multipart streaming responses, enabling load testing of http multipart/mixed GraphQL subscriptions.

## What is this?

k6 natively only supports traditional HTTP request-response patterns where all data is returned at once. This extension bridges that gap by providing WebSocket-like functionality for HTTP multipart streams, specifically targeting:

- **GraphQL Subscriptions** using the [multipart protocol](https://www.apollographql.com/docs/graphos/routing/operations/subscriptions/multipart-protocol)
- **Apollo Router Subscriptions**
- **Server-Sent Events** over multipart HTTP
- Any streaming HTTP endpoint using `multipart/mixed` content type

## How it works

The extension implements an event-driven architecture that:

1. **Establishes HTTP connections** with `multipart/mixed` content type
2. **Parses streaming multipart responses** in real-time using boundary delimiters (`--graphql`)
3. **Emits JavaScript events** (open, message, close, error) similar to WebSocket API
4. **Integrates with k6 metrics** to track session duration, message counts, and connection states
5. **Handles multiple payload formats** including GraphQL `{"payload":...}` and `{"data":...}` structures

## Project structure

```
├── module.go           # k6 module registration and per-VU instance creation
├── multipartHTTP.go    # Core subscription client with event handling
├── client.go           # HTTP connection and multipart response parsing
├── listeners.go        # JavaScript event system implementation
├── events/events.go    # Event type definitions and utilities
├── metrics.go          # Custom k6 metrics integration
├── params.go           # Parameter parsing for connection options
└── timer_example/      # Complete GraphQL server example for testing
```

## Usage

### JavaScript API

```javascript
import multipartHTTP from 'k6/x/multipartHTTP';

export default function() {
  // GraphQL subscription example
  const subscription = multipartHTTP.MultipartHttp('http://localhost:8080/query', {
    method: 'POST',
    headers: {
      'Accept': 'multipart/mixed; boundary="graphql"; subscriptionSpec=1.0',
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({
      query: 'subscription { time { time } }'
    })
  });

  // Event handlers (similar to WebSocket API)
  subscription.addEventListener('open', () => {
    console.log('Connection opened');
  });

  subscription.addEventListener('message', (event) => {
    const data = JSON.parse(event.data);
    console.log('Received:', data);
  });

  subscription.addEventListener('close', (event) => {
    console.log('Connection closed');
  });

  subscription.addEventListener('error', (event) => {
    console.log('Error occurred:', event);
  });

  // Close connection after 10 seconds
  setTimeout(() => {
    subscription.close();
  }, 10000);
}
```

## Requirements

* [xk6](https://github.com/grafana/xk6) (`go install go.k6.io/xk6/cmd/xk6@latest`)

## Getting started

1. Build the k6 binary with the extension:

   ```shell
   # Using Docker (recommended)
   make compile-docker

   # Or build locally
   make k6
   ```

2. Run the example:

   ```shell
   # Start the test GraphQL server
   cd timer_example && go run server.go

   # In another terminal, run the k6 test
   ./k6 run timer_example/timer_example.js
   ```

## Protocol support

- **Content-Type**: `multipart/mixed; boundary="graphql"; subscriptionSpec=1.0`
- **Boundary parsing**: Handles `--graphql` multipart boundaries
- **GraphQL formats**: Supports both Apollo Server (`{"data":...}`) and standard (`{"payload":...}`) formats
- **Heartbeat detection**: Recognizes `{}` heartbeat messages
- **Connection states**: Implements WebSocket-like ReadyState pattern (CONNECTING, OPEN, CLOSING, CLOSED)
