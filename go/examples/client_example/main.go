package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/rasteiro11/go-sv2-common/sv2"
)

// Base58 alphabet (Bitcoin style, no 0,O,l,I)
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var b58Index [256]int

func init() {
	for i := range b58Index {
		b58Index[i] = -1
	}
	for i, c := range []byte(b58Alphabet) {
		b58Index[c] = i
	}
}

// base58Decode decodes a Base58-encoded string into bytes (Bitcoin alphabet).
func base58Decode(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	res := big.NewInt(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		v := b58Index[c]
		if v < 0 {
			return nil, fmt.Errorf("invalid base58 char '%c'", c)
		}
		res.Mul(res, big.NewInt(58))
		res.Add(res, big.NewInt(int64(v)))
	}
	// Convert big.Int to bytes
	decoded := res.Bytes()
	// Add leading zero bytes for each leading '1'
	leadingZeros := 0
	for i := 0; i < len(s) && s[i] == '1'; i++ {
		leadingZeros++
	}
	if leadingZeros > 0 {
		decoded = append(make([]byte, leadingZeros), decoded...)
	}
	return decoded, nil
}

// authorityPublicKey decodes the base58 authority public key and extracts the 32-byte key (skip 2-byte version prefix).
func authorityPublicKey() ([]byte, error) {
	// Matches Python examples & tests
	const authorityPubKeyB58 = "9auqWEzQDVyd2oe1JVGFLMLHZtCo2FFqZwtKA5gd9xbuEu7PH72"
	full, err := base58Decode(authorityPubKeyB58)
	if err != nil {
		return nil, err
	}
	if len(full) < 34 {
		return nil, errors.New("decoded pub key too short")
	}
	key := full[2:34] // skip 2-byte prefix
	if len(key) != 32 {
		return nil, errors.New("extracted pub key not 32 bytes")
	}
	return key, nil
}

func performHandshake(conn net.Conn, state *sv2.CodecState) error {
	fmt.Println("--- Starting Handshake as Initiator ---")
	frame0, err := state.Step0()
	if err != nil {
		return fmt.Errorf("step0: %w", err)
	}
	if _, err = conn.Write(frame0); err != nil {
		return fmt.Errorf("write step0: %w", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read step1: %w", err)
	}
	if err = state.Step2(buf[:n]); err != nil {
		return fmt.Errorf("step2: %w", err)
	}
	done, err := state.HandshakeComplete()
	if err != nil || !done {
		return fmt.Errorf("handshake not complete: %v", err)
	}
	fmt.Println("✓ Handshake complete")
	return nil
}

func createSetupConnection() (*sv2.Message, error) {
	return sv2.NewSetupConnection(1, 2, 2, 0, "client.example.com", "Example Go Client", "v1.0.0", "go-client-1.0", "go-client-001")
}

func exchangeMessages(conn net.Conn, state *sv2.CodecState) error {
	enc, err := sv2.NewEncoder()
	if err != nil {
		return err
	}
	defer enc.Free()
	dec, err := sv2.NewDecoder()
	if err != nil {
		return err
	}
	defer dec.Free()

	msg, err := createSetupConnection()
	if err != nil {
		return err
	}
	defer msg.Free()

	encoded, err := enc.Encode(msg, state)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	fmt.Printf("✓ Encoded message (%d bytes): %s...\n", len(encoded), hex.EncodeToString(encoded))
	if _, err := conn.Write(encoded); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	fmt.Println("✓ Sent SetupConnection")

	for {
		need, err := dec.BufferSize()
		if err != nil {
			return fmt.Errorf("buffer size: %w", err)
		}

		if need == 0 {
			// Trigger internal size calculation by attempting decode with empty slice
			_, missing, err := dec.TryDecode(nil, state)
			if err != nil && missing > 0 {
				// Decoder updated internal expected size; loop again
				continue
			} else if err != nil {
				return fmt.Errorf("initial decode unexpected error: %w", err)
			}
			// If it somehow decoded with zero bytes (unlikely), continue to handle
			need, err = dec.BufferSize()
			if err != nil {
				return fmt.Errorf("buffer size post-trigger: %w", err)
			}
		}

		// Read exactly 'need' bytes (handle partial TCP reads)
		buf := make([]byte, need)
		readTotal := 0
		for readTotal < int(need) {
			n, rerr := conn.Read(buf[readTotal:])
			if rerr != nil {
				return fmt.Errorf("read resp: %w", rerr)
			}
			if n == 0 {
				return errors.New("connection closed while reading response")
			}
			readTotal += n
		}

		resp, missing, err := dec.TryDecode(buf, state)
		if err != nil {
			if missing > 0 { // Need more bytes; continue loop
				continue
			}
			return fmt.Errorf("decode: %w", err)
		}

		// Successfully decoded a message
		if resp.IsSetupConnectionSuccess() {
			scs, err := resp.GetSetupConnectionSuccess()
			if err != nil {
				resp.Free()
				return fmt.Errorf("get success fields: %w", err)
			}
			fmt.Println("🎉 Received SetupConnectionSuccess!")
			fmt.Printf("Used Version: %d\n", scs.UsedVersion)
			fmt.Printf("Flags: %d\n", scs.Flags)
			resp.Free()
			break
		} else {
			resp.Free()
			fmt.Println("Received unexpected message variant; continuing")
			// Continue listening for success (optional break could be here)
			continue
		}
	}
	return nil
}

func runClient(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	fmt.Printf("Connecting to %s...\n", addr)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	authKey, err := authorityPublicKey()
	if err != nil {
		return fmt.Errorf("authority key decode: %w", err)
	}
	fmt.Printf("✓ Using authority public key: %x...\n", authKey[:8])
	state, err := sv2.NewInitiator(authKey)
	if err != nil {
		return err
	}
	defer state.Free()
	if err := performHandshake(conn, state); err != nil {
		return err
	}
	if err := exchangeMessages(conn, state); err != nil {
		return err
	}
	fmt.Println("Keeping connection alive 2s...")
	time.Sleep(2 * time.Second)
	fmt.Println("Client finished successfully")
	return nil
}

func main() {
	fmt.Println("==============================")
	fmt.Println("     Stratum V2 Go Client")
	fmt.Println("==============================")
	host := os.Getenv("SV2_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 34254
	if p := os.Getenv("SV2_PORT"); p != "" {
		var v int
		if _, err := fmt.Sscanf(p, "%d", &v); err == nil {
			port = v
		}
	}
	if err := runClient(host, port); err != nil {
		fmt.Printf("✗ Client failed: %v\n", err)
	}
}
