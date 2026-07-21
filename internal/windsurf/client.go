package windsurf

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/deqiying/fast-context/internal/protowire"
	"github.com/deqiying/fast-context/internal/search"
)

const (
	apiBase  = "https://server.self-serve.windsurf.com/exa.api_server_pb.ApiServerService"
	authBase = "https://server.self-serve.windsurf.com/exa.auth_pb.AuthService"

	wsApp    = "windsurf"
	wsAppVer = "1.48.2"
	wsLSVer  = "1.9544.35"
	wsModel  = "MODEL_SWE_1_6_FAST"
)

type Client struct {
	httpClient *http.Client
	apiBase    string
	authBase   string
}

type Error struct {
	code string
	err  error
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return e.err }
func (e *Error) Code() string  { return e.code }

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	return &Client{httpClient: httpClient, apiBase: apiBase, authBase: authBase}
}

func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if os.Getenv("FC_INSECURE_TLS") == "1" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Transport: transport}
}

func (c *Client) FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error) {
	meta := &protowire.Encoder{}
	meta.String(1, wsApp)
	meta.String(2, envDefault("WS_APP_VER", wsAppVer))
	meta.String(3, apiKey)
	meta.String(4, "zh-cn")
	meta.String(7, envDefault("WS_LS_VER", wsLSVer))
	meta.String(12, wsApp)
	meta.Bytes(30, []byte{0x00, 0x01})

	outer := &protowire.Encoder{}
	outer.Message(1, meta)
	resp, err := c.unary(ctx, c.authBase+"/GetUserJwt", outer.BytesValue(), false, normalizeTimeout(timeout))
	if err != nil {
		return "", err
	}
	for _, s := range protowire.ExtractStrings(resp) {
		if strings.HasPrefix(s, "eyJ") && strings.Contains(s, ".") {
			return s, nil
		}
	}
	return "", &Error{code: "PROTOCOL", err: errors.New("failed to extract JWT from GetUserJwt response")}
}

func (c *Client) CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error) {
	req := &protowire.Encoder{}
	req.Message(1, buildMetadata(apiKey, jwt))
	req.String(3, envDefault("WS_MODEL", wsModel))
	_, err := c.unary(ctx, c.apiBase+"/CheckUserMessageRateLimit", req.BytesValue(), true, normalizeTimeout(timeout))
	if err == nil {
		return true, nil
	}
	var fcErr *Error
	if errors.As(err, &fcErr) && fcErr.Code() == "RATE_LIMITED" {
		return false, nil
	}
	return false, err
}

func (c *Client) Stream(ctx context.Context, apiKey, jwt string, messages []search.Message, toolDefs string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	proto := buildRequest(apiKey, jwt, messages, toolDefs)
	frame, err := protowire.EncodeConnectFrame(proto, true)
	if err != nil {
		return nil, &Error{code: "PROTOCOL", err: err}
	}
	url := c.apiBase + "/GetDevstralStream"
	var lastErr error
	for attempt := 0; attempt <= 2; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(frame))
		if err != nil {
			cancel()
			return nil, err
		}
		traceID := randomHex(16)
		spanID := randomHex(8)
		req.Header.Set("Content-Type", "application/connect+proto")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("Connect-Accept-Encoding", "gzip")
		req.Header.Set("Connect-Content-Encoding", "gzip")
		req.Header.Set("Connect-Timeout-Ms", fmt.Sprint(timeout.Milliseconds()))
		req.Header.Set("User-Agent", "connect-go/1.18.1 (go1.25.5)")
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Baggage", "sentry-release=language-server-windsurf@"+envDefault("WS_LS_VER", wsLSVer)+",sentry-environment=stable,sentry-sampled=false,sentry-trace_id="+traceID+",sentry-public_key=b813f73488da69eedec534dba1029111")
		req.Header.Set("Sentry-Trace", traceID+"-"+spanID+"-0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			cancel()
			lastErr = classify(err, 0)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			return nil, classify(readErr, 0)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = classify(fmt.Errorf("HTTP %d", resp.StatusCode), resp.StatusCode)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return nil, lastErr
			}
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
			}
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

