package ddtxn

import (
	"container/heap"
	"ddtxn/dlog"
	"flag"
	"log"
	"sync/atomic"
	"time"
)

const (
	BUMP_EPOCH_MS = 80
	EPOCH_INCR    = 1 << 32
	TXID_MASK     = 0x00000000ffffffff
	CLEAR_TID     = 0xffffffff00000000
)

var PhaseLength = flag.Int("phase", 80, "Phase length in milliseconds, default 80")

type Coordinator struct {
	n        int
	Workers  []*Worker
	epochTID uint64 // Global TID, atomically incremented and read

	// Notify workers
	wepoch []chan TID
	wsafe  []chan TID
	wgo    []chan TID
	wdone  []chan TID

	Coordinate            bool
	PotentialPhaseChanges int64
	Done                  chan chan bool
	Accelerate            chan bool
	trigger               int32
}

func NewCoordinator(n int, s *Store) *Coordinator {
	c := &Coordinator{
		n:                     n,
		Workers:               make([]*Worker, n),
		epochTID:              EPOCH_INCR,
		wepoch:                make([]chan TID, n),
		wsafe:                 make([]chan TID, n),
		wgo:                   make([]chan TID, n),
		wdone:                 make([]chan TID, n),
		Done:                  make(chan chan bool),
		Accelerate:            make(chan bool),
		Coordinate:            false,
		PotentialPhaseChanges: 0,
	}
	for i := 0; i < n; i++ {
		c.wepoch[i] = make(chan TID)
		c.wsafe[i] = make(chan TID)
		c.wgo[i] = make(chan TID)
		c.wdone[i] = make(chan TID)
		c.Workers[i] = NewWorker(i, s, c)
	}
	dlog.Printf("[coordinator] %v workers\n", n)
	go c.Process()
	return c
}

var NextEpoch int64

func (c *Coordinator) NextGlobalTID() TID {
	atomic.AddInt64(&NextEpoch, 1)
	x := atomic.AddUint64(&c.epochTID, EPOCH_INCR)
	return TID(x)
}

func (c *Coordinator) GetEpoch() TID {
	x := atomic.LoadUint64(&c.epochTID)
	return TID(x)
}

var RMoved int64
var WMoved int64
var Time_in_IE time.Duration
var Time_in_IE1 time.Duration

func (c *Coordinator) Stats() (map[Key]bool, map[Key]bool) {
	if c.PotentialPhaseChanges%(10) != 0 {
		return nil, nil
	}
	start2 := time.Now()
	s := c.Workers[0].store
	for i := 0; i < c.n; i++ {
		w := c.Workers[i]
		c.Workers[i].Lock()
		s.cand.Merge(w.local_store.candidates)
	}
	potential_dd_keys := make(map[Key]bool)
	to_remove := make(map[Key]bool)
	xx := len(*s.cand.h)
	for i := 0; i < xx; i++ {
		o := heap.Pop(s.cand.h).(*OneStat)
		x, y := UndoCKey(o.k)
		dlog.Printf("%v Considering key %v %v; ratio %v\n", i, x, y, o.ratio())
		br, _ := s.getKey(o.k)
		if !br.dd && o.ratio() > *WRRatio && o.writes > 3 {
			potential_dd_keys[o.k] = true
			dlog.Printf("Moving %v %v to split r:%v w:%v c:%v ratio:%v\n", x, y, o.reads, o.writes, o.conflicts, o.ratio())
		} else if br.dd {
			dlog.Printf("No need to Move %v %v to split; already dd\n", x, y)
		} else {
			dlog.Printf("Not enough writes yet: %v %v %v; ratio %v\n", x, y, o.writes, o.ratio())
		}
	}
	// Check to see if we need to remove anything from dd
	for k, v := range s.dd {
		if !v {
			continue
		}
		o, ok := s.cand.m[k]
		if !ok {
			x, y := UndoCKey(k)
			dlog.Printf("Key %v %v was split but now is not in store candidates\n", x, y)
			continue
		}
		if o.ratio() < (*WRRatio)/2 {
			to_remove[k] = true
			x, y := UndoCKey(o.k)
			dlog.Printf("Moved %v %v from split ratio %v\n", x, y, o.ratio())
		}
	}
	if len(s.dd) == 0 && len(potential_dd_keys) == 0 {
		s.any_dd = false
		c.Coordinate = false
	} else {
		c.Coordinate = true
		s.any_dd = true
	}
	// Reset global store
	x := make([]*OneStat, 0)
	sh := StatsHeap(x)
	s.cand = &Candidates{make(map[Key]*OneStat), &sh}

	for i := 0; i < c.n; i++ {
		// Reset local stores and unlock
		w := c.Workers[i]
		x := make([]*OneStat, 0)
		sh := StatsHeap(x)
		w.local_store.candidates = &Candidates{make(map[Key]*OneStat), &sh}
		w.Unlock()
	}
	end := time.Since(start2)
	Time_in_IE1 += end
	return potential_dd_keys, to_remove
}

