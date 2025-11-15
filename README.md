# go-sv2-common

Go bindings for Stratum V2 codec and message primitives via the Rust `libsv2` cdylib, exposed through a small C ABI and cgo wrappers.

This package lets Go programs:
- Perform the Noise_NX handshake (initiator) with an Sv2 server
- Encode Sv2 messages over the encrypted transport (currently SetupConnection)
- Decode responses and inspect variants (e.g., SetupConnectionSuccess)

The bindings are intentionally small and focused on the example flows. You can extend the Rust FFI and Go wrappers to cover more message types as needed.

## Architecture
- Rust cdylib: `libsv2.so` (root crate) built by `cargo build --release`
- FFI layer: `src/ffi.rs` defines a minimal C ABI for handshake/encode/decode + message constructors
- Go cgo wrappers: `go/sv2/ffi.go` map C ABI to idiomatic Go types and errors
- Example: `go/examples/client_example/main.go` connects to a Python server example, performs handshake, sends SetupConnection, and prints SetupConnectionSuccess

## Requirements
- Linux
- Go 1.22+
- Rust 1.82+ (Cargo.lock v4)

## Build and Run
### 1) Build Rust library
```bash
cargo build --release
```
The shared library will be at `target/release/libsv2.so`.

### 2) Run the Go example
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
Note: The Go client connects to `127.0.0.1`. When running with Docker, use `--network host` (Linux) or modify the client to connect to the service name (e.g., `sv2-server`) for bridge networking.

## Go API Surface (current)
- Types
  - `sv2.CodecState`: handshake state (initiator)
  - `sv2.Encoder`, `sv2.Decoder`: frame encode/decode over transport
  - `sv2.Message`: opaque enum holding Sv2 messages
  - `sv2.SetupConnectionSuccess`: fields of the success variant
- Constructors & methods
  - `sv2.NewInitiator(authorityPubKey32 []byte) (*CodecState, error)`
  - `(*CodecState).Step0() ([]byte, error)`
  - `(*CodecState).Step2(frame []byte) error`
  - `(*CodecState).HandshakeComplete() (bool, error)`
  - `sv2.NewEncoder()/NewDecoder()`
  - `(*Encoder).Encode(msg *Message, state *CodecState) ([]byte, error)`
  - `(*Decoder).BufferSize() (uint32, error)`
  - `(*Decoder).TryDecode(data []byte, state *CodecState) (*Message, uint32, error)` — if error and `missing>0`, read that many more bytes
  - `sv2.NewSetupConnection(...) (*Message, error)`
  - `(*Message).IsSetupConnectionSuccess() bool`
  - `(*Message).GetSetupConnectionSuccess() (*SetupConnectionSuccess, error)`

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
- Only a subset of Sv2 messages is wired through the FFI (SetupConnection + success/error). Add more as needed.
- Only the initiator side of the Noise_NX handshake is wrapped in Go; the Python example implements the responder.
- Dynamic linking: the Go binary requires `libsv2.so` available via `LD_LIBRARY_PATH`.

## License
Dual-licensed: MIT or Apache-2.0 (see repository LICENSE files).
