package apps

import (
	"ddtxn"
	"ddtxn/dlog"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

var Skewed = flag.Bool("skew", false, "Rubis-C (skewed workload) or not, default Rubis-B")
var Cont = flag.Float64("ccont", 1.04, "\"theta\" parameter for zipf")
var Oldmode = flag.Bool("oldskew", false, "specify Rubis txn probs via \"skew\"")

type Rubis struct {
	sync.Mutex
	padding    [128]byte
	nproducts  int
	nbidders   int
	portion_sz int
	nworkers   int
	ngo        int
	maxes      []int32
	num_bids   []int32
	ratings    map[uint64]int32
	users      []uint64
	products   []uint64
	sp         uint32
	rates      []float64
	padding1   [128]byte
	pidIdx     map[uint64]int
	zip        []*ddtxn.Zipf
}

func (b *Rubis) Init(np, nb, nw, ngo int) {
	b.nproducts = np
	b.nbidders = nb
	b.nworkers = nw
	b.ngo = ngo
	b.maxes = make([]int32, np)
	b.num_bids = make([]int32, np)
	b.pidIdx = make(map[uint64]int, np)
	b.ratings = make(map[uint64]int32)
	b.sp = uint32(nb / nw)
	b.rates = ddtxn.GetTxns(*Skewed, *Oldmode)
	b.users = make([]uint64, nb)
	if ddtxn.NUM_ITEMS > np {
		b.products = make([]uint64, ddtxn.NUM_ITEMS)
	} else {
		b.products = make([]uint64, np)
	}

	b.zip = make([]*ddtxn.Zipf, nw)
}

func (b *Rubis) Populate(s *ddtxn.Store, c *ddtxn.Coordinator) {
	tmp := *ddtxn.Allocate
	tmp2 := *dlog.Debug
	*ddtxn.Allocate = true
	*dlog.Debug = false
	for wi := 0; wi < b.nworkers; wi++ {
		w := c.Workers[wi]
		ex := w.E
		for i := b.sp * uint32(wi); i < b.sp*(uint32(wi+1)); i++ {
			q := ddtxn.Query{
				U1: uint64(rand.Intn(ddtxn.NUM_REGIONS)),
				U2: 0,
			}
			r, err := ddtxn.RegisterUserTxn(q, ex)
			if err != nil {
				log.Fatalf("Could not create user %v; err:%v\n", i, err)
			}
			b.users[i] = r.V.(uint64)
			if b.users[i] == 0 {
				fmt.Printf("Created user 0; index; %v\n", i)
			}
			ex.Reset()
		}

		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		b.zip[wi] = ddtxn.NewZipf(r, *Cont, 1, uint64(len(b.products)))
	}
	chunk := ddtxn.NUM_ITEMS / b.nworkers
	for wi := 0; wi < b.nworkers; wi++ {
		w := c.Workers[wi]
		ex := w.E
		nx := rand.Intn(b.nbidders)
		for i := chunk * wi; i < chunk*(wi+1); i++ {
			q := ddtxn.Query{
				T:  ddtxn.TID(i),
				S1: "xxx",
				S2: "lovely",
				U1: b.users[nx],
				U2: 100,
				U3: 100,
				U4: 1000,
				U5: 1000,
				U6: 1,
				U7: uint64(rand.Intn(ddtxn.NUM_CATEGORIES)),
			}
			r, err := ddtxn.NewItemTxn(q, ex)
			if err != nil {
				fmt.Printf("%v Could not create item index %v error: %v user_id: %v user index: %v nb: %v\n", wi, i, err, q.U1, nx, b.nbidders)
				continue
			}
			v := r.V.(uint64)
			b.products[i] = v
			b.pidIdx[v] = i
			k := ddtxn.BidsPerItemKey(v)
			w.Store().CreateKey(k, nil, ddtxn.LIST)
			ex.Reset()
		}
	}
	*ddtxn.Allocate = tmp
	*dlog.Debug = tmp2
}

func (b *Rubis) MakeOne(w int, local_seed *uint32, txn *ddtxn.Query) {
	x := float64(ddtxn.RandN(local_seed, 100))
	if x < b.rates[0] {
		txn.TXN = ddtxn.RUBIS_BID
		bidder := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		//product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		product := b.zip[w].Uint64()
		txn.U1 = uint64(bidder)
		txn.U2 = uint64(product)
		txn.A = int32(ddtxn.RandN(local_seed, 10))
	} else if x < b.rates[1] {
		txn.TXN = ddtxn.RUBIS_VIEWBIDHIST
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		txn.U1 = uint64(product)
	} else if x < b.rates[2] {
		txn.TXN = ddtxn.RUBIS_BUYNOW
		bidder := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		txn.U1 = uint64(bidder)
		txn.U2 = uint64(product)
		txn.A = int32(ddtxn.RandN(local_seed, 10))
	} else if x < b.rates[3] {
		txn.TXN = ddtxn.RUBIS_COMMENT
		u1 := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		u2 := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		txn.U1 = uint64(u1)
		txn.U2 = uint64(u2)
		txn.U3 = uint64(product)
		txn.S1 = "xxxx"
		txn.U4 = 1
	} else if x < b.rates[4] {
		txn.TXN = ddtxn.RUBIS_NEWITEM
		bidder := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		amt := uint64(ddtxn.RandN(local_seed, 10))
		txn.U1 = uint64(bidder)
		txn.S1 = "yyyy"
		txn.S2 = "zzzz"
		txn.U2 = amt
		txn.U3 = amt
		txn.U4 = amt
		txn.U5 = 1
		txn.U6 = 1
		txn.U7 = uint64(ddtxn.RandN(local_seed, uint32(ddtxn.NUM_CATEGORIES)))
	} else if x < b.rates[5] {
		txn.TXN = ddtxn.RUBIS_PUTBID
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		txn.U1 = uint64(product)
	} else if x < b.rates[6] {
		txn.TXN = ddtxn.RUBIS_PUTCOMMENT
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		bidder := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		txn.U1 = uint64(bidder)
		txn.U2 = uint64(product)
	} else if x < b.rates[7] {
		txn.TXN = ddtxn.RUBIS_REGISTER
		txn.U1 = uint64(ddtxn.RandN(local_seed, uint32(ddtxn.NUM_REGIONS)))
		txn.U2 = uint64(ddtxn.RandN(local_seed, 1000000000))
	} else if x < b.rates[8] {
		txn.TXN = ddtxn.RUBIS_SEARCHCAT
		txn.U1 = uint64(ddtxn.RandN(local_seed, uint32(ddtxn.NUM_CATEGORIES)))
		txn.U2 = 5
	} else if x < b.rates[9] {
		txn.TXN = ddtxn.RUBIS_SEARCHREG
		txn.U1 = uint64(ddtxn.RandN(local_seed, uint32(ddtxn.NUM_REGIONS)))
		txn.U2 = uint64(ddtxn.RandN(local_seed, uint32(ddtxn.NUM_CATEGORIES)))
		txn.U3 = 5
	} else if x < b.rates[10] {
		txn.TXN = ddtxn.RUBIS_VIEW
		product := b.products[ddtxn.RandN(local_seed, uint32(b.nproducts))]
		txn.U1 = uint64(product)
	} else if x < b.rates[11] {
		txn.TXN = ddtxn.RUBIS_VIEWUSER
		bidder := b.users[int(ddtxn.RandN(local_seed, b.sp))+w*int(b.sp)]
		txn.U1 = uint64(bidder)
	} else {
		log.Fatalf("No such transaction\n")
	}
}

func (b *Rubis) Add(t ddtxn.Query) {
	if t.TXN == ddtxn.RUBIS_BID {
		x := b.pidIdx[t.U2]
		atomic.AddInt32(&b.num_bids[x], 1)
		for t.A > b.maxes[x] {
			v := atomic.LoadInt32(&b.maxes[x])
			done := atomic.CompareAndSwapInt32(&b.maxes[x], v, t.A)
			if done {
				break
			}
		}
	} else if t.TXN == ddtxn.RUBIS_COMMENT {
		b.Lock()
		b.ratings[t.U1] += 1
		b.Unlock()
	}
}

func (b *Rubis) Validate(s *ddtxn.Store, nitr int) bool {
	good := true
	zero_cnt := 0
	for k, rat := range b.ratings {
		key := ddtxn.RatingKey(k)
		v, err := s.Get(key)
		if err != nil {
			fmt.Printf("Validating key %v failed; store: doesn't have rating for user %v: %v\n", key, k, err)
			good = false
			continue
		}
		r := v.Value().(int32)
		if r != rat {
			fmt.Printf("Validating key %v failed; store: has different rating for user %v (%v vs. %v): %v\n", key, k, rat, r, err)
			good = false
			continue
		}
	}
	for i := 0; i < b.nproducts; i++ {
		j := b.products[i]
		var x int32
		k := ddtxn.MaxBidKey(j)
		v, err := s.Get(k)
		if err != nil {
			if b.maxes[i] != 0 {
				fmt.Printf("Validating key %v failed; store: none should have: %v\n", k, b.maxes[i])
				good = false
			}
			continue
		}
		x = v.Value().(int32)
		if x != b.maxes[i] {
			fmt.Printf("Validating key %v failed; store: %v should have: %v\n", k, x, b.maxes[i])
			good = false
		}
		if x == 0 {
			dlog.Printf("Saying x is zero %v %v\n", x, zero_cnt)
			zero_cnt++
		}
		k = ddtxn.NumBidsKey(j)
		v, err = s.Get(k)
		if err != nil {
			if b.maxes[i] != 0 {
				fmt.Printf("Validating key %v failed for max bid; store: none should have: %v\n", k, b.num_bids[i])
				good = false
			}
			continue
		}
		x = v.Value().(int32)
		if x != b.num_bids[i] {
			fmt.Printf("Validating key %v failed for number of bids; store: %v should have: %v\n", k, x, b.num_bids[i])
			good = false
		}
		if x == 0 {
			dlog.Printf("Saying x is zero %v %v\n", x, zero_cnt)
			zero_cnt++
		}

	}
	if zero_cnt == 2*b.nproducts && nitr > 10 {
		fmt.Printf("Bad: all zeroes!\n")
		dlog.Printf("Bad: all zeroes!\n")
		good = false
	}
	if good {
		dlog.Printf("Validate succeeded\n")
	}
	return good
}
