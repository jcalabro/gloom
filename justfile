set shell := ["bash", "-cu"]

# Lints and runs short tests with the race detector
default: lint test

# Lints the code
lint *ARGS="./...":
    golangci-lint run --timeout 5m {{ARGS}}

# Runs the tests (short, no race detector)
test *ARGS="./...":
    go test -short -count=1 {{ARGS}}
alias t := test

# Runs the tests with the race detector (short)
test-race *ARGS="./...":
    go test -short -count=1 -race {{ARGS}}

# Runs all tests including long-running ones (no race detector)
test-long *ARGS="./...":
    go test -count=1 {{ARGS}}

# Runs benchmarks (in benchmarks/ submodule)
bench *ARGS="./...":
    cd benchmarks && GOAMD64=v2 go test -bench=. -benchmem {{ARGS}}

# Runs benchmarks with longer duration for accurate results
bench-long *ARGS="./...":
    just bench -benchtime=3s -count=3 {{ARGS}}
