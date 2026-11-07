package server

import (
	"testing"
	"time"
)

// 刮削名额按登记顺序发放：先登记的任务先开始，后登记的不会插队。
func TestScrapeLimiterGrantsInReserveOrder(t *testing.T) {
	var limiter scrapeLimiter
	const limit = 3
	for range limit {
		limiter.acquire(limit)
	}
	waiters := make([]<-chan struct{}, 50)
	for i := range waiters {
		waiters[i] = limiter.reserve(limit)
	}
	for i, ready := range waiters {
		// 下一个之前的都已拿到名额，之后的都还在等待。
		select {
		case <-ready:
			t.Fatalf("waiter %d granted before a slot was released", i)
		default:
		}
		limiter.release()
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d was not granted after release", i)
		}
		for j := i + 1; j < len(waiters); j++ {
			select {
			case <-waiters[j]:
				t.Fatalf("waiter %d jumped ahead of waiter %d", j, i+1)
			default:
			}
		}
	}
	for range limit {
		limiter.release()
	}
	if limiter.running != 0 || len(limiter.waiters) != 0 {
		t.Fatalf("limiter not drained: running=%d waiters=%d", limiter.running, len(limiter.waiters))
	}
}