func (c *Coordinator) IncrementEpoch(force bool) {
	c.PotentialPhaseChanges++
	move_dd, remove_dd := c.Stats()
	if !c.Coordinate && !force {
		return
	}
	dlog.Printf("Incrementing epoch %v\n", c.epochTID)
	start := time.Now()
	next_epoch := c.NextGlobalTID()

	// Wait for everyone to merge the previous epoch
	for i := 0; i < c.n; i++ {
		e := <-c.wepoch[i]
		if e != next_epoch {
			log.Fatalf("Out of alignment in epoch ack; I expected %v, got %v\n", next_epoch, e)
		}
	}

	// All merged.  The previous epoch is now safe; tell everyone to
	// do their reads.
	atomic.StoreInt32(&c.trigger, 0)
	for i := 0; i < c.n; i++ {
		c.wsafe[i] <- next_epoch
	}
	for i := 0; i < c.n; i++ {
		e := <-c.wdone[i]
		if e != next_epoch {
			log.Fatalf("Out of alignment in done; I expected %v, got %v\n", next_epoch, e)
		}

	}

	s := c.Workers[0].store
	// Merge dd
	if move_dd != nil {
		for k, _ := range move_dd {
			br, _ := s.getKey(k)
			br.dd = true
			s.dd[k] = true
			WMoved += 1
		}
	}
	if remove_dd != nil {
		for k, _ := range remove_dd {
			br, _ := s.getKey(k)
			br.dd = false
			s.dd[k] = false
			RMoved += 1
		}
	}

	for i := 0; i < c.n; i++ {
		c.wgo[i] <- next_epoch
	}
	end := time.Since(start)
	Time_in_IE += end
}

func (c *Coordinator) Finish() {
	dlog.Printf("Coordinator finishing\n")
	x := make(chan bool)
	c.Done <- x
	<-x
}

var Nfast int64

func (c *Coordinator) Process() {
	tm := time.NewTicker(time.Duration(*PhaseLength) * time.Millisecond).C

	// More frequently, check if the workers are demanding a phase
	// change due to long stashed queue lengths.
	check_trigger := time.NewTicker(time.Duration(*PhaseLength) * time.Microsecond * 10).C

	for {
		select {
		case x := <-c.Done:
			if *SysType == DOPPEL {
				c.IncrementEpoch(true)
			}
			for i := 0; i < c.n; i++ {
				c.Workers[i].done <- true
				dlog.Printf("Worker %v finished\n", i)
			}
			x <- true
			return
		case <-tm:
			if *SysType == DOPPEL {
				c.IncrementEpoch(false)
			}
		case <-check_trigger:
			if *SysType == DOPPEL {
				x := atomic.LoadInt32(&c.trigger)
				if x == int32(c.n) {
					Nfast++
					atomic.StoreInt32(&c.trigger, 0)
					c.IncrementEpoch(true)
				}
			}
		case <-c.Accelerate:
			if *SysType == DOPPEL {
				dlog.Printf("Accelerating\n")
				c.IncrementEpoch(true)
			}
		}
	}
}
