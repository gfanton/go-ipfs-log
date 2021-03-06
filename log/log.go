package log // import "berty.tech/go-ipfs-log/log"

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"berty.tech/go-ipfs-log/accesscontroller"
	"berty.tech/go-ipfs-log/entry"
	"berty.tech/go-ipfs-log/errmsg"
	"berty.tech/go-ipfs-log/identityprovider"
	"berty.tech/go-ipfs-log/io"
	"berty.tech/go-ipfs-log/utils/lamportclock"
	"github.com/iancoleman/orderedmap"
	cid "github.com/ipfs/go-cid"
	cbornode "github.com/ipfs/go-ipld-cbor"
	"github.com/pkg/errors"
	"github.com/polydawn/refmt/obj/atlas"
)

type JSONLog struct {
	ID    string
	Heads []cid.Cid
}

type Log struct {
	Storage          *io.IpfsServices
	ID               string
	AccessController accesscontroller.Interface
	SortFn           func(a *entry.Entry, b *entry.Entry) (int, error)
	Identity         *identityprovider.Identity
	Entries          *entry.OrderedMap
	heads            *entry.OrderedMap
	Next             *entry.OrderedMap
	Clock            *lamportclock.LamportClock
}

type NewLogOptions struct {
	ID               string
	AccessController accesscontroller.Interface
	Entries          *entry.OrderedMap
	Heads            []*entry.Entry
	Clock            *lamportclock.LamportClock
	SortFn           func(a *entry.Entry, b *entry.Entry) (int, error)
}

type Snapshot struct {
	ID     string
	Heads  []cid.Cid
	Values []*entry.Entry
	Clock  *lamportclock.LamportClock
}

// max returns the larger of x or y.
func maxInt(x, y int) int {
	if x < y {
		return y
	}
	return x
}

func maxClockTimeForEntries(entries []*entry.Entry, defValue int) int {
	max := defValue
	for _, e := range entries {
		max = maxInt(e.Clock.Time, max)
	}

	return max
}

func NewLog(services *io.IpfsServices, identity *identityprovider.Identity, options *NewLogOptions) (*Log, error) {
	if services == nil {
		return nil, errmsg.IPFSNotDefined
	}

	if identity == nil {
		return nil, errmsg.IdentityNotDefined
	}

	if options == nil {
		options = &NewLogOptions{}
	}

	if options.ID == "" {
		options.ID = strconv.FormatInt(time.Now().Unix()/1000, 10)
	}

	if options.SortFn == nil {
		options.SortFn = LastWriteWins
	}

	maxTime := 0
	if options.Clock != nil {
		maxTime = options.Clock.Time
	}
	maxTime = maxClockTimeForEntries(options.Heads, maxTime)

	if options.AccessController == nil {
		options.AccessController = &accesscontroller.Default{}
	}

	if options.Entries == nil {
		options.Entries = entry.NewOrderedMap()
	}

	if len(options.Heads) == 0 && len(options.Entries.Keys()) > 0 {
		options.Heads = FindHeads(options.Entries)
	}

	next := entry.NewOrderedMap()
	for _, key := range options.Entries.Keys() {
		entry := options.Entries.UnsafeGet(key)
		for _, n := range entry.Next {
			next.Set(n.String(), entry)
		}
	}

	return &Log{
		Storage:          services,
		ID:               options.ID,
		Identity:         identity,
		AccessController: options.AccessController,
		SortFn:           NoZeroes(options.SortFn),
		Entries:          options.Entries.Copy(),
		heads:            entry.NewOrderedMapFromEntries(options.Heads),
		Next:             next,
		Clock:            lamportclock.New(identity.PublicKey, maxTime),
	}, nil
}

// addToStack Add an entry to the stack and traversed nodes index
func (l *Log) addToStack(e *entry.Entry, stack []*entry.Entry, traversed *orderedmap.OrderedMap) ([]*entry.Entry, *orderedmap.OrderedMap) {
	// If we've already processed the entry, don't add it to the stack
	if _, ok := traversed.Get(e.Hash.String()); ok {
		return stack, traversed
	}

	// Add the entry in front of the stack and sort
	stack = append([]*entry.Entry{e}, stack...)
	entry.Sort(l.SortFn, stack)
	Reverse(stack)

	// Add to the cache of processed entries
	traversed.Set(e.Hash.String(), true)

	return stack, traversed
}

