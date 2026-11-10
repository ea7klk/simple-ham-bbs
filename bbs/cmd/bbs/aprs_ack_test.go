package main

import (
	"fmt"
	"testing"
	"time"
)

func ackReq(id string) aprsAckRequest {
	return aprsAckRequest{User: "EA7KLK", From: "EA1ABC-0", To: "EA7KLK", MessageID: id}
}

func TestAckQueueDeduplicatesWithinWindow(t *testing.T) {
	q := newAPRSAckQueue(&app{})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }

	if !q.Enqueue(ackReq("ABC12")) {
		t.Fatal("first ack was not queued")
	}
	if q.Enqueue(ackReq("ABC12")) {
		t.Fatal("duplicate ack was queued")
	}
	if _, dropped := q.Stats(); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	// Past the dedupe window the same ID may be acked again.
	now = now.Add(aprsAckDedupeWindow + time.Minute)
	if !q.Enqueue(ackReq("ABC12")) {
		t.Fatal("ack was not re-queued after the dedupe window")
	}
}

func TestAckQueueDropsWhenFull(t *testing.T) {
	q := newAPRSAckQueue(&app{})
	fixed := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return fixed }

	for i := 0; i < aprsAckQueueSize; i++ {
		if !q.Enqueue(ackReq(fmt.Sprintf("%05d", i))) {
			t.Fatalf("ack %d was dropped before the queue was full", i)
		}
	}
	// The queue is full and no worker is running.
	if q.Enqueue(ackReq("ZZZZZ")) {
		t.Fatal("ack was queued past the capacity")
	}
	if _, dropped := q.Stats(); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

// A burst cannot outrun the send interval: the worker drains one ack per
// interval, and the queue bounds how much can wait.
func TestAckQueueSpacesSends(t *testing.T) {
	a := &app{}
	q := newAPRSAckQueue(a)
	q.interval = 20 * time.Millisecond
	q.now = time.Now
	q.Start()
	defer q.Stop()

	const burst = 5
	for i := 0; i < burst; i++ {
		req := ackReq(fmt.Sprintf("%05d", i))
		if !q.Enqueue(req) {
			t.Fatalf("ack %d was dropped with an empty queue", i)
		}
	}

	// Wait long enough for roughly two sends, not the whole burst.
	time.Sleep(50 * time.Millisecond)
	sent, _ := q.Stats()
	if sent > 3 {
		t.Fatalf("worker sent %d acks in 50ms at a 20ms interval", sent)
	}
}

func TestAckQueueStopIsPrompt(t *testing.T) {
	q := newAPRSAckQueue(&app{})
	q.interval = time.Hour
	q.Start()

	done := make(chan struct{})
	go func() {
		q.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return while the worker was idle")
	}
}

func TestAckQueueStopDuringSpacing(t *testing.T) {
	q := newAPRSAckQueue(&app{})
	q.interval = time.Hour
	q.Start()
	q.Enqueue(ackReq("ABCDE"))

	// Give the worker time to take the item and enter the spacing wait.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		q.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not interrupt the send interval")
	}
}

func TestAckQueueSummaryReportsCounters(t *testing.T) {
	q := newAPRSAckQueue(&app{})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }
	q.Enqueue(ackReq("AAAAA"))
	q.Enqueue(ackReq("AAAAA")) // duplicate
	summary := q.queueSummary()
	if summary == "" {
		t.Fatal("empty summary")
	}
	if sent, dropped := q.Stats(); sent != 0 || dropped != 1 {
		t.Fatalf("sent=%d dropped=%d", sent, dropped)
	}
}
