package bybit

import (
	"bytes"
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

// APIError 表示 Bybit 返回的业务错误(retCode != 0)。
// 哨兵需要按错误码区分「限频重试」和「仓位已不存在」, 所以不能只当字符串处理。
type APIError struct {
	RetCode int
	RetMsg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bybit 接口错误 retCode=%d retMsg=%s", e.RetCode, e.RetMsg)
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
	return c.do(ctx, http.MethodGet, path, qs, []byte(qs), out)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}
	return c.do(ctx, http.MethodPost, path, "", b, out)
}

// do 发送带签名的请求。签名串为 ts + apiKey + recvWindow + payload,
// 其中 GET 的 payload 是查询串, POST 的是 JSON 请求体。
func (c *Client) do(ctx context.Context, method, path, query string, payload []byte, out any) error {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	raw := ts + c.apiKey + recvWindow + string(payload)
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(raw))
	sig := hex.EncodeToString(mac.Sum(nil))

	full := c.base + path
	if query != "" {
		full += "?" + query
	}

	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
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
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env struct {
		RetCode int             `json:"retCode"`
		RetMsg  string          `json:"retMsg"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		// IP 被限频时 Bybit 会返回 403 且响应体不是标准信封, 这里把状态码一并带出来便于排查
		return fmt.Errorf("解析响应失败(HTTP %d): %w: %.200s", resp.StatusCode, err, respBody)
	}
	if env.RetCode != 0 {
		return &APIError{RetCode: env.RetCode, RetMsg: env.RetMsg}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("解析 result 失败: %w", err)
		}
	}
	return nil
}