func (l *Log) Traverse(rootEntries *entry.OrderedMap, amount int, endHash string) ([]*entry.Entry, error) {
	if rootEntries == nil {
		return nil, errmsg.EntriesNotDefined
	}

	// Sort the given given root entries and use as the starting stack
	stack := rootEntries.Slice()

	entry.Sort(l.SortFn, stack)
	Reverse(stack)

	// Cache for checking if we've processed an entry already
	traversed := orderedmap.New()
	// End result
	result := []*entry.Entry{}
	// We keep a counter to check if we have traversed requested amount of entries
	count := 0

	// Start traversal
	// Process stack until it's empty (traversed the full log)
	// or when we have the requested amount of entries
	// If requested entry amount is -1, traverse all
	for len(stack) > 0 && (amount < 0 || count < amount) {
		// Get the next element from the stack
		e := stack[0]
		stack = stack[1:]

		// Add to the result
		count++
		result = append(result, e)

		// Add entry's next references to the stack
		for _, next := range e.Next {
			nextEntry, ok := l.Entries.Get(next.String())
			if !ok {
				continue
			}

			stack, traversed = l.addToStack(nextEntry, stack, traversed)
		}

		// If it is the specified end hash, break out of the while loop
		if e.Hash.String() == endHash {
			break
		}
	}

	return result, nil
}

func (l *Log) Append(payload []byte, pointerCount int) (*entry.Entry, error) {
	// INFO: JS default value for pointerCount is 1
	// Update the clock (find the latest clock)
	newTime := maxClockTimeForEntries(l.heads.Slice(), 0)
	newTime = maxInt(l.Clock.Time, newTime) + 1

	l.Clock = lamportclock.New(l.Clock.ID, newTime)

	// Get the required amount of hashes to next entries (as per current state of the log)
	references, err := l.Traverse(l.heads, maxInt(pointerCount, l.heads.Len()), "")
	if err != nil {
		return nil, errors.Wrap(err, "append failed")
	}

	next := []cid.Cid{}

	keys := l.heads.Keys()
	for _, k := range keys {
		e, _ := l.heads.Get(k)
		next = append(next, e.Hash)
	}
	for _, e := range references {
		next = append(next, e.Hash)
	}

	// TODO: ensure port of ```Object.keys(Object.assign({}, this._headsIndex, references))``` is correctly implemented

	// @TODO: Split Entry.create into creating object, checking permission, signing and then posting to IPFS
	// Create the entry and add it to the internal cache
	e, err := entry.CreateEntry(l.Storage, l.Identity, &entry.Entry{
		LogID:   l.ID,
		Payload: payload,
		Next:    next,
	}, l.Clock)
	if err != nil {
		return nil, errors.Wrap(err, "append failed")
	}

	if err := l.AccessController.CanAppend(e, l.Identity); err != nil {
		return nil, errors.Wrap(err, "append failed")
	}

	l.Entries.Set(e.Hash.String(), e)

	for _, k := range keys {
		nextEntry, _ := l.heads.Get(k)
		l.Next.Set(nextEntry.Hash.String(), e)
	}

	l.heads = entry.NewOrderedMap()
	l.heads.Set(e.Hash.String(), e)

	return e, nil
}

type IteratorOptions struct {
	GT     *entry.Entry
	GTE    *entry.Entry
	LT     *entry.Entry
	LTE    *entry.Entry
	Amount *int
}

func (l *Log) iterator(options IteratorOptions, output chan<- *entry.Entry) error {
	amount := -1
	if options.Amount != nil {
		if *options.Amount == 0 {
			return nil
		} else {
			amount = *options.Amount
		}
	}

	start := l.heads.Slice()
	if options.LTE != nil {
		start = []*entry.Entry{options.LTE}
	} else if options.LT != nil {
		start = []*entry.Entry{options.LT}
	}

	endHash := ""
	if options.GTE != nil {
		endHash = options.GTE.Hash.String()
	} else if options.GT != nil {
		endHash = options.GT.Hash.String()
	}

	count := -1
	if endHash == "" && options.Amount != nil {
		count = amount
	}

	entries, err := l.Traverse(entry.NewOrderedMapFromEntries(start), count, endHash)
	if err != nil {
		return errors.Wrap(err, "iterator failed")
	}

	if options.GT != nil {
		entries = entries[:len(entries)-1]
	}

	// Deal with the amount argument working backwards from gt/gte
	if (options.GT != nil || options.GTE != nil) && amount > -1 {
		entries = entries[len(entries)-amount:]
	}

	for i := range entries {
		output <- entries[i]
	}

	return nil
}

