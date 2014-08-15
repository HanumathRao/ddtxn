package ddtxn

import (
	"ddtxn/dlog"
	"flag"
	"log"
	"runtime/debug"
)

var SampleRate = flag.Int64("sr", 10000, "Sample every sr nanoseconds\n")

// Phases
const (
	SPLIT = iota
	MERGE
	JOIN
)

type Write struct {
	key    Key
	br     *BRecord
	v      Value
	op     KeyType
	create bool
	locked bool
	vint32 int32

	// TODO: Handle writing more than once to a list per txn
	ve Entry
}

type ETransaction interface {
	Reset()
	Read(k Key) (*BRecord, error)
	WriteInt32(k Key, a int32, op KeyType) error
	WriteList(k Key, l Entry, kt KeyType)
	Write(k Key, v Value, kt KeyType)
	Abort() TID
	Commit() TID
	SetPhase(int)
	Store() *Store // For AtomicIncr benchmark transaction only
}

// Not threadsafe.  Tracks execution of transaction.
type OTransaction struct {
	padding0 [128]byte
	read     []*BRecord
	lasts    []uint64
	w        *Worker
	s        *Store
	ls       *LocalStore
	phase    int
	writes   []Write
	t        int64 // Used just as a rough count
	count    bool
	sr_rate  int64
	padding  [128]byte
}

func StartOTransaction(w *Worker) *OTransaction {
	tx := &OTransaction{
		read:   make([]*BRecord, 0, 100),
		lasts:  make([]uint64, 0, 100),
		writes: make([]Write, 0, 100),
		w:      w,
		s:      w.store,
		ls:     w.local_store,
	}
	return tx
}

func (tx *OTransaction) Reset() {
	tx.lasts = tx.lasts[:0]
	tx.read = tx.read[:0]
	tx.writes = tx.writes[:0]
	tx.count = (*SysType == DOPPEL && tx.sr_rate == 0)
	if tx.count {
		tx.w.Nstats[NSAMPLES]++
		tx.sr_rate = *SampleRate
	} else {
		tx.sr_rate--
	}
	tx.t++
}

func (tx *OTransaction) Read(k Key) (*BRecord, error) {
	if len(tx.read) > 0 {
		// TODO: If I wrote the key, return that value instead
	}
	br, err := tx.s.getKey(k)
	if br != nil && *SysType == DOPPEL {
		if tx.phase == SPLIT {
			if br.dd {
				if tx.count {
					tx.ls.candidates.Stash(k)
				}
				return nil, ESTASH
			}
		}
	}
	if *CountKeys {
		p, r := UndoCKey(k)
		if r == 112 {
			tx.w.NKeyAccesses[p]++
		}
	}
	if err != nil {
		return nil, err
	}
	ok, last := br.IsUnlocked()
	// if locked and not by me, abort
	// else note the last timestamp, save it, return value
	if !ok {
		if tx.count {
			tx.ls.candidates.Conflict(k)
		}
		tx.Abort()
		return nil, EABORT
	}
	n := len(tx.read)
	tx.read = tx.read[0 : n+1]
	tx.read[n] = br
	tx.lasts = tx.lasts[0 : n+1]
	tx.lasts[n] = last
	return br, nil
}

func (tx *OTransaction) add(k Key, v Value, op KeyType, create bool) {
	if len(tx.writes) == cap(tx.writes) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.writes)
	tx.writes = tx.writes[0 : n+1]
	tx.writes[n].key = k
	tx.writes[n].br = nil
	tx.writes[n].v = v
	tx.writes[n].op = op
	tx.writes[n].create = create
	tx.writes[n].locked = false
}

func (tx *OTransaction) addInt32(k Key, v int32, op KeyType, create bool) {
	if len(tx.writes) == cap(tx.writes) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.writes)
	tx.writes = tx.writes[0 : n+1]
	tx.writes[n].key = k
	tx.writes[n].br = nil
	tx.writes[n].vint32 = v
	tx.writes[n].op = op
	tx.writes[n].create = create
	tx.writes[n].locked = false
}

func (tx *OTransaction) addList(k Key, v Entry, op KeyType, create bool) {
	if len(tx.writes) == cap(tx.writes) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.writes)
	tx.writes = tx.writes[0 : n+1]
	tx.writes[n].key = k
	tx.writes[n].br = nil
	tx.writes[n].ve = v
	tx.writes[n].op = op
	tx.writes[n].create = create
	tx.writes[n].locked = false
}