func (c *Client) ParseResponse(data []byte) (string, *search.ToolCall, error) {
	frames, err := protowire.DecodeConnectFrames(data)
	if err != nil {
		return "", nil, &Error{code: "PROTOCOL", err: err}
	}
	var all strings.Builder
	for _, frame := range frames {
		text := strings.ToValidUTF8(string(frame), "")
		if strings.HasPrefix(strings.TrimSpace(text), "{") {
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(text), &payload) == nil && payload.Error.Message != "" {
				return "[Error] " + payload.Error.Code + ": " + payload.Error.Message, nil, nil
			}
		}
		if strings.Contains(text, "[TOOL_CALLS]") || strings.Contains(text, "<ANSWER>") {
			all.WriteString(text)
		}
		for _, s := range protowire.ExtractStrings(frame) {
			if strings.Contains(s, "[TOOL_CALLS]") || strings.Contains(s, "<ANSWER>") {
				all.WriteString(s)
			}
		}
	}
	text := all.String()
	toolCall, thinking, err := search.ParseToolCall(text)
	if err != nil {
		return thinking, nil, &Error{code: "PROTOCOL", err: err}
	}
	if toolCall != nil {
		return thinking, toolCall, nil
	}
	return text, nil, nil
}

func (c *Client) unary(ctx context.Context, url string, proto []byte, compress bool, timeout time.Duration) ([]byte, error) {
	body := proto
	if compress {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write(proto); err != nil {
			return nil, err
		}
		if err := gz.Close(); err != nil {
			return nil, err
		}
		body = b.Bytes()
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", "connect-go/1.18.1 (go1.25.5)")
	if compress {
		req.Header.Set("Content-Encoding", "gzip")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, classify(err, 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classify(fmt.Errorf("HTTP %d", resp.StatusCode), resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func buildRequest(apiKey, jwt string, messages []search.Message, toolDefs string) []byte {
	req := &protowire.Encoder{}
	req.Message(1, buildMetadata(apiKey, jwt))
	for _, m := range messages {
		req.Message(2, buildChatMessage(m))
	}
	req.String(3, toolDefs)
	return req.BytesValue()
}

func buildMetadata(apiKey, jwt string) *protowire.Encoder {
	meta := &protowire.Encoder{}
	meta.String(1, wsApp)
	meta.String(2, envDefault("WS_APP_VER", wsAppVer))
	meta.String(3, apiKey)
	meta.String(4, "zh-cn")
	sysInfo := map[string]any{
		"Os":             runtime.GOOS,
		"Arch":           runtime.GOARCH,
		"Release":        "",
		"Version":        runtime.Version(),
		"Machine":        runtime.GOARCH,
		"Nodename":       hostname(),
		"Sysname":        sysname(),
		"ProductVersion": "",
	}
	sysJSON, _ := json.Marshal(sysInfo)
	meta.String(5, string(sysJSON))
	meta.String(7, envDefault("WS_LS_VER", wsLSVer))
	cpuInfo := map[string]any{
		"NumSockets": 1,
		"NumCores":   runtime.NumCPU(),
		"NumThreads": runtime.NumCPU(),
		"VendorID":   "",
		"Family":     "0",
		"Model":      "0",
		"ModelName":  "Unknown",
		"Memory":     0,
	}
	cpuJSON, _ := json.Marshal(cpuInfo)
	meta.String(8, string(cpuJSON))
	meta.String(12, wsApp)
	meta.String(21, jwt)
	meta.Bytes(30, []byte{0x00, 0x01})
	return meta
}

func buildChatMessage(m search.Message) *protowire.Encoder {
	msg := &protowire.Encoder{}
	msg.Varint(2, uint64(m.Role))
	msg.String(3, m.Content)
	if m.ToolCallID != "" && m.ToolName != "" && m.ToolArgsJSON != "" {
		tc := &protowire.Encoder{}
		tc.String(1, m.ToolCallID)
		tc.String(2, m.ToolName)
		tc.String(3, m.ToolArgsJSON)
		msg.Message(6, tc)
	}
	if m.RefCallID != "" {
		msg.String(7, m.RefCallID)
	}
	return msg
}

func classify(err error, status int) error {
	code := "NETWORK"
	switch {
	case status == http.StatusRequestEntityTooLarge:
		code = "PAYLOAD_TOO_LARGE"
	case status == http.StatusTooManyRequests:
		code = "RATE_LIMITED"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = "AUTH_ERROR"
	case status >= 500:
		code = "SERVER_ERROR"
	case status != 0:
		code = "SERVER_ERROR"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout"):
		code = "TIMEOUT"
	}
	return &Error{code: code, err: err}
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

func sysname() string {
	switch runtime.GOOS {
	case "darwin":
		return "Darwin"
	case "windows":
		return "Windows_NT"
	default:
		return "Linux"
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(buf)
}
