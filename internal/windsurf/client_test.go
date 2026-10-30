package windsurf

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deqiying/fast-context/internal/protowire"
	"github.com/deqiying/fast-context/internal/search"
)

func TestFetchJWTGzipResponse(t *testing.T) {
	jwt := "eyJheader.payload.signature"
	authResponse := &protowire.Encoder{}
	authResponse.String(1, jwt)
	compressed := gzipTestPayload(t, authResponse.BytesValue())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/proto")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.authBase = server.URL
	got, err := client.FetchJWT(context.Background(), "fixture-key", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != jwt {
		t.Fatalf("FetchJWT = %q, want %q", got, jwt)
	}
}

func TestFetchJWTAndHTTPErrorClassification(t *testing.T) {
	jwt := "eyJheader.payload.signature"
	authResponse := &protowire.Encoder{}
	authResponse.String(1, jwt)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/GetUserJwt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(authResponse.BytesValue())
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.authBase = server.URL
	got, err := client.FetchJWT(context.Background(), "fixture-key", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != jwt {
		t.Fatalf("FetchJWT = %q, want %q", got, jwt)
	}

	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	client.authBase = unauthorized.URL
	_, err = client.FetchJWT(context.Background(), "fixture-key", time.Second)
	assertErrorCode(t, err, "AUTH_ERROR")
}

func TestCheckRateLimitAndServerError(t *testing.T) {
	status := atomic.Int32{}
	status.Store(http.StatusTooManyRequests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.apiBase = server.URL
	ok, err := client.CheckRateLimit(context.Background(), "fixture-key", "fixture-jwt", time.Second)
	if err != nil || ok {
		t.Fatalf("rate-limit result ok=%t err=%v", ok, err)
	}

	status.Store(http.StatusInternalServerError)
	ok, err = client.CheckRateLimit(context.Background(), "fixture-key", "fixture-jwt", time.Second)
	if ok {
		t.Fatal("server error must not report rate-limit check success")
	}
	assertErrorCode(t, err, "SERVER_ERROR")
}

func TestFetchJWTTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.authBase = server.URL
	_, err := client.FetchJWT(context.Background(), "fixture-key", 10*time.Millisecond)
	assertErrorCode(t, err, "TIMEOUT")
}

func TestStreamRetriesServerErrorAndReadsBodyBeforeCancel(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "stream-ok")
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.apiBase = server.URL
	body, err := client.Stream(context.Background(), "fixture-key", "fixture-jwt", nil, "{}", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "stream-ok" || calls.Load() != 2 {
		t.Fatalf("body=%q calls=%d", body, calls.Load())
	}
}

func TestParseResponseAndMalformedFrame(t *testing.T) {
	payload := &protowire.Encoder{}
	payload.String(1, "<ANSWER><files></files></ANSWER>")
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(nil)
	text, toolCall, err := client.ParseResponse(frame)
	if err != nil {
		t.Fatal(err)
	}
	if toolCall != nil || !strings.Contains(text, "<ANSWER>") {
		t.Fatalf("text=%q toolCall=%#v", text, toolCall)
	}

	_, _, err = client.ParseResponse([]byte{0x00, 0x00, 0x00, 0x00, 0x05, 0x01})
	assertErrorCode(t, err, "PROTOCOL")
}

func TestParseResponseKeepsPlainTextWhenNoMarkers(t *testing.T) {
	payload := &protowire.Encoder{}
	payload.String(1, "short-id")
	payload.String(2, "I could not find any authentication code in this repository.")
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}
	text, toolCall, err := NewClient(nil).ParseResponse(frame)
	if err != nil {
		t.Fatal(err)
	}
	if toolCall != nil {
		t.Fatalf("unexpected tool call: %#v", toolCall)
	}
	if text != "I could not find any authentication code in this repository." {
		t.Fatalf("plain response was not preserved without short protobuf strings: %q", text)
	}
}

func TestParseResponseRepairsUnquotedToolArgumentKeys(t *testing.T) {
	payload := &protowire.Encoder{}
	payload.String(1, `thinking[TOOL_CALLS]restricted_exec[ARGS]{"command1":{"type":"rg","pattern":"Auth","path":"/codebase",exclude:["**/dist/**"]}}`)
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}
	_, toolCall, err := NewClient(nil).ParseResponse(frame)
	if err != nil {
		t.Fatal(err)
	}
	if toolCall == nil || toolCall.Name != "restricted_exec" || !toolCall.Repaired {
		t.Fatalf("toolCall=%#v", toolCall)
	}
}

