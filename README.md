# xk6-multipart-http

Problem:

The current version of k6 only supports http requests that return all data.

That makes SSE and [Apollo Router Subscriptions](https://www.apollographql.com/docs/graphos/routing/operations/subscriptions/multipart-protocol) impossible to load test.


There is currently a proposed SSE Extention that might one day graduate to become officially released, but it isn't at this time.


## Requirements

* [xk6](https://github.com/grafana/xk6) (`go install go.k6.io/xk6/cmd/xk6@latest`)

## Getting started  

1. Build the k6's binary:

  ```shell
asdf install
make compile-docker
  ```

2. Run an example:

  ```shell
./k6 run Examples/test.js
  ```