func (l *Log) Join(otherLog *Log, size int) (*Log, error) {
	// INFO: JS default size is -1
	if otherLog == nil {
		return nil, errmsg.LogJoinNotDefined
	}

	if l.ID != otherLog.ID {
		return l, nil
	}

	newItems := Difference(otherLog, l)

	for _, k := range newItems.Keys() {
		e := newItems.UnsafeGet(k)
		if err := l.AccessController.CanAppend(e, l.Identity); err != nil {
			return nil, errors.Wrap(err, "join failed")
		}

		if err := entry.Verify(l.Identity.Provider, e); err != nil {
			return nil, errors.Wrap(err, "unable to check signature")
		}
	}

	for _, k := range newItems.Keys() {
		e := newItems.UnsafeGet(k)
		for _, next := range e.Next {
			l.Next.Set(next.String(), e)
		}

		l.Entries.Set(e.Hash.String(), e)
	}

	nextsFromNewItems := orderedmap.New()
	for _, k := range newItems.Keys() {
		e := newItems.UnsafeGet(k)
		for _, n := range e.Next {
			nextsFromNewItems.Set(n.String(), true)
		}
	}

	mergedHeads := FindHeads(l.heads.Merge(otherLog.heads))
	for idx, e := range mergedHeads {
		// notReferencedByNewItems
		if _, ok := nextsFromNewItems.Get(e.Hash.String()); ok {
			mergedHeads[idx] = nil
		}

		// notInCurrentNexts
		if _, ok := l.Next.Get(e.Hash.String()); ok {
			mergedHeads[idx] = nil
		}
	}

	l.heads = entry.NewOrderedMapFromEntries(mergedHeads)

	if size > -1 {
		tmp := l.Values().Slice()
		tmp = tmp[len(tmp)-size:]
		l.Entries = entry.NewOrderedMapFromEntries(tmp)
		l.heads = entry.NewOrderedMapFromEntries(FindHeads(entry.NewOrderedMapFromEntries(tmp)))
	}

	// Find the latest clock from the heads
	maxClock := maxClockTimeForEntries(l.heads.Slice(), 0)
	l.Clock = lamportclock.New(l.Clock.ID, maxInt(l.Clock.Time, maxClock))

	return l, nil
}

func Difference(logA, logB *Log) *entry.OrderedMap {
	if logA == nil || logA.Entries == nil || logA.Entries.Len() == 0 || logB == nil {
		return entry.NewOrderedMap()
	}

	if logB.Entries == nil {
		logB.Entries = entry.NewOrderedMap()
	}

	stack := logA.heads.Keys()
	traversed := map[string]bool{}
	res := entry.NewOrderedMap()

	for {
		if len(stack) == 0 {
			break
		}
		hash := stack[0]
		stack = stack[1:]

		eA, okA := logA.Entries.Get(hash)
		_, okB := logB.Entries.Get(hash)

		if okA && !okB && eA.LogID == logB.ID {
			res.Set(hash, eA)
			traversed[hash] = true
			for _, h := range eA.Next {
				hash := h.String()
				_, okB := logB.Entries.Get(hash)
				_, okT := traversed[hash]
				if !okT && !okB {
					stack = append(stack, hash)
					traversed[hash] = true
				}
			}
		}
	}

	return res
}

func (l *Log) ToString(payloadMapper func(*entry.Entry) string) string {
	values := l.Values().Slice()
	Reverse(values)

	lines := []string{}

	for _, e := range values {
		parents := entry.FindChildren(e, l.Values().Slice())
		length := len(parents)
		padding := strings.Repeat("  ", maxInt(length-1, 0))
		if length > 0 {
			padding = padding + "└─"
		}

		payload := ""
		if payloadMapper != nil {
			payload = payloadMapper(e)
		} else {
			payload = string(e.Payload)
		}

		lines = append(lines, padding+payload)
	}

	return strings.Join(lines, "\n")
}

