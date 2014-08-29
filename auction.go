package ddtxn

import (
	"ddtxn/dlog"
	"log"
	"time"
)

const (
	NUM_USERS      = 1000000
	NUM_CATEGORIES = 20
	NUM_REGIONS    = 62
	NUM_ITEMS      = 330000
	BIDS_PER_ITEM  = 10
	NUM_COMMENTS   = 506000
	BUY_NOW        = .1 * NUM_ITEMS
	FEEDBACK       = .95 * NUM_ITEMS
)

type User struct {
	ID       uint64
	Name     string
	Nickname string
	Rating   uint64
	Region   uint64
}

type Item struct {
	ID        uint64
	Seller    uint64
	Qty       uint64
	Startdate int
	Enddate   int
	Name      string
	Desc      string
	Sprice    uint64
	Rprice    uint64
	Buynow    uint64
	Dur       uint64
	Categ     uint64
}

type Bid struct {
	ID     uint64
	Item   uint64
	Bidder uint64
	Price  int32
}

type BuyNow struct {
	BuyerID uint64
	ItemID  uint64
	Qty     uint64
	Date    int
}

type Comment struct {
	ID      uint64
	From    uint64
	To      uint64
	Rating  uint64
	Item    uint64
	Date    uint64
	Comment string
}

func RegisterUserTxn(t Query, tx ETransaction) (*Result, error) {
	nickname := t.S1
	region := t.U1

	var r *Result = nil
	n := tx.UID()
	nick := SKey(nickname)
	user := &User{
		ID:       uint64(n),
		Name:     Randstr(10),
		Nickname: nickname,
		Region:   region,
	}
	u := UserKey(int(n))
	tx.MaybeWrite(nick)
	_, err := tx.Read(nick)

	if err != ENOKEY {
		// Someone else is using this nickname
		dlog.Printf("Nickname taken %v %v\n", nickname, nick)
		tx.Abort()
		return nil, ENORETRY
	}
	tx.Write(u, user, WRITE)
	tx.Write(nick, nickname, WRITE)

	if tx.Commit() == 0 {
		dlog.Printf("Could not commit\n")
		return nil, EABORT
	}
	if *Allocate {
		r = &Result{uint64(n)}
		//dlog.Printf("Registered user %v %v\n", nickname, n)
	}
	return r, nil
}

func NewItemTxn(t Query, tx ETransaction) (*Result, error) {
	var r *Result = nil
	now := time.Now().Second()
	n := tx.UID()
	x := &Item{
		ID:        n,
		Name:      t.S1,
		Seller:    t.U1,
		Desc:      t.S2,
		Sprice:    t.U2,
		Rprice:    t.U3,
		Buynow:    t.U4,
		Dur:       t.U5,
		Qty:       t.U6,
		Startdate: now,
		Enddate:   t.I,
		Categ:     t.U7,
	}
	urec, err := tx.Read(UserKey(int(t.U1)))
	if err != nil {
		if err == ESTASH {
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("NewItemTxn: User doesn't exist %v\n", t.U1)
			return nil, ENORETRY
		}
		return nil, err
	}
	region := urec.value.(*User).Region
	val := Entry{order: now, top: int(n), key: ItemKey(n)}
	tx.Write(ItemKey(n), x, WRITE)
	tx.WriteList(ItemsByCatKey(x.Categ), val, LIST)
	tx.WriteList(ItemsByRegKey(region, x.Categ), val, LIST)
	err = tx.WriteInt32(MaxBidKey(n), int32(0), MAX)
	if err != nil {
		tx.Abort()
		dlog.Printf("Error creating new item %v! %v\n", x, err)
		return nil, err
	}
	tx.Write(MaxBidBidderKey(n), uint64(0), WRITE)
	err = tx.WriteInt32(NumBidsKey(n), int32(0), SUM)
	if err != nil {
		tx.Abort()
		dlog.Printf("Error creating new item %v! %v\n", x, err)
		return nil, err
	}

	if tx.Commit() == 0 {
		dlog.Printf("Abort %v!\n", x)
		return r, EABORT
	}

	if *Allocate {
		r = &Result{n}
		//dlog.Printf("Registered new item %v %v\n", x, n)
	}
	return r, nil
}

