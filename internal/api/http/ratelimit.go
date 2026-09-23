package http

import (
	"container/heap"
	"slices"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const maxCapacityInspection = 4

type tokenBucket struct {
	key        string
	tokens     int
	lastRefill time.Time
	lastUsed   time.Time
	evictAt    time.Time
	heapIndex  int
}

type bucketExpiryHeap []*tokenBucket

func (h *bucketExpiryHeap) Len() int { return len(*h) }

func (h *bucketExpiryHeap) Less(i, j int) bool { return (*h)[i].evictAt.Before((*h)[j].evictAt) }

func (h *bucketExpiryHeap) Swap(i, j int) {
	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
	(*h)[i].heapIndex = i
	(*h)[j].heapIndex = j
}

func (h *bucketExpiryHeap) Push(value any) {
	bucket := mustTokenBucket(value)
	bucket.heapIndex = len(*h)
	*h = append(*h, bucket)
}

func mustTokenBucket(value any) *tokenBucket {
	bucket, ok := value.(*tokenBucket)
	if !ok {
		panic("login limiter heap contains an invalid value")
	}
	return bucket
}

func (h *bucketExpiryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	bucket := old[last]
	old[last] = nil
	bucket.heapIndex = -1
	*h = old[:last]
	return bucket
}

type loginLimiter struct {
	mu       sync.Mutex
	clock    core.Clock
	refill   time.Duration
	burst    int
	maxKeys  int
	buckets  map[string]*tokenBucket
	expiring bucketExpiryHeap
}

func newLoginLimiter(clock core.Clock, refill time.Duration, burst, maxKeys int) *loginLimiter {
	return &loginLimiter{
		clock: clock, refill: refill, burst: burst, maxKeys: maxKeys,
		buckets:  make(map[string]*tokenBucket, maxKeys),
		expiring: make(bucketExpiryHeap, 0, maxKeys),
	}
}

func (l *loginLimiter) allow(ipKey, usernameKey string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	ip := l.buckets[ipKey]
	username := l.buckets[usernameKey]
	if allowed, retry := l.existingBucketsAllow(now, ip, username); !allowed {
		return false, retry
	}
	missing := missingBucketCount(ip, username)
	if allowed, retry := l.ensureCapacity(now, missing, ipKey, usernameKey); !allowed {
		return false, retry
	}
	if ip == nil {
		ip = l.newBucket(now, ipKey)
	}
	if username == nil {
		username = l.newBucket(now, usernameKey)
	}
	ip.tokens--
	username.tokens--
	l.markUsed(now, ip)
	l.markUsed(now, username)
	return true, 0
}

func (l *loginLimiter) allowOne(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	bucket := l.buckets[key]
	if allowed, retry := l.existingBucketsAllow(now, bucket); !allowed {
		return false, retry
	}
	if bucket == nil {
		if allowed, retry := l.ensureCapacity(now, 1, key); !allowed {
			return false, retry
		}
		bucket = l.newBucket(now, key)
	}
	bucket.tokens--
	l.markUsed(now, bucket)
	return true, 0
}

func (l *loginLimiter) existingBucketsAllow(now time.Time, buckets ...*tokenBucket) (bool, time.Duration) {
	var retry time.Duration
	for _, bucket := range buckets {
		if bucket == nil {
			continue
		}
		l.refillBucket(now, bucket)
		retry = max(retry, l.retryAfter(now, bucket))
	}
	return retry == 0, retry
}

func missingBucketCount(left, right *tokenBucket) int {
	missing := 0
	if left == nil {
		missing++
	}
	if right == nil {
		missing++
	}
	return missing
}

func (l *loginLimiter) ensureCapacity(now time.Time, missing int, protected ...string) (bool, time.Duration) {
	needed := len(l.buckets) + missing - l.maxKeys
	if needed <= 0 {
		return true, 0
	}
	popped, count, candidates := l.capacityCandidates(needed, protected)
	if candidates < needed {
		l.restorePopped(popped, count)
		return false, l.refill
	}
	availableAt := nthCandidate(popped, count, protected, needed).evictAt
	if availableAt.After(now) {
		l.restorePopped(popped, count)
		return false, availableAt.Sub(now)
	}
	l.evictCandidates(popped, count, protected, needed)
	l.restorePopped(popped, count)
	return true, 0
}

func (l *loginLimiter) capacityCandidates(
	needed int,
	protected []string,
) ([maxCapacityInspection]*tokenBucket, int, int) {
	var popped [maxCapacityInspection]*tokenBucket
	count := 0
	candidates := 0
	for range maxCapacityInspection {
		if l.expiring.Len() == 0 || candidates == needed {
			break
		}
		bucket := mustTokenBucket(heap.Pop(&l.expiring))
		popped[count] = bucket
		count++
		if !protectedKey(bucket.key, protected) {
			candidates++
		}
	}
	return popped, count, candidates
}

func nthCandidate(
	popped [maxCapacityInspection]*tokenBucket,
	count int,
	protected []string,
	want int,
) *tokenBucket {
	found := 0
	for index := range count {
		if protectedKey(popped[index].key, protected) {
			continue
		}
		found++
		if found == want {
			return popped[index]
		}
	}
	return nil
}

func (l *loginLimiter) evictCandidates(
	popped [maxCapacityInspection]*tokenBucket,
	count int,
	protected []string,
	needed int,
) {
	evicted := 0
	for index := range count {
		bucket := popped[index]
		if protectedKey(bucket.key, protected) {
			continue
		}
		delete(l.buckets, bucket.key)
		popped[index] = nil
		evicted++
		if evicted == needed {
			return
		}
	}
}

func protectedKey(key string, protected []string) bool {
	return slices.Contains(protected, key)
}

func (l *loginLimiter) restorePopped(popped [maxCapacityInspection]*tokenBucket, count int) {
	for index := range count {
		bucket := popped[index]
		if bucket == nil {
			continue
		}
		if _, retained := l.buckets[bucket.key]; retained {
			heap.Push(&l.expiring, bucket)
		}
	}
}

func (l *loginLimiter) refillBucket(now time.Time, bucket *tokenBucket) {
	if now.Before(bucket.lastRefill) {
		return
	}
	added := int(now.Sub(bucket.lastRefill) / l.refill)
	if added <= 0 {
		return
	}
	bucket.tokens = min(l.burst, bucket.tokens+added)
	bucket.lastRefill = bucket.lastRefill.Add(time.Duration(added) * l.refill)
}

func (l *loginLimiter) retryAfter(now time.Time, bucket *tokenBucket) time.Duration {
	if bucket.tokens > 0 {
		return 0
	}
	if now.Before(bucket.lastRefill) {
		return l.refill
	}
	retry := l.refill - now.Sub(bucket.lastRefill)
	if retry <= 0 {
		return l.refill
	}
	return retry
}

func (l *loginLimiter) newBucket(now time.Time, key string) *tokenBucket {
	bucket := &tokenBucket{key: key, tokens: l.burst, lastRefill: now, lastUsed: now, heapIndex: -1}
	l.buckets[key] = bucket
	return bucket
}

func (l *loginLimiter) markUsed(now time.Time, bucket *tokenBucket) {
	if now.After(bucket.lastUsed) {
		bucket.lastUsed = now
	}
	fullAt := bucket.lastRefill.Add(time.Duration(l.burst-bucket.tokens) * l.refill)
	bucket.evictAt = maxTime(fullAt, bucket.lastUsed.Add(l.refill))
	if bucket.heapIndex < 0 {
		heap.Push(&l.expiring, bucket)
		return
	}
	heap.Fix(&l.expiring, bucket.heapIndex)
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func (l *loginLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
