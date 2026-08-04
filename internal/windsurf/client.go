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
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/deqiying/fast-context/internal/protowire"
	"github.com/deqiying/fast-context/internal/search"
	"github.com/deqiying/fast-context/internal/textutil"
)

const (
	apiBase  = "https://server.self-serve.windsurf.com/exa.api_server_pb.ApiServerService"
	authBase = "https://server.self-serve.windsurf.com/exa.auth_pb.AuthService"

	wsApp    = "windsurf"
	wsAppVer = "1.48.2"
	wsLSVer  = "1.9544.35"
	wsModel  = "MODEL_SWE_1_6_FAST"

	maxRemoteErrorBodyBytes = 64 * 1024
	maxRemoteMessageBytes   = 2 * 1024
)

var (
	remoteCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	traceIDRe    = regexp.MustCompile(`(?i)trace\s*ID\s*[:=]\s*[a-z0-9-]{1,128}`)
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

func (c *Client) RequestSize(apiKey, jwt string, messages []search.Message, toolDefs string) (int, error) {
	proto, err := buildRequest(apiKey, jwt, messages, toolDefs)
	if err != nil {
		return 0, err
	}
	return len(proto), nil
}

func (c *Client) Stream(ctx context.Context, apiKey, jwt string, messages []search.Message, toolDefs string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	proto, err := buildRequest(apiKey, jwt, messages, toolDefs)
	if err != nil {
		return nil, err
	}
	if len(proto) > search.MaxRequestProtoBytes {
		return nil, &Error{
			code: "PAYLOAD_TOO_LARGE",
			err:  fmt.Errorf("request protobuf is %d bytes; limit is %d bytes", len(proto), search.MaxRequestProtoBytes),
		}
	}
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
		var body []byte
		var bodyTooLarge bool
		var readErr error
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			body, readErr = io.ReadAll(resp.Body)
		} else {
			body, bodyTooLarge, readErr = readLimitedErrorBody(resp.Body)
		}
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			return nil, classify(readErr, 0)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = classifyHTTPError(resp.StatusCode, body, bodyTooLarge)
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
		if remote, ok := parseRemoteError(frame); ok {
			return "", nil, remoteTypedError(remote, "PROTOCOL")
		}
		text := strings.ToValidUTF8(string(frame), "")
		if strings.Contains(text, "[TOOL_CALLS]") || strings.Contains(text, "<ANSWER>") {
			all.WriteString(text)
		}
		for _, s := range protowire.ExtractStrings(frame) {
			if remote, ok := parseRemoteError([]byte(s)); ok {
				return "", nil, remoteTypedError(remote, "PROTOCOL")
			}
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
		body, tooLarge, readErr := readLimitedErrorBody(resp.Body)
		if readErr != nil {
			return nil, classify(readErr, 0)
		}
		return nil, classifyHTTPError(resp.StatusCode, body, tooLarge)
	}
	return io.ReadAll(resp.Body)
}

