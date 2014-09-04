package main

import (
	"container/heap"
	"ddtxn"
	"ddtxn/apps"
	"ddtxn/dlog"
	"ddtxn/prof"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"time"
)

var nprocs = flag.Int("nprocs", 2, "GOMAXPROCS default 2")
var nsec = flag.Int("nsec", 2, "Time to run in seconds")
var clientGoRoutines = flag.Int("ngo", 0, "Number of goroutines/workers generating client requests.")
var nworkers = flag.Int("nw", 0, "Number of workers")
var doValidate = flag.Bool("validate", false, "Validate")

var contention = flag.Float64("contention", 1000, "Amount of contention, higher is more")
var nbidders = flag.Int("nb", 1000000, "Bidders in store, default is 1M")
var readrate = flag.Int("rr", 0, "Read rate %.  Rest are buys")
var notcontended_readrate = flag.Float64("ncrr", .8, "Uncontended read rate %.  Default to .8")
var dataFile = flag.String("out", "buy-data.out", "Filename for output")
var atomicIncr = flag.Bool("atomic", false, "NOT USED")

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(*nprocs)

	if *clientGoRoutines == 0 {
		*clientGoRoutines = *nprocs
	}
	if *nworkers == 0 {
		*nworkers = *nprocs
	}

	if *doValidate {
		if !*ddtxn.Allocate {
			log.Fatalf("Cannot correctly validate without waiting for results; add -allocate\n")
		}
	}
	var nproducts int
	if *contention > 0 {
		nproducts = *nbidders / int(*contention)
	} else {
		nproducts = *nbidders
	}
	s := ddtxn.NewStore()
	buy_app := &apps.Buy{}
	buy_app.Init(nproducts, *nbidders, *nworkers, *readrate, *clientGoRoutines, *notcontended_readrate)
	dlog.Printf("Starting to initialize buy\n")
	buy_app.Populate(s, nil)

	coord := ddtxn.NewCoordinator(*nworkers, s)

	if *ddtxn.CountKeys {
		for i := 0; i < *nworkers; i++ {
			w := coord.Workers[i]
			w.NKeyAccesses = make([]int64, *nbidders)
		}
	}

	dlog.Printf("Done initializing buy\n")

	p := prof.StartProfile()
	start := time.Now()

	var wg sync.WaitGroup

	gave_up := make([]int64, *clientGoRoutines)
	for i := 0; i < *clientGoRoutines; i++ {
		wg.Add(1)
		go func(n int) {
			exp := ddtxn.MakeExp(30)
			retries := make(ddtxn.RetryHeap, 0)
			heap.Init(&retries)
			end_time := time.Now().Add(time.Duration(*nsec) * time.Second)
			var local_seed uint32 = uint32(rand.Intn(10000000))
			var sp uint32 = uint32(*nbidders / *clientGoRoutines)
			w := coord.Workers[n%(*nworkers)]
			var tm time.Time
			for {
				tm = time.Now()
				if !end_time.After(tm) {
					break
				}
				var t ddtxn.Query
				if len(retries) > 0 && retries[0].TS.Before(tm) {
					t = heap.Pop(&retries).(ddtxn.Query)
				} else {
					buy_app.MakeOne(w.ID, &local_seed, sp, &t)
				}
				if *doValidate {
					t.W = make(chan struct {
						R *ddtxn.Result
						E error
					}, 1)
				}
				if *ddtxn.Latency {
					t.S = time.Now()
				}
				committed := false
				_, err := w.One(t)
				if err == ddtxn.ESTASH {
					if *doValidate {
						x := <-t.W
						err = x.E
						if err == ddtxn.EABORT {
							log.Fatalf("Should be run until commitment!\n")
						}
					}
					committed = true // The worker stash code will retry
				} else if err == ddtxn.EABORT {
					committed = false
				} else {
					committed = true
				}
				t.I++
				if !committed {
					t.TS = tm.Add(time.Duration(ddtxn.RandN(&local_seed, exp.Exp(t.I))) * time.Microsecond)
					if t.TS.Before(end_time) {
						heap.Push(&retries, t)
					} else {
						gave_up[n]++
					}
				}
				if committed && *doValidate {
					buy_app.Add(t)
				}
			}
			wg.Done()
			if len(retries) > 0 {
				dlog.Printf("[%v] Length of retry queue on exit: %v\n", n, len(retries))
			}
			gave_up[n] = gave_up[n] + int64(len(retries))
		}(i)
	}
	wg.Wait()
	coord.Finish()
	end := time.Since(start)
	p.Stop()

	stats := make([]int64, ddtxn.LAST_STAT)
	nitr, nwait, _ := ddtxn.CollectCounts(coord, stats)

	if *doValidate {
		buy_app.Validate(s, int(nitr))
	}

	for i := 1; i < *clientGoRoutines; i++ {
		gave_up[0] = gave_up[0] + gave_up[i]
	}

	// nitr + NABORTS + ENOKEY is how many requests were issued.  A
	// stashed transaction eventually executes and contributes to
	// nitr.
	out := fmt.Sprintf(" nworkers: %v, nwmoved: %v, nrmoved: %v, sys: %v, total/sec: %v, abortrate: %.2f, stashrate: %.2f, rr: %v, ncrr: %v, nbids: %v, nproducts: %v, contention: %v, done: %v, actual time: %v, nreads: %v, nbuys: %v, epoch changes: %v, throughput ns/txn: %v, naborts: %v, coord time: %v, coord stats time: %v, total worker time transitioning: %v, nstashed: %v, rlock: %v, wrratio: %v, nsamples: %v, getkeys: %v, ddwrites: %v, nolock: %v, failv: %v, stashdone: %v, nfast: %v, gaveup: %v, potential: %v  ", *nworkers, ddtxn.WMoved, ddtxn.RMoved, *ddtxn.SysType, float64(nitr)/end.Seconds(), 100*float64(stats[ddtxn.NABORTS])/float64(nitr+stats[ddtxn.NABORTS]), 100*float64(stats[ddtxn.NSTASHED])/float64(nitr+stats[ddtxn.NABORTS]), *readrate, *notcontended_readrate*float64(*readrate), *nbidders, nproducts, *contention, nitr, end, stats[ddtxn.D_READ_TWO], stats[ddtxn.D_BUY], ddtxn.NextEpoch, end.Nanoseconds()/nitr, stats[ddtxn.NABORTS], ddtxn.Time_in_IE, ddtxn.Time_in_IE1, nwait, stats[ddtxn.NSTASHED], *ddtxn.UseRLocks, *ddtxn.WRRatio, stats[ddtxn.NSAMPLES], stats[ddtxn.NGETKEYCALLS], stats[ddtxn.NDDWRITES], stats[ddtxn.NO_LOCK], stats[ddtxn.NFAIL_VERIFY], stats[ddtxn.NDIDSTASHED], ddtxn.Nfast, gave_up[0], coord.PotentialPhaseChanges)
	fmt.Printf(out)
	fmt.Printf("\n")
	f, err := os.OpenFile(*dataFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	ddtxn.PrintStats(out, stats, f, coord, s, *nbidders)

	x, y := coord.Latency()
	f.WriteString(x)
	f.WriteString(y)
	f.WriteString("\n")
}
