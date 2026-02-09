package app

import (
	"context"
	"math/rand"
	"time"
)

// Pacer provides a simple global start-rate limiter for download jobs.
// It emits tokens at a randomized interval in the range [minDelay, maxDelay].
// A token must be acquired before starting a download to avoid request bursts.
type Pacer struct {
	ch   chan struct{}
	stop chan struct{}
	rnd  *rand.Rand
	min  time.Duration
	max  time.Duration
}

func NewPacer(minDelay, maxDelay time.Duration) *Pacer {
	if minDelay <= 0 {
		minDelay = 150 * time.Millisecond
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	return &Pacer{
		ch:   make(chan struct{}, 1),
		stop: make(chan struct{}),
		rnd:  rand.New(rand.NewSource(time.Now().UnixNano())),
		min:  minDelay,
		max:  maxDelay,
	}
}

func (p *Pacer) Start() {
	go func() {
		for {
			select {
			case <-p.stop:
				return
			default:
			}

			d := p.nextDelay()
			t := time.NewTimer(d)
			select {
			case <-t.C:
				// Emit at most one token to avoid unbounded buffering.
				select {
				case p.ch <- struct{}{}:
				default:
				}
			case <-p.stop:
				if !t.Stop() {
					<-t.C
				}
				return
			}
		}
	}()
}

func (p *Pacer) Stop() {
	select {
	case <-p.stop:
		return
	default:
		close(p.stop)
	}
}

func (p *Pacer) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.stop:
		return context.Canceled
	case <-p.ch:
		return nil
	}
}

func (p *Pacer) nextDelay() time.Duration {
	if p.max == p.min {
		return p.min
	}
	delta := p.max - p.min
	// Randomize the delay to avoid a fixed request cadence.
	n := p.rnd.Int63n(int64(delta) + 1)
	return p.min + time.Duration(n)
}