func TestParseResponseMarksMalformedToolArgumentsRecoverable(t *testing.T) {
	payload := &protowire.Encoder{}
	payload.String(1, `[TOOL_CALLS]restricted_exec[ARGS]{"command1",{"type":"rg"}}`)
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = NewClient(nil).ParseResponse(frame)
	assertErrorCode(t, err, "PROTOCOL")
	var malformed *search.MalformedToolCallError
	if !errors.As(err, &malformed) {
		t.Fatalf("error = %T %v, want wrapped MalformedToolCallError", err, err)
	}
}

func TestParseResponsePreservesRemoteInvalidArgument(t *testing.T) {
	frame, err := protowire.EncodeConnectFrame([]byte(`{"error":{"code":"invalid_argument","message":"internal error (trace ID: fixture-trace)"}}`), true)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = NewClient(nil).ParseResponse(frame)
	assertErrorCode(t, err, "invalid_argument")
	if !strings.Contains(err.Error(), "fixture-trace") {
		t.Fatalf("remote trace ID was not preserved: %v", err)
	}
}

func TestStreamRejectsOversizeProtoBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "unexpected")
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.apiBase = server.URL
	messages := []search.Message{{Role: 1, Content: strings.Repeat("x", 321*1024)}}
	_, err := client.Stream(context.Background(), "fixture-key", "fixture-jwt", messages, "{}", time.Second)
	assertErrorCode(t, err, "PAYLOAD_TOO_LARGE")
	if calls.Load() != 0 {
		t.Fatalf("oversize request reached HTTP server: calls=%d", calls.Load())
	}
}

func TestStreamRejectsInvalidUTF8BeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.apiBase = server.URL
	_, err := client.Stream(
		context.Background(),
		"fixture-key",
		"fixture-jwt",
		[]search.Message{{Role: 1, Content: string([]byte{'x', 0xff})}},
		"{}",
		time.Second,
	)
	assertErrorCode(t, err, "PROTOCOL")
	if calls.Load() != 0 {
		t.Fatalf("invalid UTF-8 request reached HTTP server: calls=%d", calls.Load())
	}
}

func TestBuildRequestRejectsInvalidUTF8AcrossOutboundFields(t *testing.T) {
	invalid := string([]byte{'x', 0xff})
	validMessage := search.Message{
		Role:         2,
		Content:      "content",
		ToolCallID:   "call-id",
		ToolName:     "restricted_exec",
		ToolArgsJSON: "{}",
		RefCallID:    "ref-id",
	}
	tests := []struct {
		name     string
		apiKey   string
		jwt      string
		messages []search.Message
		toolDefs string
	}{
		{name: "api key", apiKey: invalid, jwt: "jwt", messages: []search.Message{validMessage}, toolDefs: "{}"},
		{name: "jwt", apiKey: "key", jwt: invalid, messages: []search.Message{validMessage}, toolDefs: "{}"},
		{name: "tool definitions", apiKey: "key", jwt: "jwt", messages: []search.Message{validMessage}, toolDefs: invalid},
	}
	messageFields := []struct {
		name   string
		mutate func(*search.Message)
	}{
		{name: "content", mutate: func(message *search.Message) { message.Content = invalid }},
		{name: "tool call id", mutate: func(message *search.Message) { message.ToolCallID = invalid }},
		{name: "tool name", mutate: func(message *search.Message) { message.ToolName = invalid }},
		{name: "tool args", mutate: func(message *search.Message) { message.ToolArgsJSON = invalid }},
		{name: "ref call id", mutate: func(message *search.Message) { message.RefCallID = invalid }},
	}
	for _, field := range messageFields {
		message := validMessage
		field.mutate(&message)
		tests = append(tests, struct {
			name     string
			apiKey   string
			jwt      string
			messages []search.Message
			toolDefs string
		}{name: field.name, apiKey: "key", jwt: "jwt", messages: []search.Message{message}, toolDefs: "{}"})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildRequest(test.apiKey, test.jwt, test.messages, test.toolDefs)
			assertErrorCode(t, err, "PROTOCOL")
		})
	}
}