func buildRequest(apiKey, jwt string, messages []search.Message, toolDefs string) ([]byte, error) {
	for field, value := range map[string]string{
		"api_key":                 apiKey,
		"jwt":                     jwt,
		"app_version":             envDefault("WS_APP_VER", wsAppVer),
		"language_server_version": envDefault("WS_LS_VER", wsLSVer),
		"hostname":                hostname(),
		"sysname":                 sysname(),
	} {
		if !utf8.ValidString(value) {
			return nil, &Error{code: "PROTOCOL", err: fmt.Errorf("request metadata %s contains invalid UTF-8", field)}
		}
	}
	if !utf8.ValidString(toolDefs) {
		return nil, &Error{code: "PROTOCOL", err: errors.New("tool definitions contain invalid UTF-8")}
	}
	req := &protowire.Encoder{}
	req.Message(1, buildMetadata(apiKey, jwt))
	for _, m := range messages {
		message, err := buildChatMessage(m)
		if err != nil {
			return nil, err
		}
		req.Message(2, message)
	}
	req.String(3, toolDefs)
	return req.BytesValue(), nil
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

func buildChatMessage(m search.Message) (*protowire.Encoder, error) {
	for field, value := range map[string]string{
		"content":        m.Content,
		"tool_call_id":   m.ToolCallID,
		"tool_name":      m.ToolName,
		"tool_args_json": m.ToolArgsJSON,
		"ref_call_id":    m.RefCallID,
	} {
		if !utf8.ValidString(value) {
			return nil, &Error{code: "PROTOCOL", err: fmt.Errorf("message %s contains invalid UTF-8", field)}
		}
	}
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
	return msg, nil
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

type remoteErrorPayload struct {
	Code    string
	Message string
}

func parseRemoteError(data []byte) (remoteErrorPayload, bool) {
	if len(data) == 0 || len(data) > maxRemoteErrorBodyBytes {
		return remoteErrorPayload{}, false
	}
	var payload struct {
		Error   *remoteErrorPayload `json:"error"`
		Code    string              `json:"code"`
		Message string              `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return remoteErrorPayload{}, false
	}
	remote := remoteErrorPayload{Code: payload.Code, Message: payload.Message}
	if payload.Error != nil {
		remote = *payload.Error
	}
	if remote.Code == "" && remote.Message == "" {
		return remoteErrorPayload{}, false
	}
	remote.Message = sanitizeRemoteMessage(remote.Message)
	return remote, true
}

func remoteTypedError(remote remoteErrorPayload, fallbackCode string) error {
	code := fallbackCode
	if remoteCodeRe.MatchString(remote.Code) {
		code = remote.Code
	}
	message := remote.Message
	if message == "" {
		message = "remote request failed"
	}
	if remoteCodeRe.MatchString(remote.Code) && !strings.HasPrefix(message, remote.Code+":") {
		message = remote.Code + ": " + message
	}
	message = sanitizeRemoteMessage(message)
	return &Error{code: code, err: errors.New(message)}
}

func classifyHTTPError(status int, body []byte, bodyTooLarge bool) error {
	baseCode := "SERVER_ERROR"
	switch {
	case status == http.StatusRequestEntityTooLarge:
		baseCode = "PAYLOAD_TOO_LARGE"
	case status == http.StatusTooManyRequests:
		baseCode = "RATE_LIMITED"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		baseCode = "AUTH_ERROR"
	case status >= 500:
		baseCode = "SERVER_ERROR"
	}
	if !bodyTooLarge {
		if remote, ok := parseRemoteError(body); ok {
			if status >= 400 && status < 500 &&
				status != http.StatusUnauthorized && status != http.StatusForbidden &&
				status != http.StatusTooManyRequests && status != http.StatusRequestEntityTooLarge &&
				remoteCodeRe.MatchString(remote.Code) {
				return remoteTypedError(remote, baseCode)
			}
			if remote.Message != "" {
				message := sanitizeRemoteMessage(fmt.Sprintf("HTTP %d: %s", status, remote.Message))
				return &Error{code: baseCode, err: errors.New(message)}
			}
		}
	}
	return &Error{code: baseCode, err: fmt.Errorf("HTTP %d", status)}
}

func readLimitedErrorBody(reader io.Reader) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxRemoteErrorBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > maxRemoteErrorBodyBytes {
		return nil, true, nil
	}
	return body, false, nil
}

func sanitizeRemoteMessage(message string) string {
	message, _ = textutil.NormalizeUTF8(message)
	message = ansiEscapeRe.ReplaceAllString(message, "")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	traceID := traceIDRe.FindString(message)
	trimmed, truncated := textutil.TruncateUTF8Bytes(message, maxRemoteMessageBytes, " ...[truncated]")
	if !truncated || traceID == "" || strings.Contains(trimmed, traceID) {
		return trimmed
	}
	separator := " (" + traceID + ")"
	prefix, _ := textutil.TruncateUTF8Bytes(message, maxRemoteMessageBytes-len(separator), " ...[truncated]")
	return prefix + separator
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
