// Your mission, should you choose to accept it:
//
// 1. Create a Github repository containing this code.
//
// 2. Write some tests for this service. You may modify the code to make it
// easier to test.
//
// 3. Create a Dockerfile that builds this service into a multiplatform
// amd64/arm64 Docker image.
//
// 4. Setup Github Actions (or your CI/CD provider of choice) so that:
//
//    - When a pull request is opened, the tests run.
//
//    - When a pull request is merged, a Docker image is built and pushed to ECR.
//
// 5. Be ready to answer questions about your work! We will ask you to walk us
// through how to run your Docker image.

package main

import (
	"encoding/json"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	bindAddr := os.Getenv("BIND_ADDR")
	log.Println("Listening on " + bindAddr)
	log.Fatal(http.ListenAndServe(bindAddr, http.HandlerFunc(HandleRequest)))
}

var counter = NewCounter()

func HandleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}

	const limitPerMinute = 120

	info := counter.Add(remoteHost, limitPerMinute, 1)

	w.Header().Set("Content-Type", "application/json")

	if info.Allowed {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusTooManyRequests)
	}

	jsonData, _ := json.MarshalIndent(info, "", "  ")
	w.Write(jsonData)
}

const (
	// WindowDuration is the duration which all limits are specified in terms of
	// (calls per minute or CPM).
	WindowDuration = 1 * time.Minute

	// BurstTolerance determines the size of each bucket. the bucket is sized to
	// allow a burst of 1 second's worth of tokens, on top of the steady rate
	// limit. If 1 second's worth of tokens is less than 1, then the bucket size
	// is 1 and there is no burst tolerance.
	BurstTolerance = 1 * time.Second
)

// Counter implements a leaky bucket algorithm to limit total calls per minute
// (CPM).
type Counter struct {
	sync.Mutex
	// buckets is the current state of the rate limit buckets.
	buckets map[string]Bucket
	Now     func() time.Time
}

// NewCounter creates a new rate limiting counter.
func NewCounter() *Counter {
	return &Counter{
		buckets: map[string]Bucket{},
		Now: func() time.Time {
			return time.Now()
		},
	}
}

// Add checks the current value and size of the rate limit bucket specified by
// "key", based on the given limit per window. It returns Info about the bucket
// state, and true/false to indicate whether the value was successfully added to
// the bucket. If the limit is zero, it always returns success.
func (p *Counter) Add(key string, limitPerWindow int64, add int64) Info {
	p.Lock()
	defer p.Unlock()
	if limitPerWindow == 0 {
		return Info{
			Bucket: key,
			//ResetAt: time.Now(),
			ResetAt: p.Now(),
			Allowed: true,
		}
	}

	now := p.Now()

	existingBucket := p.buckets[key]

	newBucket := existingBucket.Plus(now, limitPerWindow, add)

	newCount := newBucket.CountAt(now)
	bucketSize := BucketSize(limitPerWindow)
	if newCount > bucketSize {
		return Info{
			Bucket:     key,
			ResetAt:    existingBucket.WillReach(bucketSize-add, now),
			BucketSize: bucketSize,
			Remaining:  max(0, bucketSize-existingBucket.CountAt(now)),
			Allowed:    false,
		}
	}

	p.buckets[key] = newBucket

	remaining := bucketSize - newCount
	return Info{
		Bucket:     key,
		ResetAt:    newBucket.WillReach(bucketSize-1, now),
		BucketSize: bucketSize,
		Remaining:  remaining,
		Allowed:    true,
	}
}

// Info contains rate limit information produced by a rate limit check.
type Info struct {
	Bucket     string    `json:"bucket"`
	ResetAt    time.Time `json:"resetAt"`
	BucketSize int64     `json:"bucketSize"`
	Remaining  int64     `json:"remaining"`
	Allowed    bool      `json:"allowed"`
}

// Bucket represents a single rate limit bucket.
type Bucket struct {
	UpdatedAt      time.Time
	LimitPerWindow int64
	Count          float64
}

// CountAt returns the count of the bucket at the given time.
func (b Bucket) CountAt(now time.Time) int64 {
	return int64(math.Ceil(b.countAt(now)))
}

// Plus returns a new copy of the Bucket with the given amount of tokens added
// to it.
func (b Bucket) Plus(now time.Time, limitPerWindow int64, add int64) Bucket {
	return Bucket{
		UpdatedAt:      now,
		LimitPerWindow: limitPerWindow,
		Count:          b.countAt(now) + float64(add),
	}
}

func (b Bucket) countAt(now time.Time) float64 {
	leakage := (float64(b.LimitPerWindow) * float64(now.Sub(b.UpdatedAt))) / float64(WindowDuration)
	return max(0.0, b.Count-leakage)
}

// WillReach returns the time at which the bucket will leak enough to reach the
// given count, or now if it is already at or below the count. If you pass a
// negative count, it will also return now.
func (b Bucket) WillReach(count int64, now time.Time) time.Time {
	if count < 0 {
		return now
	}
	needToLeak := b.Count - float64(count)
	if needToLeak <= 0 {
		return now
	}
	resetAt := b.UpdatedAt.Add(time.Duration(needToLeak*float64(WindowDuration)) / time.Duration(b.LimitPerWindow))
	if resetAt.Before(now) {
		return now
	}
	return resetAt
}

// BucketSize determines the size of each bucket automatically based on the
// given limit per window. The bucket is sized to allow a burst of 1 second's
// worth of tokens, on top of the steady rate limit. If 1 second's worth of
// tokens is less than 1, then the bucket size is 1 and there is no burst
// tolerance.
func BucketSize(limitPerWindow int64) int64 {
	a := limitPerWindow * int64(BurstTolerance)
	b := int64(WindowDuration)
	// positive integer ceiling division
	return (a + b - 1) / b
}
