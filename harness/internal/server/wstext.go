package server

import (
	"strconv"
	"strings"
)

// wsProse is the text varied messages are cut from (WSMessage.Prose): JSON
// events from a fixed seed, so page and mimicry send the same bytes. It
// repeats at every distance, so compressors differing in level or window
// give different bytes. ASCII only, so JS length equals UTF-8 length.
var wsProse = makeProse(48 << 10)

// wsRand is the pseudo-random sequence of wsProse and wsNoise: a 32-bit
// linear congruential generator (Numerical Recipes' constants), which the
// page's script computes with Math.imul to the same values.
type wsRand uint32

func (r *wsRand) next() uint32 {
	*r = *r*1664525 + 1013904223
	return uint32(*r)
}

// pick returns one of words.
func (r *wsRand) pick(words []string) string {
	return words[r.next()>>8%uint32(len(words))]
}

// n returns a number below limit.
func (r *wsRand) n(limit uint32) uint32 {
	return r.next() >> 8 % limit
}

var (
	proseUsers = []string{
		"maria_k", "jdoe", "alex.r", "sam", "priya88", "tomasz", "lee_w", "nora",
		"kenji", "fatima.a", "olu", "beth_m", "carlos", "ivy", "dmitri", "zoe",
	}
	proseWords = []string{
		"is", "the", "blue", "one", "still", "in", "stock", "i", "need", "it",
		"by", "friday", "does", "this", "come", "with", "a", "charger", "thanks",
		"for", "help", "can", "you", "check", "my", "order", "status", "please",
		"what", "size", "should", "get", "usually", "wear", "medium", "but",
		"these", "run", "small", "shipping", "was", "fast", "love", "color",
		"how", "long", "does", "delivery", "take", "to", "canada", "any",
		"discount", "codes", "today", "that", "price", "looks", "great", "will",
		"order", "two", "of", "them", "are", "returns", "free", "if", "doesn't",
		"fit", "just", "got", "mine", "and", "works", "perfectly", "sorry",
		"wrong", "chat", "where", "is", "tracking", "number", "ok", "found",
		"lol", "yes", "no", "maybe", "later", "tomorrow", "morning", "back",
		"cart", "checkout", "keeps", "failing", "card", "declined", "tried",
		"again", "worked", "now",
	}
	proseQueries = []string{
		"running shoes", "wireless earbuds", "usb c cable", "winter jacket",
		"coffee grinder", "yoga mat", "phone case", "desk lamp", "backpack",
		"water bottle", "mechanical keyboard", "rain boots", "air fryer",
		"hdmi adapter", "wool socks", "standing desk",
	}
	proseColors = []string{"black", "white", "navy", "red", "olive", "grey", "sand"}
	proseSizes  = []string{"xs", "s", "m", "l", "xl", "8", "9", "10", "11"}
	proseOps    = []string{"add", "remove", "update"}
	proseRooms  = []string{"support", "general", "deals", "orders"}
)

// makeProse returns n bytes of events.
func makeProse(n int) string {
	r := wsRand(20261026)
	ts := uint64(1792900000000)
	var b strings.Builder
	for b.Len() < n {
		ts += uint64(r.n(5000)) + 1
		switch r.n(10) {
		case 0, 1, 2, 3, 4:
			words := make([]string, 3+r.n(12))
			for i := range words {
				words[i] = r.pick(proseWords)
			}
			b.WriteString(`{"type":"chat","room":"` + r.pick(proseRooms) + `","user":"` + r.pick(proseUsers) +
				`","text":"` + strings.Join(words, " ") + `","ts":` + strconv.FormatUint(ts, 10) + "}\n")
		case 5, 6:
			b.WriteString(`{"type":"search","q":"` + r.pick(proseQueries) + `","page":` + strconv.Itoa(int(r.n(5))+1) +
				`,"filters":{"color":"` + r.pick(proseColors) + `","size":"` + r.pick(proseSizes) + `"}}` + "\n")
		case 7, 8:
			b.WriteString(`{"type":"cart","op":"` + r.pick(proseOps) + `","sku":"SKU-` + strconv.Itoa(int(r.n(90000))+10000) +
				`","qty":` + strconv.Itoa(int(r.n(4))+1) + `,"price":` + strconv.Itoa(int(r.n(300))+1) + "." + strconv.Itoa(int(r.n(90))+10) + "}\n")
		default:
			b.WriteString(`{"type":"typing","user":"` + r.pick(proseUsers) + `"}` + "\n")
		}
	}
	return b.String()[:n]
}

// wsNoise returns n pseudo-random bytes seeded by seed: incompressible.
func wsNoise(seed, n int) []byte {
	r := wsRand(seed)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.next() >> 24)
	}
	return b
}