func (tx *OTransaction) WriteInt32(k Key, a int32, op KeyType) error {
	// RTM requests that during the normal phase, Doppel operates
	// just like OCC, for ease of exposition.  That means it would
	// have to put the key into the read set and potentially abort
	// accordingly.  Doing so here, but not using the value until
	// commit time.
	br, err := tx.s.getKey(k)
	if err != nil {
		return err
	}
	if *SysType == DOPPEL && tx.phase == SPLIT && br.dd == true {
		// Do not need to read-validate
	} else {
		ok, last := br.IsUnlocked()
		// if locked and not by me, abort
		// else note the last timestamp and save it
		if !ok {
			tx.Abort()
			return EABORT
		}
		n := len(tx.read)
		tx.read = tx.read[0 : n+1]
		tx.read[n] = br
		tx.lasts = tx.lasts[0 : n+1]
		tx.lasts[n] = last
	}
	n := len(tx.writes)
	tx.writes = tx.writes[0 : n+1]
	tx.writes[n].key = k
	tx.writes[n].br = br
	tx.writes[n].vint32 = a
	tx.writes[n].op = op
	tx.writes[n].create = false
	tx.writes[n].locked = false
	return nil
}

func (tx *OTransaction) Write(k Key, v Value, kt KeyType) {
	if kt == SUM || kt == MAX {
		tx.addInt32(k, v.(int32), kt, true)
		return
	}
	tx.add(k, v, kt, true)
}

func (tx *OTransaction) WriteList(k Key, l Entry, kt KeyType) {
	if kt != LIST {
		log.Fatalf("Not a list\n")
	}
	tx.addList(k, l, kt, true)
}

func (tx *OTransaction) SetPhase(p int) {
	tx.phase = p
}

func (tx *OTransaction) Store() *Store {
	return tx.s
}

func (tx *OTransaction) Abort() TID {
	for i, _ := range tx.writes {
		if tx.writes[i].locked {
			tx.writes[i].br.Unlock(0)
		}
	}
	return 0
}

func (tx *OTransaction) Commit() TID {
	// for each write key
	//  if global get from global store and lock
	for i, _ := range tx.writes {
		w := &tx.writes[i]
		if w.br == nil {
			br, err := tx.s.getKey(w.key)
			if *CountKeys {
				p, r := UndoCKey(w.key)
				if r == 112 {
					tx.w.NKeyAccesses[p]++
				}
			}
			if br == nil || err != nil {
				switch w.op {
				case SUM:
					br = tx.s.CreateInt32Key(w.key, 0, w.op)
				case MAX:
					br = tx.s.CreateInt32Key(w.key, 0, w.op)
				case LIST:
					br = tx.s.CreateKey(w.key, nil, LIST)
				default:
					if w.v == nil {
						br = tx.s.CreateKey(w.key, "", WRITE)
					} else {
						br = tx.s.CreateKey(w.key, w.v, WRITE)
					}
				}
			}
			w.br = br
		}
		if *SysType == DOPPEL && tx.phase == SPLIT {
			if w.br.dd {
				continue
			}
		}
		if !w.br.Lock() {
			if tx.count {
				tx.ls.candidates.Conflict(w.key)
			}
			tx.w.Nstats[NO_LOCK]++
			return tx.Abort()
		}
		w.locked = true
	}
	// TODO: acquire timestamp higher than anything i've read or am
	// writing
	tid := tx.w.commitTID()

	// for each read key
	//  verify
	if len(tx.read) != len(tx.lasts) {
		debug.PrintStack()
		log.Fatalf("Mismatch in lengths reads: %v, lasts: %v\n", tx.read, tx.lasts)
	}
	for i, _ := range tx.read {
		// Would have checked for dd earlier
		if tx.count {
			tx.ls.candidates.Read(tx.read[i].key)
		}
		rd := false
		if !tx.read[i].Verify(tx.lasts[i]) {
			for j, _ := range tx.writes {
				if tx.writes[j].key == tx.read[i].key {
					// We would have aborted if we did not
					// successfully lock this earlier, but check to
					// make sure the version didn't change after I
					// read but before I locked
					rd = tx.read[i].Own(tx.lasts[i])
					break
				}
			}
			if rd {
				continue
			}
			if tx.count {
				tx.ls.candidates.Conflict(tx.read[i].key)
			}
			return tx.Abort()
			tx.w.Nstats[NFAIL_VERIFY]++
		}
	}
	// for each write key
	//  if dd and split phase, apply locally
	//  else apply globally and unlock
	for i, _ := range tx.writes {
		w := &tx.writes[i]
		if *SysType == DOPPEL && tx.phase == SPLIT && w.br.dd {
			if tx.count {
				tx.ls.candidates.Write(w.key)
			}
			switch w.op {
			case SUM:
				tx.ls.Apply(w.key, w.op, w.vint32, w.op)
			case MAX:
				tx.ls.Apply(w.key, w.op, w.vint32, w.op)
			case LIST:
				tx.ls.ApplyList(w.key, w.ve)
			default:
				tx.ls.Apply(w.key, w.op, w.v, w.op)
			}
		} else {
			switch w.op {
			case SUM:
				tx.s.SetInt32(w.br, w.vint32, w.op)
			case MAX:
				tx.s.SetInt32(w.br, w.vint32, w.op)
			case LIST:
				tx.s.Set(w.br, w.ve, w.op)
			default:
				tx.s.Set(w.br, w.v, w.op)
			}
			w.br.Unlock(tid)
		}
	}
	return tid
}

type Rec struct {
	br     *BRecord
	read   bool
	v      interface{}
	vint32 int32
	ve     Entry
	kt     KeyType
}

