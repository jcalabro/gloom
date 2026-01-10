set shell := ["bash", "-cu"]

# Lints and runs all tests
default: lint test

# Lints the code
lint *ARGS="./...":
    golangci-lint run --timeout 5m {{ARGS}}

# Runs the tests
t *ARGS="./...":
    go test -v -count=1 -covermode=atomic -coverprofile=test-coverage.out {{ARGS}}

# Runs the tests with the race detector enabled
test *ARGS="./...":
    just t -race {{ARGS}}

# Runs benchmarks (in benchmarks/ submodule)
bench *ARGS="./...":
    cd benchmarks && GOAMD64=v2 go test -bench=. -benchmem {{ARGS}}

# Runs benchmarks with longer duration for accurate results
bench-long *ARGS="./...":
    just bench -benchtime=3s -count=3 {{ARGS}}
