package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/utils/clock"
)

type MockClock struct {
	now time.Time
}

// NewMockClock creates a new MockClock with the specified time.
func NewMockClock() *MockClock {
	return &MockClock{
		now: time.Now(),
	}
}

// Implement clock.Clock interface for MockClock
func (m *MockClock) Now() time.Time {
	return m.now
}

func (m *MockClock) Advance(d time.Duration) {
	m.now = m.now.Add(d)
}

func (m *MockClock) Since(t time.Time) time.Duration {
	return m.now.Sub(t)
}

func resetCounter(c clock.PassiveClock) {
	counter = NewCounter()
	counter.Now = func() time.Time {
		return c.Now()
	}
}

func TestHandleRequest_GETAllowed(t *testing.T) {
	resetCounter(NewMockClock())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()

	HandleRequest(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !info.Allowed {
		t.Errorf("expected Allowed=true, got false")
	}
}

func TestHandleRequest_MethodNotAllowed(t *testing.T) {
	resetCounter(NewMockClock())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()

	HandleRequest(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Method not allowed") {
		t.Errorf("expected body to contain 'Method not allowed', got %q", string(body))
	}
}

// Test with go test -race ./...
func TestThreadSafety(t *testing.T) {
	var waiter sync.WaitGroup
	for i := 0; i < 100; i++ {
		waiter.Add(1)
		go func() {
			defer waiter.Done()
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "1.2.3.4:5678"
			HandleRequest(rr, req)
		}()
	}
	waiter.Wait()
}

func TestHandleRequest_RateLimitExceeded(t *testing.T) {
	clock := NewMockClock()
	resetCounter(clock)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:1234"

	var rr *httptest.ResponseRecorder

	for i := 0; i < 3; i++ {
		rr = httptest.NewRecorder()
		HandleRequest(rr, req)
		resp := rr.Result()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d on request %d", resp.StatusCode, i+1)
		}
		resp.Body.Close()
		clock.Advance(300 * time.Millisecond)
	}

	// request should be rate limited
	rr = httptest.NewRecorder()
	HandleRequest(rr, req)
	resp := rr.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if info.Allowed {
		t.Errorf("expected Allowed=false, got true")
	}
}