func StoreBidTxn(t Query, tx ETransaction) (*Result, error) {
	var r *Result = nil
	user := t.U1
	item := t.U2
	price := t.A
	// insert bid
	n := tx.UID()
	bid := &Bid{
		ID:     uint64(n),
		Item:   item,
		Bidder: user,
		Price:  price,
	}
	bid_key := PairBidKey(user, item)
	tx.Write(bid_key, bid, WRITE)

	// update max bid?
	high := MaxBidKey(item)
	bidder := MaxBidBidderKey(item)
	tx.MaybeWrite(high)
	max, err := tx.Read(high)
	if err != nil {
		if err == ESTASH {
			//dlog.Printf("Max bid key for item %v stashed\n", item)
			return nil, ESTASH
		}
		dlog.Printf("No max key for item? %v\n", item)
		tx.Abort()
		return nil, err
	}
	if price > max.int_value {
		err = tx.WriteInt32(high, price, MAX)
		if err != nil {
			tx.Abort()
			return nil, err
		}
		tx.Write(bidder, user, WRITE)
	}

	// update # bids per item
	err = tx.WriteInt32(NumBidsKey(item), 1, SUM)
	if err != nil {
		tx.Abort()
		return nil, err
	}

	// add to item's bid list
	e := Entry{int(bid.Price), bid_key, 0}
	tx.WriteList(BidsPerItemKey(item), e, LIST)
	if tx.Commit() == 0 {
		return r, EABORT
	}

	if *Allocate {
		r = &Result{uint64(n)}
		//dlog.Printf("%v Bid on %v %v\n", user, item, price)
	}
	return r, nil
}

func StoreCommentTxn(t Query, tx ETransaction) (*Result, error) {
	touser := t.U1
	fromuser := t.U2
	item := t.U3
	comment_s := t.S1
	rating := t.U4

	n := tx.UID()
	comment := &Comment{
		ID:      n,
		From:    fromuser,
		To:      touser,
		Rating:  rating,
		Comment: comment_s,
		Item:    item,
		Date:    11,
	}
	com := CommentKey(uint64(n))
	tx.Write(com, comment, WRITE)

	rkey := RatingKey(touser)
	err := tx.WriteInt32(rkey, int32(rating), SUM)
	if err != nil {
		tx.Abort()
		return nil, err
	}

	if tx.Commit() == 0 {
		dlog.Printf("Comment abort %v\n", t)
		return nil, EABORT
	}
	var r *Result = nil
	if *Allocate {
		r = &Result{uint64(n)}
		//dlog.Printf("%v Comment %v %v\n", touser, fromuser, item)
	}
	return r, nil
}

func StoreBuyNowTxn(t Query, tx ETransaction) (*Result, error) {
	now := 1
	user := t.U1
	item := t.U2
	qty := t.U3
	bnrec := &BuyNow{
		BuyerID: user,
		ItemID:  item,
		Qty:     qty,
		Date:    now,
	}
	uk := UserKey(int(t.U1))
	_, err := tx.Read(uk)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("User  %v stashed\n", t.U1)
			return nil, ESTASH
		}
		dlog.Printf("No user? %v\n", t.U1)
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return nil, EABORT
	}
	ik := ItemKey(item)
	tx.MaybeWrite(ik)
	irec, err := tx.Read(ik)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Item key  %v stashed\n", item)
			return nil, ESTASH
		}
		dlog.Printf("StoreBuyNowTxn: No item? %v\n", item)
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return nil, EABORT
	}
	itemv := irec.Value().(*Item)
	maxqty := itemv.Qty
	newq := maxqty - qty

	if maxqty < qty {
		dlog.Printf("Req quantity > quantity %v %v\n", qty, maxqty)
		tx.Abort()
		return nil, ENORETRY
	}
	bnk := BuyNowKey(uint64(tx.UID()))
	tx.Write(bnk, bnrec, WRITE)

	if newq == 0 {
		itemv.Enddate = now
		itemv.Qty = 0
	} else {
		itemv.Qty = newq
	}

	tx.Write(ik, itemv, WRITE)
	if tx.Commit() == 0 {
		return nil, EABORT
	}

	var r *Result = nil
	if *Allocate {
		r = &Result{qty}
	}
	return r, nil
}