func TestParseResponsePreservesEmbeddedRemoteError(t *testing.T) {
	payload := &protowire.Encoder{}
	payload.String(1, `{"error":{"code":"invalid_argument","message":"embedded trace ID: abc123"}}`)
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = NewClient(nil).ParseResponse(frame)
	assertErrorCode(t, err, "invalid_argument")
	if !strings.Contains(err.Error(), "abc123") {
		t.Fatalf("embedded remote message was not preserved: %v", err)
	}
}

func TestClassifyHTTPErrorPreservesStatusPriorityAndRemoteCode(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_argument","message":"fixture trace ID: abc123"}}`)
	tests := []struct {
		status int
		code   string
	}{
		{status: http.StatusBadRequest, code: "invalid_argument"},
		{status: http.StatusUnauthorized, code: "AUTH_ERROR"},
		{status: http.StatusForbidden, code: "AUTH_ERROR"},
		{status: http.StatusTooManyRequests, code: "RATE_LIMITED"},
		{status: http.StatusRequestEntityTooLarge, code: "PAYLOAD_TOO_LARGE"},
		{status: http.StatusBadGateway, code: "SERVER_ERROR"},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			err := classifyHTTPError(test.status, body, false)
			assertErrorCode(t, err, test.code)
			if !strings.Contains(err.Error(), "abc123") {
				t.Fatalf("sanitized remote diagnostic missing: %v", err)
			}
		})
	}
}

func TestStreamPreservesHTTP400RemoteCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_argument","message":"fixture trace ID: abc123"}}`)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.apiBase = server.URL
	_, err := client.Stream(context.Background(), "fixture-key", "fixture-jwt", nil, "{}", time.Second)
	assertErrorCode(t, err, "invalid_argument")
	if !strings.Contains(err.Error(), "abc123") {
		t.Fatalf("remote diagnostic missing: %v", err)
	}
}

func TestClassifyHTTPErrorRejectsInvalidRemoteCode(t *testing.T) {
	for _, code := range []string{"INVALID ARGUMENT\n", " invalid_argument ", strings.Repeat("a", 65)} {
		body, err := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": "fixture"}})
		if err != nil {
			t.Fatal(err)
		}
		assertErrorCode(t, classifyHTTPError(http.StatusBadRequest, body, false), "SERVER_ERROR")
	}
}

func TestRemoteMessageIsSanitizedBoundedAndKeepsTrace(t *testing.T) {
	message := "\x1b[31m\x00" + strings.Repeat("x", 3000) + " trace ID: abc123"
	body, err := json.Marshal(map[string]any{"error": map[string]any{"code": "invalid_argument", "message": message}})
	if err != nil {
		t.Fatal(err)
	}
	err = classifyHTTPError(http.StatusBadRequest, body, false)
	assertErrorCode(t, err, "invalid_argument")
	if len(err.Error()) > maxRemoteMessageBytes || strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "\x00") {
		t.Fatalf("remote message was not sanitized: bytes=%d value=%q", len(err.Error()), err.Error())
	}
	if !strings.Contains(err.Error(), "trace ID: abc123") {
		t.Fatalf("trace ID was not preserved: %q", err.Error())
	}
}

