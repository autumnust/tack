package hibana

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// ID is a 26-character ULID-style identifier: time-sortable + unique.
// First 10 chars encode 48-bit ms timestamp; last 16 encode 80 random bits.
// Crockford base32 (no I, L, O, U) so they're case-insensitive and look ok in text.
type ID string

const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// monotonic guards against same-millisecond ID generation: if NewID is called
// twice within one ms we deterministically advance the random tail rather
// than risk a collision.
var (
	idMu       sync.Mutex
	lastIDMS   uint64
	lastIDRand [10]byte
)

// NewID returns a fresh sortable ID.
func NewID() ID {
	idMu.Lock()
	defer idMu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	var rnd [10]byte
	if ms == lastIDMS {
		// Same ms as previous call — increment the random tail to keep sortable
		// uniqueness intact within that ms.
		rnd = lastIDRand
		for i := len(rnd) - 1; i >= 0; i-- {
			rnd[i]++
			if rnd[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(rnd[:]); err != nil {
			// crypto/rand failure on a healthy system is essentially impossible;
			// fall back to a time-derived tail so we still produce *something*
			// rather than panic and lose the user's note.
			binary.BigEndian.PutUint64(rnd[:8], uint64(time.Now().UnixNano()))
		}
	}
	lastIDMS = ms
	lastIDRand = rnd
	return ID(encodeID(ms, rnd))
}

// encodeID renders the (ms, rnd) pair as 26 base32 chars: 10 for time, 16 for entropy.
func encodeID(ms uint64, rnd [10]byte) string {
	out := make([]byte, 26)
	// Time portion: 48 bits = 50 bits of base32 (10 chars), top 2 bits zero.
	for i := 9; i >= 0; i-- {
		out[i] = idAlphabet[ms&0x1f]
		ms >>= 5
	}
	// Random portion: 80 bits = 16 chars.
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

// ValidID reports whether s looks like a 26-char ID. Cheap structural check;
// we don't validate that every character is in the alphabet.
func ValidID(s string) bool { return len(s) == 26 }

// String makes ID printable.
func (id ID) String() string { return string(id) }

// Less compares two IDs lexicographically (which is also chronological for
// IDs minted on the same machine).
func (id ID) Less(other ID) bool { return string(id) < string(other) }

// must is a tiny helper used only in tests / panics.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("hibana: %s", err))
	}
}