func ViewBidHistoryTxn(t Query, tx ETransaction) (*Result, error) {
	item := t.U1
	ik := ItemKey(item)
	_, err := tx.Read(ik)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Item key  %v stashed\n", item)
			return nil, ESTASH
		}
		dlog.Printf("ViewBidTxn: Abort? %v err: %v\n", item, err)
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return nil, err
	}

	bids := BidsPerItemKey(item)
	brec, err := tx.Read(bids)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("BidsPerItem key  %v stashed\n", item)
			return nil, ESTASH
		}
		if err == ENOKEY {
			dlog.Printf("No bids for item %v\n", item)
			if tx.Commit() != 0 {
				return nil, EABORT
			} else {
				return nil, nil
			}
		}
		tx.Abort()
		return nil, err
	}
	listy := brec.entries

	var rbids []Bid
	var rnn []string

	if *Allocate {
		rbids = make([]Bid, len(listy))
		rnn = make([]string, len(listy))
	}

	for i := 0; i < len(listy); i++ {
		b, err := tx.Read(listy[i].key)
		if err != nil {
			if err == ESTASH {
				dlog.Printf("key stashed %v\n", listy[i].key)
				return nil, ESTASH
			}
			tx.Abort()
			if err == ENOKEY {
				dlog.Printf("No such key %v\n", listy[i].key)
				return nil, ENORETRY
			}
			return nil, err
		}
		bid := b.Value().(*Bid)
		if *Allocate {
			rbids[i] = *bid
		}
		uk := UserKey(int(bid.Bidder))
		u, err := tx.Read(uk)
		if err != nil {
			if err == ESTASH {
				dlog.Printf("user stashed %v\n", uk)
				return nil, ESTASH
			}
			tx.Abort()
			if err == ENOKEY {
				dlog.Printf("Viewing bid and user doesn't exist?! %v\n", uk)
				return nil, ENORETRY
			}
			return nil, err
		}
		if *Allocate {
			rnn[i] = u.Value().(*User).Nickname
		}
	}

	if tx.Commit() == 0 {
		return nil, EABORT
	}
	var r *Result = nil
	if *Allocate {
		r = &Result{
			&struct {
				bids []Bid
				nns  []string
			}{rbids, rnn}}
	}
	return r, nil
}

func ViewUserInfoTxn(t Query, tx ETransaction) (*Result, error) {
	uk := UserKey(int(t.U1))
	urec, err := tx.Read(uk)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("User  %v stashed\n", t.U1)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No user? %v\n", t.U1)
			return nil, ENORETRY
		}
		return nil, err
	}
	if tx.Commit() == 0 {
		return nil, EABORT
	}
	var r *Result = nil
	if *Allocate {
		r = &Result{urec.Value()}
	}
	return r, nil
}

func PutBidTxn(t Query, tx ETransaction) (*Result, error) {
	item := t.U1

	ik := ItemKey(item)
	irec, err := tx.Read(ik)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Item key  %v stashed\n", item)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("PutBidTxn: No item? %v\n", item)
			return nil, ENORETRY
		}
		return nil, err
	}
	tok := UserKey(int(irec.Value().(*Item).Seller))
	torec, err := tx.Read(tok)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("User key for user %v stashed\n", tok)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No user? %v\n", tok)
			return nil, ENORETRY
		}
		return nil, err
	}
	nickname := torec.Value().(*User).Nickname
	maxbk := MaxBidKey(item)
	maxbrec, err := tx.Read(maxbk)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Max bid key for item %v stashed\n", item)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No max bid? %v\n", item)
			return nil, ENORETRY
		}
		return nil, err
	}
	maxb := maxbrec.int_value

	numbk := NumBidsKey(item)
	numbrec, err := tx.Read(numbk)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Num bids key for item %v stashed\n", item)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No num bids? %v\n", item)
			return nil, ENORETRY
		}
		return nil, err
	}
	nb := numbrec.int_value
	if tx.Commit() == 0 {
		return nil, EABORT
	}
	var r *Result = nil
	if *Allocate {
		r = &Result{
			&struct {
				nick string
				max  int32
				numb int32
			}{nickname, maxb, nb},
		}
	}
	return r, nil
}

