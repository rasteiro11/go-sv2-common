package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/rasteiro11/go-sv2-common/sv2"
)

// --- Base58 Helpers (from server_example) ---
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

// --- RPC Client ---
type RPCClient struct {
	URL      string
	User     string
	Password string
	Client   *http.Client
}

func NewRPCClient(url, user, password string) *RPCClient {
	return &RPCClient{
		URL:      url,
		User:     user,
		Password: password,
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *RPCClient) Call(method string, params []interface{}) (json.RawMessage, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "sv2-tp",
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.URL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.User, c.Password)
	req.Header.Set("Content-Type", "text/plain;")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  interface{}     `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error: %v", rpcResp.Error)
	}
	return rpcResp.Result, nil
}

type BlockTemplate struct {
	Version                  uint32            `json:"version"`
	PreviousBlockHash        string            `json:"previousblockhash"`
	Transactions             []Tx              `json:"transactions"`
	CoinbaseAux              map[string]string `json:"coinbaseaux"`
	CoinbaseValue            int64             `json:"coinbasevalue"`
	Target                   string            `json:"target"`
	MinTime                  int64             `json:"mintime"`
	CurTime                  int64             `json:"curtime"`
	Bits                     string            `json:"bits"`
	Height                   int64             `json:"height"`
	DefaultWitnessCommitment string            `json:"default_witness_commitment,omitempty"`
}

type Tx struct {
	Data string `json:"data"`
	TxID string `json:"txid"`
	Hash string `json:"hash"`
}

// --- Template Provider ---
type TemplateProvider struct {
	rpc        *RPCClient
	clients    map[net.Conn]*Client
	mu         sync.Mutex
	currentTpl *BlockTemplate
	tplID      uint64
	payoutAddr btcutil.Address
}

type Client struct {
	conn net.Conn
	enc  *sv2.Encoder
	resp *sv2.CodecState
}

func NewTemplateProvider(rpc *RPCClient, payoutAddr string, chainParams *chaincfg.Params) *TemplateProvider {
	addr, err := btcutil.DecodeAddress(payoutAddr, chainParams)
	if err != nil {
		// Fallback or panic
		panic(fmt.Sprintf("DecodeAddress %s failed: %v", payoutAddr, err))
	}
	return &TemplateProvider{
		rpc:        rpc,
		clients:    make(map[net.Conn]*Client),
		payoutAddr: addr,
	}
}

func (tp *TemplateProvider) Start(port int) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	fmt.Printf("Template Provider listening on :%d\n", port)

	go tp.pollLoop()

	for {
		c, err := ln.Accept()
		if err != nil {
			fmt.Printf("accept err: %v\n", err)
			continue
		}
		go tp.handleConn(c)
	}
}

func (tp *TemplateProvider) pollLoop() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		tp.updateTemplate()
	}
}

func (tp *TemplateProvider) updateTemplate() {
	// Call getblocktemplate
	res, err := tp.rpc.Call("getblocktemplate", []interface{}{map[string]interface{}{"rules": []string{"segwit"}}})
	if err != nil {
		fmt.Printf("getblocktemplate error: %v\n", err)
		return
	}
	var tpl BlockTemplate
	if err := json.Unmarshal(res, &tpl); err != nil {
		fmt.Printf("unmarshal tpl error: %v\n", err)
		return
	}

	tp.mu.Lock()
	tp.currentTpl = &tpl
	tp.tplID++
	currentID := tp.tplID
	tp.mu.Unlock()

	fmt.Printf("New template %d, prev: %s\n", currentID, tpl.PreviousBlockHash)

	// Broadcast NewTemplate to all clients
	tp.broadcastTemplate(currentID, &tpl)
}

func (tp *TemplateProvider) createTemplateMessages(id uint64, tpl *BlockTemplate) (*sv2.Message, *sv2.Message, error) {
	// 1. Build Coinbase Prefix and Outputs
	// ScriptSig: BIP34 Height + CoinbaseAux

	// BIP34 Height
	scriptBuilder := txscript.NewScriptBuilder()
	scriptBuilder.AddInt64(tpl.Height)
	// Add CoinbaseAux
	// Sort keys for determinism
	keys := make([]string, 0, len(tpl.CoinbaseAux))
	for k := range tpl.CoinbaseAux {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := tpl.CoinbaseAux[k]
		b, err := hex.DecodeString(v)
		if err != nil {
			return nil, nil, fmt.Errorf("decode coinbaseaux: %v", err)
		}
		scriptBuilder.AddData(b)
	}
	scriptSigPrefix, err := scriptBuilder.Script()
	if err != nil {
		return nil, nil, fmt.Errorf("build script: %v", err)
	}

	// Coinbase Tx Structure for Prefix:
	// Version (4) + Input Count (1) + Input PrevHash (32) + Input Index (4) + ScriptLen (VarInt) + ScriptPrefix
	// We need to construct this manually or use wire.MsgTx and serialize partial.

	cbTx := wire.NewMsgTx(2)
	cbTx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: 0xFFFFFFFF},
		SignatureScript:  scriptSigPrefix, // This is the prefix part
		Sequence:         0xFFFFFFFF,
	})

	// Outputs
	pkScript, err := txscript.PayToAddrScript(tp.payoutAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("paytoaddr: %v", err)
	}
	cbTx.AddTxOut(wire.NewTxOut(tpl.CoinbaseValue, pkScript))

	// Add Witness Commitment if present
	if tpl.DefaultWitnessCommitment != "" {
		witnessScript, err := hex.DecodeString(tpl.DefaultWitnessCommitment)
		if err != nil {
			return nil, nil, fmt.Errorf("decode witness commitment: %v", err)
		}
		// Value is 0 for witness commitment
		cbTx.AddTxOut(wire.NewTxOut(0, witnessScript))
	}

	// Let's reconstruct prefix manually to be safe.
	var prefixBuf bytes.Buffer
	// Version (4)
	binary.Write(&prefixBuf, binary.LittleEndian, uint32(cbTx.Version))
	// Input Count (VarInt) - assume 1
	wire.WriteVarInt(&prefixBuf, 0, 1)
	// PrevHash (32)
	prefixBuf.Write(cbTx.TxIn[0].PreviousOutPoint.Hash[:])
	// Index (4)
	binary.Write(&prefixBuf, binary.LittleEndian, cbTx.TxIn[0].PreviousOutPoint.Index)

	// Script Length (VarInt)
	// The miner will append extranonce. We assume standard 8 bytes extranonce.
	// IMPORTANT: We must append the OP_PUSH opcode for the extranonce so the miner's bytes
	// are interpreted as data, not opcodes.
	// For 8 bytes, the opcode is 0x08.
	extraNonceLen := 8

	// Append the opcode to the script prefix
	// Note: scriptSigPrefix currently ends with the last push from the builder.
	// We add the opcode for the *next* push (the extranonce) which the miner provides.
	scriptSigPrefixWithOpcode := append(scriptSigPrefix, byte(extraNonceLen))

	// Total Script Length = len(scriptSigPrefixWithOpcode) + extraNonceLen
	totalScriptLen := uint64(len(scriptSigPrefixWithOpcode) + extraNonceLen)
	wire.WriteVarInt(&prefixBuf, 0, totalScriptLen)

	// Script Prefix
	prefixBuf.Write(scriptSigPrefixWithOpcode)

	// Serialize outputs
	var outputsBuf bytes.Buffer
	wire.WriteVarInt(&outputsBuf, 0, uint64(len(cbTx.TxOut)))
	for _, txOut := range cbTx.TxOut {
		wire.WriteTxOut(&outputsBuf, 0, 0, txOut)
	}
	coinbaseOutputs := outputsBuf.Bytes()

	// 2. Merkle Path
	txHashes := make([]*chainhash.Hash, 0, len(tpl.Transactions)+1)
	txHashes = append(txHashes, nil) // Placeholder for coinbase
	for _, tx := range tpl.Transactions {
		h, err := chainhash.NewHashFromStr(tx.TxID)
		if err != nil {
			return nil, nil, fmt.Errorf("txid hash: %v", err)
		}
		txHashes = append(txHashes, h)
	}
	merklePath := buildMerklePath(txHashes)

	// 3. Target and PrevHash
	targetBytes, err := hex.DecodeString(tpl.Target)
	if err != nil {
		return nil, nil, fmt.Errorf("decode target: %v", err)
	}
	reverse(targetBytes)

	prevHashBytes, err := chainhash.NewHashFromStr(tpl.PreviousBlockHash)
	if err != nil {
		return nil, nil, fmt.Errorf("prevhash: %v", err)
	}
	prevHash := prevHashBytes.CloneBytes()

	// 4. Bits
	bits, _ := strconv.ParseUint(tpl.Bits, 16, 32)

	// Create messages
	newTplMsg, err := sv2.NewNewTemplate(
		id,
		false, // future
		tpl.Version,
		2, // coinbase tx version
		prefixBuf.Bytes(),
		cbTx.TxIn[0].Sequence,
		0, // Value remaining (0 because we allocated full value to outputs)
		uint32(len(cbTx.TxOut)),
		coinbaseOutputs,
		cbTx.LockTime,
		merklePath,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("NewNewTemplate: %v", err)
	}

	setPrevHashMsg, err := sv2.NewSetNewPrevHash(
		id,
		prevHash,
		uint32(tpl.CurTime),
		uint32(bits),
		targetBytes,
	)
	if err != nil {
		newTplMsg.Free()
		return nil, nil, fmt.Errorf("NewSetNewPrevHash: %v", err)
	}

	return newTplMsg, setPrevHashMsg, nil
}

func (tp *TemplateProvider) broadcastTemplate(id uint64, tpl *BlockTemplate) {
	newTplMsg, setPrevHashMsg, err := tp.createTemplateMessages(id, tpl)
	if err != nil {
		fmt.Printf("createTemplateMessages error: %v\n", err)
		return
	}
	defer newTplMsg.Free()
	defer setPrevHashMsg.Free()

	tp.mu.Lock()
	defer tp.mu.Unlock()

	for _, client := range tp.clients {
		// Send NewTemplate
		encFrame, _ := client.enc.Encode(newTplMsg, client.resp)
		client.conn.Write(encFrame)
		// Send SetNewPrevHash
		encFrame2, _ := client.enc.Encode(setPrevHashMsg, client.resp)
		client.conn.Write(encFrame2)
	}
}

func buildMerklePath(hashes []*chainhash.Hash) [][]byte {
	// Simple implementation for index 0 (coinbase)
	// We need to pair hashes up the tree.
	// Since we are index 0, we always take the right sibling.
	path := [][]byte{}

	// Copy hashes to avoid modifying original
	currentLevel := make([]*chainhash.Hash, len(hashes))
	copy(currentLevel, hashes)

	// We are tracking index 0
	index := 0

	for len(currentLevel) > 1 {
		nextLevel := []*chainhash.Hash{}
		for i := 0; i < len(currentLevel); i += 2 {
			var left, right *chainhash.Hash
			left = currentLevel[i]
			if i+1 < len(currentLevel) {
				right = currentLevel[i+1]
			} else {
				right = left // Duplicate last if odd
			}

			// If we are part of this pair, add the sibling to path
			if i == index || i+1 == index {
				if i == index {
					path = append(path, right.CloneBytes())
				} else {
					// This shouldn't happen for index 0 (always left), but for completeness
					path = append(path, left.CloneBytes())
				}
				// Update index for next level
				index = i / 2
			}

			// Hash pair
			concat := append(left.CloneBytes(), right.CloneBytes()...)
			h := chainhash.DoubleHashH(concat)
			nextLevel = append(nextLevel, &h)
		}
		currentLevel = nextLevel
	}
	return path
}

func (tp *TemplateProvider) handleConn(c net.Conn) {
	defer c.Close()
	fmt.Printf("New client: %s\n", c.RemoteAddr())

	pub, priv, err := authorityKeypair()
	if err != nil {
		return
	}
	resp, err := sv2.NewResponder(pub, priv, 86400)
	if err != nil {
		return
	}
	defer resp.Free()

	// Handshake
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		return
	}
	frame1, err := resp.Step1(buf)
	if err != nil {
		return
	}
	if _, err := c.Write(frame1); err != nil {
		return
	}

	dec, _ := sv2.NewDecoder()
	defer dec.Free()
	enc, _ := sv2.NewEncoder()
	defer enc.Free()

	client := &Client{conn: c, enc: enc, resp: resp}
	tp.mu.Lock()
	tp.clients[c] = client
	tp.mu.Unlock()
	defer func() {
		tp.mu.Lock()
		delete(tp.clients, c)
		tp.mu.Unlock()
	}()

	for {
		need, err := dec.BufferSize()
		if err != nil {
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
				return
			}
			if n == 0 {
				return
			}
			read += n
		}
		msg, missing, err := dec.TryDecode(rb, resp)
		if err != nil {
			if missing > 0 {
				continue
			}
			return
		}

		// Handle Message
		if msg.IsSetupConnection() {
			sc, _ := msg.GetSetupConnection()
			fmt.Printf("SetupConnection: flags=%d\n", sc.Flags)
			succ, _ := sv2.NewSetupConnectionSuccess(sc.MaxVersion, sc.Flags)
			encFrame, _ := enc.Encode(succ, resp)
			succ.Free()
			c.Write(encFrame)
		} else if msg.IsCoinbaseOutputConstraints() {
			fmt.Println("Received CoinbaseOutputConstraints")
			// Send initial template if available
			tp.mu.Lock()
			if tp.currentTpl != nil {
				go tp.broadcastTemplate(tp.tplID, tp.currentTpl)
			}
			tp.mu.Unlock()
		} else if msg.IsRequestTransactionData() {
			req, _ := msg.GetRequestTransactionData()
			fmt.Printf("RequestTransactionData: id=%d\n", req.TemplateID)

			tp.mu.Lock()
			tpl := tp.currentTpl
			tp.mu.Unlock()

			if tpl != nil {
				// Serialize transactions
				txList := [][]byte{}
				for _, tx := range tpl.Transactions {
					b, _ := hex.DecodeString(tx.Data)
					txList = append(txList, b)
				}

				succ, _ := sv2.NewRequestTransactionDataSuccess(req.TemplateID, []byte{}, txList)
				encFrame, _ := enc.Encode(succ, resp)
				succ.Free()
				c.Write(encFrame)
			}
		} else if msg.IsSubmitSolution() {
			sol, _ := msg.GetSubmitSolution()
			fmt.Printf("SubmitSolution: id=%d nonce=%d\n", sol.TemplateID, sol.HeaderNonce)

			// Reconstruct Block
			// 1. Parse Coinbase
			var coinbaseTx wire.MsgTx
			if err := coinbaseTx.Deserialize(bytes.NewReader(sol.CoinbaseTX)); err != nil {
				fmt.Printf("Failed to deserialize coinbase: %v\n", err)
				msg.Free()
				continue
			}

			tp.mu.Lock()
			tpl := tp.currentTpl
			tp.mu.Unlock()

			if tpl != nil {
				// 2. Build Block
				block := wire.NewMsgBlock(wire.NewBlockHeader(
					int32(sol.Version),
					&chainhash.Hash{}, // PrevHash (filled below)
					&chainhash.Hash{}, // MerkleRoot (filled below)
					uint32(sol.HeaderTimestamp),
					uint32(parseBits(tpl.Bits)), // Bits
				))
				block.Header.Nonce = sol.HeaderNonce

				// Set PrevHash
				prevHash, _ := chainhash.NewHashFromStr(tpl.PreviousBlockHash)
				block.Header.PrevBlock = *prevHash

				// Add Txs
				block.AddTransaction(&coinbaseTx)
				for _, txData := range tpl.Transactions {
					var tx wire.MsgTx
					b, _ := hex.DecodeString(txData.Data)
					tx.Deserialize(bytes.NewReader(b))
					block.AddTransaction(&tx)
				}

				// Calculate Merkle Root
				// (btcd handles this if we use a helper, or we calculate it)
				// wire.MsgBlock doesn't auto-calc merkle root.
				// We need to calculate it.
				// But wait, we need to submit the block hex.
				// We can use `btcutil.NewBlock(block)`?

				// Let's just serialize and send. The node will verify merkle root.
				// Wait, if we send a block with wrong merkle root in header, it will be rejected immediately.
				// We MUST calculate the merkle root.
				txs := make([]*btcutil.Tx, len(block.Transactions))
				for i, tx := range block.Transactions {
					txs[i] = btcutil.NewTx(tx)
				}
				merkleRoot := buildMerkleRoot(txs)
				block.Header.MerkleRoot = *merkleRoot

				// Serialize
				var buf bytes.Buffer
				block.Serialize(&buf)
				blockHex := hex.EncodeToString(buf.Bytes())

				// Submit
				fmt.Printf("Submitting block: %s...\n", blockHex[:64])
				res, err := tp.rpc.Call("submitblock", []interface{}{blockHex})
				if err != nil {
					fmt.Printf("submitblock error: %v\n", err)
				} else {
					fmt.Printf("submitblock result: %s\n", res)
				}
			}
		}

		msg.Free()
	}
}

func buildMerkleRoot(txs []*btcutil.Tx) *chainhash.Hash {
	// Use btcd's merkle tree builder
	// But wait, `blockchain.BuildMerkleTreeStore` is in `blockchain` package which pulls in a lot.
	// We can use a simpler one or just copy the logic.
	// For now, let's use a simplified version since we implemented `buildMerklePath`.
	hashes := make([]*chainhash.Hash, len(txs))
	for i, tx := range txs {
		h := tx.Hash()
		hashes[i] = h
	}
	// ... (merkle root calculation logic similar to buildMerklePath but returning root)
	// Actually, let's just use the one we wrote but return the last element.
	// But `buildMerklePath` returns the path for index 0.
	// Let's write `calcMerkleRoot`.
	if len(hashes) == 0 {
		return &chainhash.Hash{}
	}
	currentLevel := hashes
	for len(currentLevel) > 1 {
		nextLevel := []*chainhash.Hash{}
		for i := 0; i < len(currentLevel); i += 2 {
			left := currentLevel[i]
			right := left
			if i+1 < len(currentLevel) {
				right = currentLevel[i+1]
			}
			concat := append(left.CloneBytes(), right.CloneBytes()...)
			h := chainhash.DoubleHashH(concat)
			nextLevel = append(nextLevel, &h)
		}
		currentLevel = nextLevel
	}
	return currentLevel[0]
}

func reverse(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

func parseBits(s string) uint32 {
	b, _ := strconv.ParseUint(s, 16, 32)
	return uint32(b)
}

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:8332", "Bitcoin RPC URL")
	rpcUser := flag.String("rpcuser", "user", "Bitcoin RPC User")
	rpcPass := flag.String("rpcpass", "pass", "Bitcoin RPC Password")
	port := flag.Int("port", 34254, "Listen port")
	payout := flag.String("payout", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "Payout address (P2PKH/P2WPKH)")
	chainName := flag.String("chain", "mainnet", "Chain name (mainnet, testnet3, regtest, simnet)")
	flag.Parse()

	if *payout == "" {
		fmt.Println("Error: payout address required")
		os.Exit(1)
	}

	var chainParams *chaincfg.Params
	switch *chainName {
	case "mainnet":
		chainParams = &chaincfg.MainNetParams
	case "testnet3":
		chainParams = &chaincfg.TestNet3Params
	case "regtest":
		chainParams = &chaincfg.RegressionNetParams
	case "simnet":
		chainParams = &chaincfg.SimNetParams
	default:
		fmt.Printf("Unknown chain: %s, defaulting to mainnet\n", *chainName)
		chainParams = &chaincfg.MainNetParams
	}

	rpc := NewRPCClient(*rpcURL, *rpcUser, *rpcPass)
	tp := NewTemplateProvider(rpc, *payout, chainParams)
	tp.Start(*port)
}
