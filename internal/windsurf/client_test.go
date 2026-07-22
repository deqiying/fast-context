package windsurf

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