func PutCommentTxn(t Query, tx ETransaction) (*Result, error) {
	var r *Result = nil
	touser := t.U1
	item := t.U2
	tok := UserKey(int(touser))
	torec, err := tx.Read(tok)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("User key for user %v stashed\n", touser)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No user? %v\n", touser)
			return nil, ENORETRY
		}
		return nil, err
	}
	nickname := torec.Value().(*User).Nickname
	ik := ItemKey(item)
	irec, err := tx.Read(ik)
	if err != nil {
		if err == ESTASH {
			dlog.Printf("Item key  %v stashed\n", item)
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("PutCommentTxn: No item? %v\n", item)
			return nil, ENORETRY
		}
		return nil, err
	}
	itemname := irec.Value().(*Item).Name
	if tx.Commit() == 0 {
		return r, EABORT
	}
	if *Allocate {
		r = &Result{
			&struct {
				nick  string
				iname string
			}{nickname, itemname},
		}
	}
	return r, nil
}

func SearchItemsCategTxn(t Query, tx ETransaction) (*Result, error) {
	categ := t.U1
	num := t.U2
	var r *Result = nil
	if num > 10 {
		log.Fatalf("Only 10 search items are currently supported.\n")
	}
	ibck := ItemsByCatKey(categ)
	ibcrec, err := tx.Read(ibck)

	if err != nil {
		if err == ESTASH {
			return nil, ESTASH
		}
		dlog.Printf("SearchItemsCategTxn: Abort? %v %v\n", ibck, err)
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return r, err
	}
	listy := ibcrec.entries

	if len(listy) > 10 {
		dlog.Printf("Only 10 search items are currently supported %v %v\n", len(listy), listy)
	}

	var ret []*Item
	var maxb []int32
	var numb []int32

	if *Allocate {
		ret = make([]*Item, len(listy))
		maxb = make([]int32, len(listy))
		numb = make([]int32, len(listy))
	}

	var br *BRecord
	for i := 0; i < len(listy); i++ {
		k := uint64(listy[i].top)
		br, err = tx.Read(ItemKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			tx.Abort()
			if err == ENOKEY {
				dlog.Printf("search items cat: Item in list doesn't exist %v; %v\n", k, listy[i])
				return nil, ENORETRY
			}
			return r, err
		}
		if *Allocate {
			ret[i] = br.Value().(*Item)
		}
		br, err = tx.Read(MaxBidKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			dlog.Printf("No max bid key %v\n", k)
		} else {
			if *Allocate {
				maxb[i] = br.Value().(int32)
			}
		}
		br, err = tx.Read(NumBidsKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			dlog.Printf("No number of bids key %v\n", k)
		} else if *Allocate {
			numb[i] = br.Value().(int32)
		}
	}

	if tx.Commit() == 0 {
		return r, EABORT
	}
	if *Allocate {
		r = &Result{
			&struct {
				items   []*Item
				maxbids []int32
				numbids []int32
			}{ret, maxb, numb},
		}
		//dlog.Printf("Searched categ %v %v\n", categ, *r)
	}
	return r, nil
}

