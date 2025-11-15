package main

import (
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/rasteiro11/go-sv2-common/sv2"
)

// Base58 decode (Bitcoin alphabet)
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
func base58Decode(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	n := big.NewInt(0)
	for i := 0; i < len(s); i++ {
		v := b58Index[s[i]]
		if v < 0 {
			return nil, fmt.Errorf("invalid base58 char %q", s[i])
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(v)))
	}
	out := n.Bytes()
	lead := 0
	for lead < len(s) && s[lead] == '1' {
		lead++
	}
	if lead > 0 {
		out = append(make([]byte, lead), out...)
	}
	return out, nil
}

func authorityKeypair() (pub32, priv32 []byte, err error) {
	const pubB58 = "9auqWEzQDVyd2oe1JVGFLMLHZtCo2FFqZwtKA5gd9xbuEu7PH72"
	const privB58 = "mkDLTBBRxdBv998612qipDYoTK3YUrqLe8uWw7gu3iXbSrn2n"
	pubFull, err := base58Decode(pubB58)
	if err != nil {
		return nil, nil, err
	}
	privFull, err := base58Decode(privB58)
	if err != nil {
		return nil, nil, err
	}
	if len(pubFull) < 34 || len(privFull) < 32 {
		return nil, nil, fmt.Errorf("bad decoded key sizes")
	}
	return pubFull[2:34], privFull[:32], nil
}

func handleConn(c net.Conn) {
	defer c.Close()
	fmt.Printf("\nNew client: %s\n", c.RemoteAddr())
	pub, priv, err := authorityKeypair()
	if err != nil {
		fmt.Printf("authority keys error: %v\n", err)
		return
	}
	resp, err := sv2.NewResponder(pub, priv, 86400)
	if err != nil {
		fmt.Printf("responder create error: %v\n", err)
		return
	}
	defer resp.Free()

	// Handshake responder: read 64 bytes from initiator, respond with step1
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		fmt.Printf("read step0: %v\n", err)
		return
	}
	frame1, err := resp.Step1(buf)
	if err != nil {
		fmt.Printf("step1: %v\n", err)
		return
	}
	if _, err := c.Write(frame1); err != nil {
		fmt.Printf("write step1: %v\n", err)
		return
	}
	fmt.Println("Handshake complete (responder)")

	dec, _ := sv2.NewDecoder()
	defer dec.Free()
	enc, _ := sv2.NewEncoder()
	defer enc.Free()

	for {
		need, err := dec.BufferSize()
		if err != nil {
			fmt.Printf("buffer size: %v\n", err)
			return
		}
		if need == 0 {
			if _, missing, err := dec.TryDecode(nil, resp); err != nil && missing > 0 {
				continue
			}
		}
		if need == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		rb := make([]byte, need)
		read := 0
		for read < int(need) {
			n, rerr := c.Read(rb[read:])
			if rerr != nil {
				fmt.Printf("read enc msg: %v\n", rerr)
				return
			}
			if n == 0 {
				fmt.Println("client closed")
				return
			}
			read += n
		}
		msg, missing, err := dec.TryDecode(rb, resp)
		if err != nil {
			if missing > 0 {
				continue
			}
			fmt.Printf("decode error: %v\n", err)
			return
		}
		// Handle message
		if msg.IsSetupConnection() {
			sc, _ := msg.GetSetupConnection()
			fmt.Printf("Received SetupConnection: proto=%d ver=%d-%d flags=%d\n", sc.Protocol, sc.MinVersion, sc.MaxVersion, sc.Flags)
			// Respond with success (use max version and same flags)
			succ, _ := sv2.NewSetupConnectionSuccess(sc.MaxVersion, sc.Flags)
			encFrame, err := enc.Encode(succ, resp)
			succ.Free()
			if err != nil {
				fmt.Printf("encode resp: %v\n", err)
				msg.Free()
				return
			}
			if _, err := c.Write(encFrame); err != nil {
				fmt.Printf("write resp: %v\n", err)
				msg.Free()
				return
			}
			fmt.Println("Sent SetupConnectionSuccess")
		} else {
			fmt.Println("Received non-SetupConnection message (ignored)")
		}
		msg.Free()
	}
}

func main() {
	ln, err := net.Listen("tcp", ":34254")
	if err != nil {
		panic(err)
	}
	fmt.Println("Go Sv2 Server listening on :34254")
	for {
		c, err := ln.Accept()
		if err != nil {
			fmt.Printf("accept err: %v\n", err)
			continue
		}
		go handleConn(c)
	}
}