func (l *Log) ToSnapshot() *Snapshot {
	return &Snapshot{
		ID:     l.ID,
		Heads:  entrySliceToCids(l.heads.Slice()),
		Values: l.Values().Slice(),
	}
}

func entrySliceToCids(slice []*entry.Entry) []cid.Cid {
	cids := []cid.Cid{}

	for _, e := range slice {
		cids = append(cids, e.Hash)
	}

	return cids
}

func (l *Log) ToBuffer() ([]byte, error) {
	return json.Marshal(l.ToJSON())
}

func (l *Log) ToMultihash() (cid.Cid, error) {
	return ToMultihash(l.Storage, l)
}

func NewFromMultihash(services *io.IpfsServices, identity *identityprovider.Identity, hash cid.Cid, logOptions *NewLogOptions, fetchOptions *FetchOptions) (*Log, error) {
	if services == nil {
		return nil, errmsg.IPFSNotDefined
	}

	if identity == nil {
		return nil, errmsg.IdentityNotDefined
	}

	if logOptions == nil {
		return nil, errmsg.LogOptionsNotDefined
	}

	if fetchOptions == nil {
		return nil, errmsg.FetchOptionsNotDefined
	}

	data, err := FromMultihash(services, hash, &FetchOptions{
		Length:       fetchOptions.Length,
		Exclude:      fetchOptions.Exclude,
		ProgressChan: fetchOptions.ProgressChan,
	})

	if err != nil {
		return nil, errors.Wrap(err, "newfrommultihash failed")
	}

	heads := []*entry.Entry{}
	for _, e := range data.Values {
		for _, h := range data.Heads {
			if e.Hash.String() == h.String() {
				heads = append(heads, e)
				break
			}
		}
	}

	return NewLog(services, identity, &NewLogOptions{
		ID:               data.ID,
		AccessController: logOptions.AccessController,
		Entries:          entry.NewOrderedMapFromEntries(data.Values),
		Heads:            heads,
		Clock:            lamportclock.New(data.Clock.ID, data.Clock.Time),
		SortFn:           logOptions.SortFn,
	})
}

func NewFromEntryHash(services *io.IpfsServices, identity *identityprovider.Identity, hash cid.Cid, logOptions *NewLogOptions, fetchOptions *FetchOptions) (*Log, error) {
	if logOptions == nil {
		return nil, errmsg.LogOptionsNotDefined
	}

	if fetchOptions == nil {
		return nil, errmsg.FetchOptionsNotDefined
	}

	// TODO: need to verify the entries with 'key'
	entries, err := FromEntryHash(services, []cid.Cid{hash}, &FetchOptions{
		Length:       fetchOptions.Length,
		Exclude:      fetchOptions.Exclude,
		ProgressChan: fetchOptions.ProgressChan,
	})
	if err != nil {
		return nil, errors.Wrap(err, "newfromentryhash failed")
	}

	return NewLog(services, identity, &NewLogOptions{
		ID:               logOptions.ID,
		AccessController: logOptions.AccessController,
		Entries:          entry.NewOrderedMapFromEntries(entries),
		SortFn:           logOptions.SortFn,
	})
}

func NewFromJSON(services *io.IpfsServices, identity *identityprovider.Identity, jsonLog *JSONLog, logOptions *NewLogOptions, fetchOptions *entry.FetchOptions) (*Log, error) {
	if logOptions == nil {
		return nil, errmsg.LogOptionsNotDefined
	}

	if fetchOptions == nil {
		return nil, errmsg.FetchOptionsNotDefined
	}

	// TODO: need to verify the entries with 'key'

	snapshot, err := FromJSON(services, jsonLog, &entry.FetchOptions{
		Length:       fetchOptions.Length,
		Timeout:      fetchOptions.Timeout,
		ProgressChan: fetchOptions.ProgressChan,
	})
	if err != nil {
		return nil, errors.Wrap(err, "newfromjson failed")
	}

	return NewLog(services, identity, &NewLogOptions{
		ID:               snapshot.ID,
		AccessController: logOptions.AccessController,
		Entries:          entry.NewOrderedMapFromEntries(snapshot.Values),
		SortFn:           logOptions.SortFn,
	})
}

