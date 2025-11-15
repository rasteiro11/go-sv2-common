# go-sv2-common

Go bindings for Stratum V2 codec and message primitives via the Rust `libsv2` cdylib, exposed through a small C ABI and cgo wrappers.

This package lets Go programs:
- Perform the Noise_NX handshake as an initiator (client)
- Perform the Noise_NX handshake as a responder (server)
- Encode Sv2 messages over the encrypted transport (currently SetupConnection)
- Decode requests/responses and inspect variants (SetupConnection, SetupConnectionSuccess)

The bindings are intentionally small and focused on the example flows. You can extend the Rust FFI and Go wrappers to cover more message types as needed.

## Architecture
- Rust cdylib: `libsv2.so` (root crate) built by `cargo build --release`
- FFI layer: `src/ffi.rs` defines a minimal C ABI for handshake/encode/decode + message constructors
- Go cgo wrappers: `go/sv2/ffi.go` map C ABI to idiomatic Go types and errors
- Examples:
  - Client: `go/examples/client_example/main.go` (initiator) — connects, handshakes, sends SetupConnection, prints SetupConnectionSuccess
  - Server: `go/examples/server_example/main.go` (responder) — accepts TCP, performs responder handshake, decodes SetupConnection, replies with SetupConnectionSuccess

## Requirements
- Linux
- Go 1.22+
- Rust 1.82+ (Cargo.lock v4)

## Build and Run (Local)
### 1) Build Rust library
```bash
cargo build --release
```
The shared library will be at `target/release/libsv2.so`.

### 2) Run the Go client (initiator)
```bash
# From repo root
export LD_LIBRARY_PATH=target/release
go run ./go/examples/client_example
```
The client will connect to `127.0.0.1:34254`, perform the Noise_NX handshake, send `SetupConnection`, and print the received `SetupConnectionSuccess` fields.

Make sure the Python server example is running first:
```bash
python3 python/examples/server_example.py
```

Environment variables for the Go client:
- `SV2_HOST` (default `127.0.0.1`)
- `SV2_PORT` (default `34254`)

Example overriding host/port:
```bash
SV2_HOST=10.0.0.42 SV2_PORT=34254 \
  LD_LIBRARY_PATH=target/release \
  go run ./go/examples/client_example
```

### 3) Run the Go server (responder)
In one terminal:
```bash
export LD_LIBRARY_PATH=target/release
go run ./go/examples/server_example
```
In another terminal, run a client (Go or Python) to connect to `127.0.0.1:34254`.

## Docker 

Build and run just the Go client image:
```bash
docker build -f docker/Dockerfile.go-client -t sv2-go-client .
docker run --rm -it --network host sv2-go-client
```
Or orchestrate with compose (build all images):
```bash
cd docker
docker compose up --build
```
Docker Compose services:
- `sv2-server` (Python server) — publishes `34254:34254`
- `sv2-server-go` (Go server) — publishes `34254:34254`
- `sv2-client-python` (Python client) — connects to `sv2-server:34254` by env
- `sv2-client-go` (Go client) — connects to `sv2-server:34254` by env

Recommended usage (run one server at a time to avoid port conflicts):
```bash
# Start Python server (detached)
docker compose -f docker/docker-compose.yml up -d sv2-server

# Run Go client (targets sv2-server by env)
docker compose -f docker/docker-compose.yml run --rm sv2-client-go

# Or run Python client (targets sv2-server by env)
docker compose -f docker/docker-compose.yml run --rm sv2-client-python

# View server logs
docker logs -f sv2-server-go

# Stop & remove server when finished
docker compose -f docker/docker-compose.yml stop sv2-server-go
docker compose -f docker/docker-compose.yml rm -f sv2-server-go
```

Notes:
- To expose externally, connect to `localhost:34254` from your host (port is published).

## Go API Surface (current)
- Types
  - `sv2.CodecState`: handshake state (initiator or responder)
  - `sv2.Encoder`, `sv2.Decoder`: frame encode/decode over transport
  - `sv2.Message`: opaque enum holding Sv2 messages
  - `sv2.SetupConnection`: request fields (via getter)
  - `sv2.SetupConnectionSuccess`: success fields (via getter or constructor)
- Constructors & methods
  - `sv2.NewInitiator(authorityPubKey32 []byte) (*CodecState, error)`
  - `(*CodecState).Step0() ([]byte, error)`
  - `(*CodecState).Step2(frame []byte) error`
  - `(*CodecState).HandshakeComplete() (bool, error)`
  - `sv2.NewResponder(authorityPub32, authorityPriv32 []byte, certValiditySecs uint64) (*CodecState, error)`
  - `(*CodecState).Step1(initiatorFrame []byte) ([]byte, error)`
  - `sv2.NewEncoder()/NewDecoder()`
  - `(*Encoder).Encode(msg *Message, state *CodecState) ([]byte, error)`
  - `(*Decoder).BufferSize() (uint32, error)`
  - `(*Decoder).TryDecode(data []byte, state *CodecState) (*Message, uint32, error)` — if error and `missing>0`, read that many more bytes
  - `sv2.NewSetupConnection(...) (*Message, error)`
  - `sv2.NewSetupConnectionSuccess(usedVersion uint16, flags uint32) (*Message, error)`
  - `(*Message).IsSetupConnectionSuccess() bool`
  - `(*Message).GetSetupConnectionSuccess() (*SetupConnectionSuccess, error)`
  - `(*Message).IsSetupConnection() bool`
  - `(*Message).GetSetupConnection() (*SetupConnection, error)`

- Errors
  - `sv2.ErrCodec`, `sv2.ErrMessage` for broad categories
  - Detailed decode error mapping is returned as `fmt.Errorf("codec error code %d")` for non-MissingBytes, and MissingBytes is surfaced via the `missing` value of `TryDecode`

## Example Authority Key
Both the Python server and Go client use the same base58-encoded authority public key. The Go example includes a small base58 decoder and extracts the 32-byte key (skipping a 2-byte prefix) to match the Python example.

## Extending Coverage
To support more message types:
1. Add/extend conversions and message variants in Rust (under `src/messages/*` and `src/messages/mod.rs`).
2. Extend the C ABI in `src/ffi.rs` to add constructors and variant inspectors for new message types.
3. Update Go wrapper `go/sv2/ffi.go` to surface the new functions as Go types and methods.
4. Rebuild `libsv2.so` and rerun the Go code.

## Known Limitations
- Only a subset of Sv2 messages is wired through the FFI (SetupConnection + success). Add more as needed.
- Dynamic linking: Go binaries require `libsv2.so` available via `LD_LIBRARY_PATH`

## License
Dual-licensed: MIT or Apache-2.0 (see repository LICENSE files).