func SearchItemsRegionTxn(t Query, tx ETransaction) (*Result, error) {
	region := t.U1
	categ := t.U2
	num := t.U3
	var r *Result = nil
	if num > 10 {
		log.Fatalf("Only 10 search items are currently supported.\n")
	}
	ibrk := ItemsByRegKey(region, categ)
	ibrrec, err := tx.Read(ibrk)

	if err != nil {
		if err == ESTASH {
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			dlog.Printf("No index for region %v\n", ibrk)
			return nil, ENORETRY
		}
		return r, err
	}
	listy := ibrrec.entries

	if len(listy) > 10 {
		dlog.Printf("Only 10 search items are currently supported %v %v\n", len(listy), listy)
	}

	var ret []*Item
	var maxb []int32
	var numb []int32

	if *Allocate {
		ret = make([]*Item, len(listy))
		maxb = make([]int32, len(listy))
		numb = make([]int32, len(listy))
	}

	var br *BRecord
	for i := 0; i < len(listy); i++ {
		k := uint64(listy[i].top)
		br, err = tx.Read(ItemKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			tx.Abort()
			if err == ENOKEY {
				dlog.Printf("Item in list doesn't exist %v; %v\n", k, listy[i])
				return nil, ENORETRY
			}
			return r, err
		}
		if *Allocate {
			ret[i] = br.Value().(*Item)
		}
		br, err = tx.Read(MaxBidKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			dlog.Printf("No max bid key %v\n", k)
		} else {
			if *Allocate {
				maxb[i] = br.Value().(int32)
			}
		}
		br, err = tx.Read(NumBidsKey(k))
		if err != nil {
			if err == ESTASH {
				return nil, ESTASH
			}
			dlog.Printf("No number of bids key %v\n", k)
		} else if *Allocate {
			numb[i] = br.Value().(int32)
		}
	}

	if tx.Commit() == 0 {
		return r, EABORT
	}
	if *Allocate {
		r = &Result{
			&struct {
				items   []*Item
				maxbids []int32
				numbids []int32
			}{ret, maxb, numb},
		}
	}
	return r, nil
}

func ViewItemTxn(t Query, tx ETransaction) (*Result, error) {
	var r *Result = nil
	id := t.U1
	item, err := tx.Read(ItemKey(id))
	if err != nil {
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return r, err
	}
	maxbid, err := tx.Read(MaxBidKey(id))
	if err != nil {
		if err == ESTASH {
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return r, err
	}
	maxbidder, err := tx.Read(MaxBidBidderKey(id))
	if err != nil {
		if err == ESTASH {
			return nil, ESTASH
		}
		tx.Abort()
		if err == ENOKEY {
			return nil, ENORETRY
		}
		return r, err
	}
	if tx.Commit() == 0 {
		return r, EABORT
	}
	if *Allocate {
		r = &Result{&struct {
			Item
			int32
			uint64
		}{*item.Value().(*Item), maxbid.Value().(int32), maxbidder.Value().(uint64)}}
	}
	return r, nil
}

func GetTxns(skewed bool) []float64 {
	perc := make(map[float64]int)
	if skewed {
		perc = map[float64]int{
			10.0: RUBIS_SEARCHCAT,
			10.5: RUBIS_VIEW,
			5.47: RUBIS_SEARCHREG,
			4.97: RUBIS_PUTBID,
			40.0: RUBIS_BID,
			2.13: RUBIS_VIEWUSER,
			1.81: RUBIS_NEWITEM,
			1.8:  RUBIS_REGISTER,
			1.4:  RUBIS_BUYNOW,
			1.34: RUBIS_VIEWBIDHIST,
			.55:  RUBIS_PUTCOMMENT,
			.5:   RUBIS_COMMENT,
		}
	} else {
		perc = map[float64]int{
			13.4: RUBIS_SEARCHCAT,
			11.3: RUBIS_VIEW,
			5.47: RUBIS_SEARCHREG,
			4.97: RUBIS_PUTBID,
			3.7:  RUBIS_BID,
			2.13: RUBIS_VIEWUSER,
			1.81: RUBIS_NEWITEM,
			1.8:  RUBIS_REGISTER,
			1.4:  RUBIS_BUYNOW,
			1.34: RUBIS_VIEWBIDHIST,
			.55:  RUBIS_PUTCOMMENT,
			.5:   RUBIS_COMMENT,
		}
	}

	var sum float64
	for k, _ := range perc {
		sum += k
	}
	newperc := make(map[int]float64)
	for k, v := range perc {
		newperc[v] = 100 * k / sum
	}

	rates := make([]float64, len(perc))
	sum = 0
	for i := RUBIS_BID; i < BIG_INCR; i++ {
		sum = sum + newperc[i]
		rates[i-RUBIS_BID] = sum
	}
	return rates
}