// Not threadsafe.  Tracks execution of transaction.
type LTransaction struct {
	padding0 [128]byte
	keys     []Rec
	w        *Worker
	s        *Store
	t        int64 // Used just as a rough count
	ls       *LocalStore
	phase    int
	padding  [128]byte
}

func StartLTransaction(w *Worker) *LTransaction {
	tx := &LTransaction{
		keys: make([]Rec, 0, 100),
		w:    w,
		s:    w.store,
		ls:   w.local_store,
	}
	return tx
}

func (tx *LTransaction) Reset() {
	tx.keys = tx.keys[:0]
	tx.t++
}

func (tx *LTransaction) Read(k Key) (*BRecord, error) {
	// TODO: If I wrote the key, return that value instead
	br, err := tx.s.getKey(k)
	if *CountKeys {
		p, r := UndoCKey(k)
		if r == 117 {
			tx.w.NKeyAccesses[p]++
		}
	}
	if err != nil {
		return nil, err
	}
	br.mu.RLock()
	n := len(tx.keys)
	tx.keys = tx.keys[0 : n+1]
	tx.keys[n] = Rec{br: br, read: true}
	return br, nil
}

func (tx *LTransaction) upgrade(k Key) int {
	for i := 0; i < len(tx.keys); i++ {
		if tx.keys[i].br.key == k && tx.keys[i].read == true {
			tx.keys[i].read = false
			tx.keys[i].br.mu.RUnlock()
			return i
		}
	}
	return -1
}

func (tx *LTransaction) WriteInt32(k Key, a int32, op KeyType) error {
	if len(tx.keys) == cap(tx.keys) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.keys)
	if x := tx.upgrade(k); x >= 0 {
		n = x
	} else {
		tx.keys = tx.keys[0 : n+1]
	}
	br, err := tx.s.getKey(k)
	if br == nil || err != nil {
		br = tx.s.CreateInt32Key(k, a, op)
	}
	br.mu.Lock()
	tx.keys[n] = Rec{br: br, read: false, vint32: a, kt: op}
	return nil
}

func (tx *LTransaction) Write(k Key, v Value, kt KeyType) {
	if kt == SUM || kt == MAX {
		tx.WriteInt32(k, v.(int32), kt)
		return
	}
	if len(tx.keys) == cap(tx.keys) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.keys)
	if x := tx.upgrade(k); x >= 0 {
		n = x
	} else {
		tx.keys = tx.keys[0 : n+1]
	}
	br, err := tx.s.getKey(k)
	if br == nil || err != nil {
		dlog.Printf("Creating %v %v %v\n", k, v, kt)
		br = tx.s.CreateKey(k, v, kt)
	}
	br.mu.Lock()
	tx.keys[n] = Rec{br: br, read: false, v: v, kt: kt}
}

func (tx *LTransaction) WriteList(k Key, l Entry, kt KeyType) {
	if kt != LIST {
		log.Fatalf("Not a list\n")
	}
	if len(tx.keys) == cap(tx.keys) {
		// TODO: extend
		log.Fatalf("Ran out of room\n")
	}
	n := len(tx.keys)
	if x := tx.upgrade(k); x >= 0 {
		n = x
	} else {
		tx.keys = tx.keys[0 : n+1]
	}
	br, err := tx.s.getKey(k)
	if br == nil || err != nil {
		br = tx.s.CreateKey(k, nil, LIST)
	}
	br.mu.Lock()
	tx.keys[n] = Rec{br: br, read: false, ve: l, kt: kt}
}

func (tx *LTransaction) SetPhase(p int) {
	tx.phase = p
}

func (tx *LTransaction) Store() *Store {
	return tx.s
}

func (tx *LTransaction) Abort() TID {
	for i := len(tx.keys) - 1; i >= 0; i-- {
		if tx.keys[i].read {
			tx.keys[i].br.mu.RUnlock()
		} else {
			tx.keys[i].br.mu.Unlock()
		}
	}
	return 0
}

func (tx *LTransaction) Commit() TID {
	tid := tx.w.commitTID()
	for i := len(tx.keys) - 1; i >= 0; i-- {
		//x, y := UndoCKey(tx.keys[i].br.key)
		//dlog.Printf("Dealing with key %v %v", x, y)
		// Apply and unlock
		if tx.keys[i].read == false {
			switch tx.keys[i].kt {
			case SUM:
				tx.s.SetInt32(tx.keys[i].br, tx.keys[i].vint32, tx.keys[i].kt)
			case MAX:
				tx.s.SetInt32(tx.keys[i].br, tx.keys[i].vint32, tx.keys[i].kt)
			case LIST:
				tx.s.Set(tx.keys[i].br, tx.keys[i].ve, tx.keys[i].kt)
			default:
				tx.s.Set(tx.keys[i].br, tx.keys[i].v, tx.keys[i].kt)
			}
			tx.keys[i].br.mu.Unlock()
		} else {
			tx.keys[i].br.mu.RUnlock()
		}
	}
	return tid
}