func TestRequestSizeUsesBuiltProtobuf(t *testing.T) {
	client := NewClient(nil)
	messages := []search.Message{{Role: 1, Content: "fixture"}}
	got, err := client.RequestSize("fixture-key", "fixture-jwt", messages, "{}")
	if err != nil {
		t.Fatal(err)
	}
	proto, err := buildRequest("fixture-key", "fixture-jwt", messages, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if got != len(proto) {
		t.Fatalf("RequestSize = %d, want %d", got, len(proto))
	}
}

func TestSearchInvalidArgumentRecoveryUsesExactWindsurfRequestSize(t *testing.T) {
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 240; i++ {
		path := filepath.Join(root, "src", fmt.Sprintf("fixture-file-%03d-with-a-long-name.go", i))
		if err := os.WriteFile(path, []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	toolFrame := connectTextFrame(t, `[TOOL_CALLS]restricted_exec[ARGS]{"command1":{"type":"tree","path":"/codebase/src","levels":2}}`)
	answerFrame := connectTextFrame(t, `[TOOL_CALLS]answer[ARGS]{"answer":"<ANSWER><file path=\"/codebase/src/fixture-file-000-with-a-long-name.go\"><range>1-1</range></file></ANSWER>"}`)
	authResponse := &protowire.Encoder{}
	authResponse.String(1, "eyJfixture.payload.signature")

	var streamCalls atomic.Int32
	var sizesMu sync.Mutex
	var requestSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/GetUserJwt":
			_, _ = w.Write(authResponse.BytesValue())
		case "/CheckUserMessageRateLimit":
			w.WriteHeader(http.StatusOK)
		case "/GetDevstralStream":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			frames, err := protowire.DecodeConnectFrames(body)
			if err != nil || len(frames) != 1 {
				http.Error(w, "invalid request frame", http.StatusInternalServerError)
				return
			}
			sizesMu.Lock()
			requestSizes = append(requestSizes, len(frames[0]))
			sizesMu.Unlock()
			switch streamCalls.Add(1) {
			case 1:
				_, _ = w.Write(toolFrame)
			case 2:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_argument","message":"fixture trace ID: exact-size"}}`)
			case 3:
				_, _ = w.Write(answerFrame)
			default:
				http.Error(w, "unexpected stream call", http.StatusInternalServerError)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.authBase = server.URL
	client.apiBase = server.URL
	result, err := search.Run(context.Background(), search.Options{
		Query:            "find the fixture file",
		ProjectRoot:      root,
		TreeDepth:        2,
		MaxTurns:         2,
		MaxCommands:      4,
		MaxResults:       4,
		Timeout:          2 * time.Second,
		RepoMapMode:      "classic",
		BootstrapEnabled: false,
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	if streamCalls.Load() != 3 || !result.Meta.ContextTrimmed || result.Meta.ErrorCode != "" {
		t.Fatalf("unexpected recovery result: calls=%d meta=%#v", streamCalls.Load(), result.Meta)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "src/fixture-file-000-with-a-long-name.go" {
		t.Fatalf("unexpected result files: %#v", result.Files)
	}
	sizesMu.Lock()
	sizes := append([]int(nil), requestSizes...)
	sizesMu.Unlock()
	if len(sizes) != 3 || sizes[2] >= sizes[1] || sizes[2] > search.MaxRequestProtoBytes {
		t.Fatalf("recovery did not strictly reduce the real protobuf: sizes=%v limit=%d", sizes, search.MaxRequestProtoBytes)
	}
}

func TestClassifyStatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
		code   string
	}{
		{name: "payload too large", status: http.StatusRequestEntityTooLarge, err: errors.New("HTTP 413"), code: "PAYLOAD_TOO_LARGE"},
		{name: "rate limited", status: http.StatusTooManyRequests, err: errors.New("HTTP 429"), code: "RATE_LIMITED"},
		{name: "forbidden", status: http.StatusForbidden, err: errors.New("HTTP 403"), code: "AUTH_ERROR"},
		{name: "server", status: http.StatusBadGateway, err: errors.New("HTTP 502"), code: "SERVER_ERROR"},
		{name: "network", err: errors.New("connection reset"), code: "NETWORK"},
		{name: "timeout", err: context.DeadlineExceeded, code: "TIMEOUT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertErrorCode(t, classify(test.err, test.status), test.code)
		})
	}
}

func assertErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", want)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Code() != want {
		t.Fatalf("error=%v code=%q, want %q", err, typedCode(err), want)
	}
}

func typedCode(err error) string {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code()
	}
	return ""
}

func gzipTestPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func connectTextFrame(t *testing.T, text string) []byte {
	t.Helper()
	payload := &protowire.Encoder{}
	payload.String(1, text)
	frame, err := protowire.EncodeConnectFrame(payload.BytesValue(), true)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}
