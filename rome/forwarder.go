package rome

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
)

type TxSubscriber interface {
	SubscribeTransactions(ch chan<- core.NewTxsEvent, reorgs bool) event.Subscription
}

type jsonrpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      uint64        `json:"id"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	ID      uint64          `json:"id"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ProxyForwarder subscribes to new transactions in the txpool and forwards
// them to the Rome proxy via eth_sendRawTransaction.
type ProxyForwarder struct {
	proxyURL   string
	httpClient *http.Client
	txSub      TxSubscriber

	quit chan struct{}
	wg   sync.WaitGroup

	mu    sync.Mutex
	reqID uint64
}

func NewProxyForwarder(proxyURL string, txSub TxSubscriber) *ProxyForwarder {
	return &ProxyForwarder{
		proxyURL: proxyURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		txSub: txSub,
		quit:  make(chan struct{}),
	}
}

func (f *ProxyForwarder) Start() {
	f.wg.Add(1)
	go f.loop()
	log.Info("Rome proxy forwarder started", "proxy", f.proxyURL)
}

func (f *ProxyForwarder) Stop() {
	close(f.quit)
	f.wg.Wait()
	log.Info("Rome proxy forwarder stopped")
}

func (f *ProxyForwarder) loop() {
	defer f.wg.Done()

	txsCh := make(chan core.NewTxsEvent, 256)
	sub := f.txSub.SubscribeTransactions(txsCh, false)
	defer sub.Unsubscribe()

	for {
		select {
		case ev := <-txsCh:
			for _, tx := range ev.Txs {
				if err := f.forwardTx(tx); err != nil {
					log.Warn("Failed to forward tx to Rome proxy", "hash", tx.Hash(), "err", err)
				} else {
					log.Debug("Forwarded tx to Rome proxy", "hash", tx.Hash())
				}
			}
		case err := <-sub.Err():
			if err != nil {
				log.Error("Rome proxy forwarder subscription error", "err", err)
			}
			return
		case <-f.quit:
			return
		}
	}
}

func (f *ProxyForwarder) forwardTx(tx *types.Transaction) error {
	data, err := tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal tx: %w", err)
	}

	f.mu.Lock()
	f.reqID++
	id := f.reqID
	f.mu.Unlock()

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "eth_sendRawTransaction",
		Params:  []interface{}{hexutil.Encode(data)},
		ID:      id,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.proxyURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp jsonrpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("proxy RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return nil
}
