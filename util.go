package ddtxn

import (
	crand "crypto/rand"
	"ddtxn/dlog"
	"fmt"
	"math"
)

func RandN(seed *uint32, n uint32) uint32 {
	*seed = *seed*1103515245 + 12345
	return ((*seed & 0x7fffffff) % (n * 8) / 8)
}

func Randstr(sz int) string {
	alphanum := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, sz)
	crand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return string(bytes)
}

func Validate(c *Coordinator, s *Store, nkeys int, nproducts int, val []int32, n int) bool {
	good := true
	dlog.Printf("Validate start, store at %x\n", c.GetEpoch())
	zero_cnt := 0
	for j := 0; j < nproducts; j++ {
		var x int32
		k := ProductKey(j)
		v, err := s.getKey(k)
		if err != nil {
			if val[j] != 0 {
				fmt.Printf("Validating key %v failed; store: none should have: %v\n", k, val[j])
				good = false
			}
			continue
		}
		x = v.Value().(int32)
		dlog.Printf("Validate: %v %v\n", k, x)
		if x != val[j] {
			fmt.Printf("Validating key %v failed; store: %v should have: %v\n", k, x, val[j])
			good = false
		}
		if x == 0 {
			dlog.Printf("Saying x is zero %v %v\n", x, zero_cnt)
			zero_cnt++
		}
	}
	if zero_cnt == nproducts && n > 10 {
		fmt.Printf("Bad: all zeroes!\n")
		dlog.Printf("Bad: all zeroes!\n")
		good = false
	}
	dlog.Printf("Done validating\n")
	return good
}

func PrintLockCounts(s *Store) {
	fmt.Println()
	for _, chunk := range s.store {
		for k, v := range chunk.rows {
			if v.conflict > 0 {
				x, y := UndoCKey(k)
				fmt.Printf("%v %v\t:%v\n", x, y, v.conflict)
			}
		}
	}
}

func StddevChunks(nc []int64) (int64, float64) {
	var total int64
	var n int64
	var i int64
	n = int64(len(nc))
	for i = 0; i < n; i++ {
		total += nc[i]
	}
	mean := total / n
	variances := make([]int64, n)

	for i = 0; i < n; i++ {
		x := nc[i] - mean
		if x < 0 {
			x = x * -1
		}
		x = x * x
		variances[i] = x
	}

	var stddev int64
	for i = 0; i < n; i++ {
		stddev += variances[i]
	}
	return mean, math.Sqrt(float64(stddev / n))
}

func StddevKeys(nc []int64) (int64, float64) {
	var total int64
	var n int64
	var i int64
	n = int64(len(nc))
	var cnt int64
	for i = 0; i < n; i++ {
		if nc[i] != 0 {
			total += nc[i]
			cnt++
		}
	}
	mean := total / cnt
	variances := make([]int64, cnt)

	cnt = 0
	for i = 0; i < n; i++ {
		if nc[i] != 0 {
			x := nc[i] - mean
			if x < 0 {
				x = x * -1
			}
			x = x * x
			variances[cnt] = x
			cnt++
		}
	}

	var stddev int64
	for i = 0; i < cnt; i++ {
		stddev += variances[i]
	}
	return mean, math.Sqrt(float64(stddev / cnt))
}
