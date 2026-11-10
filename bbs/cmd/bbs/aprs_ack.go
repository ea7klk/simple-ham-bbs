package main

import (
	"fmt"
	"sync"
	"time"
)

// Bounded APRS acknowledgement sending (M2).
//
// The receiver used to answer every inbound message carrying a message ID by
// dialling APRS-IS synchronously, logging in, writing one packet, and closing.
// APRS-IS is an open network and the receiver subscribes with `filter t/m`, so
// any station could turn one inbound packet into one outbound TCP connection —
// a dial storm that also stalled the receive loop.
//
// Acknowledgements now go through a bounded queue drained by a single worker
// that spaces sends at least aprsAckMinInterval apart. Inbound traffic can still
// arrive at wire speed, but it cannot allocate outbound sockets faster than the
// worker releases them: the queue fills, overflow is dropped and logged, and the
// receive loop never blocks on a dial.

const (
	// aprsAckQueueSize bounds how many acks may wait to be sent.
	aprsAckQueueSize = 64
	// aprsAckMinInterval is the minimum spacing between outbound ack sends,
	// which caps outbound dials at six per minute.
	aprsAckMinInterval = 10 * time.Second
	// aprsAckDedupeWindow suppresses repeat acks for the same message ID.
	aprsAckDedupeWindow = 10 * time.Minute
)

type aprsAckRequest struct {
	User      string
	From      string
	To        string
	MessageID string
}

func (r aprsAckRequest) key() string {
	return r.From + "|" + r.To + "|" + r.MessageID
}

type aprsAckQueue struct {
	app      *app
	ch       chan aprsAckRequest
	quit     chan struct{}
	wg       sync.WaitGroup
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	seen    map[string]time.Time
	sent    int
	dropped int
}

func newAPRSAckQueue(a *app) *aprsAckQueue {
	return &aprsAckQueue{
		app:      a,
		ch:       make(chan aprsAckRequest, aprsAckQueueSize),
		quit:     make(chan struct{}),
		interval: aprsAckMinInterval,
		now:      time.Now,
		seen:     map[string]time.Time{},
	}
}

// Start launches the single sender goroutine.
func (q *aprsAckQueue) Start() {
	q.wg.Add(1)
	go q.run()
}

// Stop asks the worker to exit and waits for the current send to finish.
func (q *aprsAckQueue) Stop() {
	close(q.quit)
	q.wg.Wait()
}

// Enqueue queues one ack without blocking the receive loop. It returns false
// when the request is a duplicate within the dedupe window or the queue is full,
// in which case the caller logs the drop.
func (q *aprsAckQueue) Enqueue(req aprsAckRequest) bool {
	if !q.markSeen(req) {
		q.countDrop()
		return false
	}
	select {
	case q.ch <- req:
		return true
	default:
		q.countDrop()
		return false
	}
}

func (q *aprsAckQueue) markSeen(req aprsAckRequest) bool {
	now := q.now()
	key := req.key()
	q.mu.Lock()
	defer q.mu.Unlock()
	for seenKey, at := range q.seen {
		if now.Sub(at) > aprsAckDedupeWindow {
			delete(q.seen, seenKey)
		}
	}
	if _, ok := q.seen[key]; ok {
		return false
	}
	q.seen[key] = now
	return true
}

func (q *aprsAckQueue) countDrop() {
	q.mu.Lock()
	q.dropped++
	q.mu.Unlock()
}

// Stats returns how many acks were sent and dropped, for logging.
func (q *aprsAckQueue) Stats() (sent, dropped int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sent, q.dropped
}

func (q *aprsAckQueue) run() {
	defer q.wg.Done()
	for {
		select {
		case <-q.quit:
			return
		case req := <-q.ch:
			select {
			case <-q.quit:
				return
			default:
			}
			q.send(req)
			// Space the next send, but stay responsive to Stop.
			select {
			case <-q.quit:
				return
			case <-time.After(q.interval):
			}
		}
	}
}

func (q *aprsAckQueue) send(req aprsAckRequest) {
	q.mu.Lock()
	q.sent++
	q.mu.Unlock()

	if err := q.app.sendAPRSAck(req.To, req.From, req.MessageID); err != nil {
		q.app.logAPRSReceiver("APRS ack send error user=%s from=%s to=%s id=%s: %v", req.User, req.To, req.From, req.MessageID, err)
		q.app.logBBSAction(req.User, "aprs_ack_send_failed", "from=%q to=%q id=%q error=%q", req.To, req.From, req.MessageID, err.Error())
		return
	}
	q.app.logAPRSReceiver("APRS ack sent user=%s from=%s to=%s id=%s", req.User, req.To, req.From, req.MessageID)
	q.app.logBBSAction(req.User, "aprs_ack_sent", "from=%q to=%q id=%q", req.To, req.From, req.MessageID)
}

// queueSummary is the log line emitted when acks were dropped or sent, so the
// operator can see that inbound traffic outran the outbound budget.
func (q *aprsAckQueue) queueSummary() string {
	sent, dropped := q.Stats()
	return fmt.Sprintf("outbound acks sent=%d dropped=%d queue=%d interval=%s", sent, dropped, aprsAckQueueSize, q.interval)
}
