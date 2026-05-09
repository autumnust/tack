package discuss

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// ID is a 26-character ULID-style identifier: time-sortable + unique.
// Same shape as internal/hibana.ID — duplicated rather than abstracted
// because two callers isn't yet enough to justify extracting a shared
// idgen package.
type ID string

const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	idMu       sync.Mutex
	lastIDMS   uint64
	lastIDRand [10]byte
)

func NewID() ID {
	idMu.Lock()
	defer idMu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	var rnd [10]byte
	if ms == lastIDMS {
		rnd = lastIDRand
		for i := len(rnd) - 1; i >= 0; i-- {
			rnd[i]++
			if rnd[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(rnd[:]); err != nil {
			binary.BigEndian.PutUint64(rnd[:8], uint64(time.Now().UnixNano()))
		}
	}
	lastIDMS = ms
	lastIDRand = rnd
	return ID(encodeID(ms, rnd))
}

func encodeID(ms uint64, rnd [10]byte) string {
	out := make([]byte, 26)
	for i := 9; i >= 0; i-- {
		out[i] = idAlphabet[ms&0x1f]
		ms >>= 5
	}
	bits := uint64(0)
	bitCount := uint(0)
	pos := 10
	for _, b := range rnd {
		bits = (bits << 8) | uint64(b)
		bitCount += 8
		for bitCount >= 5 {
			bitCount -= 5
			out[pos] = idAlphabet[(bits>>bitCount)&0x1f]
			pos++
		}
	}
	return string(out)
}

func ValidID(s string) bool { return len(s) == 26 }

func (id ID) String() string { return string(id) }
