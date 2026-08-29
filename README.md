# unofficial-gnata-jsonata-cli

Command-line interface for JSONata query and transformation expressions, written in Go and powered by the [`recolabs/gnata`](https://github.com/recolabs/gnata) engine. It is designed as a drop-in binary alternative to [`dashjoin/jsonata-cli`](https://github.com/dashjoin/jsonata-cli).

## Installation

### From Pre-Built Binaries

Download the binary matching your platform from GitHub Releases:

- **Windows**: `arm64` (`jsonata.exe`), `amd64` (`jsonata.exe`)
- **Linux**: `amd64` (`jsonata`), `arm64` (`jsonata`)
- **macOS**: `arm64` (`jsonata`), `amd64` (`jsonata`)

### Using Go

```bash
go install github.com/mtaas-v0/unofficial-gnata-jsonata-cli@latest
```

### Build from Source

Requires Go 1.22+:

```bash
git clone https://github.com/mtaas-v0/unofficial-gnata-jsonata-cli.git
cd unofficial-gnata-jsonata-cli
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/jsonata .
```

For Windows on ARM64:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/jsonata.exe .
```

---

## Usage

```text
Usage:
  jsonata [flags] <expression>
  jsonata -f <data.json> < <transform.jsonata>

Flags:
  -f, --file <path>    Input JSON file path (use '-' for stdin)
  -v, --version        Print version information and exit
  -h, --help           Show help message and exit
```

### Examples

#### Case 1: Inline Expression with Data File

```bash
jsonata "Account.Order[0].Price * 1.2" -f order.json
```

output:

```
34.199999999999996
```

#### Case 2: Piped Data via Stdin

```bash
cat order.json | jsonata "Account.Order.Price"
```

output:

```
[28.50,107.99]
```

```bash
curl -s https://api.example.com/data | jsonata "users[active = true].name"
```

#### Case 3: Expression Piped via Stdin

When evaluating complex `.jsonata` files, pipe the expression into `stdin` and pass the data file with `-f`:

```bash
jsonata -f order.json < transform.jsonata
```

#### example order.json

```json
{
  "Account": {
    "Account Name": "Firefly",
    "Order": [
      {
        "OrderID": "order103",
        "Product": "Bowler Hat",
        "Quantity": 2,
        "Price": 28.50
      },
      {
        "OrderID": "order104",
        "Product": "Cloak",
        "Quantity": 1,
        "Price": 107.99
      }
    ]
  }
}
```

---

## Stream Resolution Rules

Input stream resolution matches the reference CLI implementation:

| Positional Argument (`expr`) | `-f` / `--file` Flag | `stdin` Status | Behavior |
| :--- | :--- | :--- | :--- |
| Present | Present | Ignored | Evaluates expression against the specified file. |
| Present | Not provided | Pipe / Redirect | Evaluates expression against JSON data read from `stdin`. |
| Not provided | Present | Pipe / Redirect | Reads JSONata expression from `stdin` and evaluates against the file. |
| Not provided | Not provided | Any | Exits with error (missing inputs). |

---

## Testing & Benchmarks

### Unit and CLI Smoke Tests

Run standard tests locally:

```bash
go test -v -race ./...
```

Run the local Ubuntu 22.04 verification script:

```bash
chmod +x ./scripts/ubuntu2204-testRun.sh
./scripts/ubuntu2204-testRun.sh
```

### Official JSONata Test Suite Benchmark

The CLI includes a benchmark harness (`cmd/benchmark`) that runs against the official JSONata test suite:

```bash
# Clone official test fixtures
git clone --depth 1 https://github.com/jsonata-js/jsonata.git /tmp/jsonata-upstream

# Run benchmark against compiled binary
go run cmd/benchmark/main.go \
  -bin ./bin/jsonata \
  -suite /tmp/jsonata-upstream/test/test-suite
```

---

## License

This project is licensed under the [MIT License](LICENSE), matching upstream [`recolabs/gnata`](https://github.com/recolabs/gnata).

## Acknowledgements

- [RecoLabs/gnata](https://github.com/recolabs/gnata): JSONata query and transformation engine in Go.
- [JSONata](https://jsonata.org): Original reference language and test suite.
- [dashjoin/jsonata-cli](https://github.com/dashjoin/jsonata-cli): Reference CLI specification and behavior.
- Gemini wrote this but errors are mine.