func NewFromEntry(services *io.IpfsServices, identity *identityprovider.Identity, sourceEntries []*entry.Entry, logOptions *NewLogOptions, fetchOptions *entry.FetchOptions) (*Log, error) {
	if logOptions == nil {
		return nil, errmsg.LogOptionsNotDefined
	}

	if fetchOptions == nil {
		return nil, errmsg.FetchOptionsNotDefined
	}

	// TODO: need to verify the entries with 'key'
	snapshot, err := FromEntry(services, sourceEntries, &entry.FetchOptions{
		Length:       fetchOptions.Length,
		Exclude:      fetchOptions.Exclude,
		ProgressChan: fetchOptions.ProgressChan,
	})
	if err != nil {
		return nil, errors.Wrap(err, "newfromentry failed")
	}

	return NewLog(services, identity, &NewLogOptions{
		ID:               snapshot.ID,
		AccessController: logOptions.AccessController,
		Entries:          entry.NewOrderedMapFromEntries(snapshot.Values),
		SortFn:           logOptions.SortFn,
	})
}

func FindTails(entries []*entry.Entry) []*entry.Entry {
	// Reverse index { next -> entry }
	reverseIndex := map[string][]*entry.Entry{}
	// Null index containing entries that have no parents (nexts)
	nullIndex := []*entry.Entry{}
	// Hashes for all entries for quick lookups
	hashes := map[string]bool{}
	// Hashes of all next entries
	nexts := []cid.Cid{}

	for _, e := range entries {
		if len(e.Next) == 0 {
			nullIndex = append(nullIndex, e)
		}

		for _, nextE := range e.Next {
			reverseIndex[nextE.String()] = append(reverseIndex[nextE.String()], e)
		}

		nexts = append(nexts, e.Next...)

		hashes[e.Hash.String()] = true
	}

	tails := []*entry.Entry{}

	for _, n := range nexts {
		if _, ok := hashes[n.String()]; !ok {
			continue
		}

		tails = append(tails, reverseIndex[n.String()]...)
	}

	tails = append(tails, nullIndex...)

	return entry.NewOrderedMapFromEntries(tails).Slice()
}

func FindTailHashes(entries []*entry.Entry) []string {
	res := []string{}
	hashes := map[string]bool{}
	for _, e := range entries {
		hashes[e.Hash.String()] = true
	}

	for _, e := range entries {
		nextLength := len(e.Next)

		for i := range e.Next {
			next := e.Next[nextLength-i]
			if _, ok := hashes[next.String()]; !ok {
				res = append([]string{e.Hash.String()}, res...)
			}
		}
	}

	return res
}

func FindHeads(entries *entry.OrderedMap) []*entry.Entry {
	if entries == nil {
		return nil
	}

	result := []*entry.Entry{}
	items := orderedmap.New()

	for _, k := range entries.Keys() {
		e := entries.UnsafeGet(k)
		for _, n := range e.Next {
			items.Set(n.String(), e.Hash.String())
		}
	}

	for _, h := range entries.Keys() {
		e, ok := items.Get(h)
		if ok || e != nil {
			continue
		}

		result = append(result, entries.UnsafeGet(h))
	}

	sort.SliceStable(result, func(a, b int) bool {
		return bytes.Compare(result[a].Clock.ID, result[b].Clock.ID) < 0
	})

	return result
}

func (l *Log) Values() *entry.OrderedMap {
	if l.heads == nil {
		return entry.NewOrderedMap()
	}
	stack, _ := l.Traverse(l.heads, -1, "")
	Reverse(stack)

	return entry.NewOrderedMapFromEntries(stack)
}

func (l *Log) ToJSON() *JSONLog {
	stack := l.heads.Slice()
	entry.Sort(l.SortFn, stack)
	Reverse(stack)

	hashes := []cid.Cid{}
	for _, e := range stack {
		hashes = append(hashes, e.Hash)
	}

	return &JSONLog{
		ID:    l.ID,
		Heads: hashes,
	}
}

func (l *Log) Heads() *entry.OrderedMap {
	heads := l.heads.Slice()
	entry.Sort(l.SortFn, heads)
	Reverse(heads)

	return entry.NewOrderedMapFromEntries(heads)
}

var AtlasJSONLog = atlas.BuildEntry(JSONLog{}).
	StructMap().
	AddField("ID", atlas.StructMapEntry{SerialName: "id"}).
	AddField("Heads", atlas.StructMapEntry{SerialName: "heads"}).
	Complete()

func init() {
	cbornode.RegisterCborType(AtlasJSONLog)
}
