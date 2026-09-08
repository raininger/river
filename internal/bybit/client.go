package bybit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const recvWindow = "5000"

type Client struct {
	apiKey string
	secret string
	base   string
	hc     *http.Client
}

func New(apiKey, apiSecret string, testnet bool) *Client {
	base := "https://api.bybit.com"
	if testnet {
		base = "https://api-testnet.bybit.com"
	}
	return &Client{
		apiKey: apiKey,
		secret: apiSecret,
		base:   base,
		hc: &http.Client{
			Timeout: 25 * time.Second,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        10,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

func (c *Client) call(ctx context.Context, path string, q url.Values, out any) error {
	qs := q.Encode()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	raw := ts + c.apiKey + recvWindow + qs
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(raw))
	sig := hex.EncodeToString(mac.Sum(nil))

	full := c.base + path
	if qs != "" {
		full += "?" + qs
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)
	req.Header.Set("X-BAPI-SIGN", sig)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env struct {
		RetCode int             `json:"retCode"`
		RetMsg  string          `json:"retMsg"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	if env.RetCode != 0 {
		return fmt.Errorf("bybit 接口错误 retCode=%d retMsg=%s", env.RetCode, env.RetMsg)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("解析 result 失败: %w", err)
		}
	}
	return nil
}
