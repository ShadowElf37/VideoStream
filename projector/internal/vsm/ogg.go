package vsm

// Ogg demuxing, enough to pull Opus packets out of what ffmpeg writes with
// `-f opus`. We only need packet boundaries and durations, so this implements
// the page/segment framing (RFC 3533) and the Opus TOC (RFC 6716 §3.1) and
// nothing else — no CRC checking, no seeking, no chaining.

import (
	"errors"
	"fmt"
	"io"
)

const oggCapture = "OggS"

// OpusPacket is one encoded packet and the number of 48 kHz samples it covers.
type OpusPacket struct {
	Data    []byte
	Samples int
}

// oggReader turns a byte stream of Ogg pages into packets.
type oggReader struct {
	r io.Reader
	// partial accumulates segments of a packet continued across pages.
	partial []byte
	queue   []([]byte)
	// headersSeen counts the two mandatory Opus header packets (OpusHead,
	// OpusTags), which carry no audio and must not reach the caller.
	headersSeen int
}

func newOggReader(r io.Reader) *oggReader { return &oggReader{r: r} }

// next returns the next audio packet, or io.EOF.
func (o *oggReader) next() (OpusPacket, error) {
	for {
		if len(o.queue) > 0 {
			pkt := o.queue[0]
			o.queue = o.queue[1:]
			if o.headersSeen < 2 {
				o.headersSeen++
				continue
			}
			n, err := opusSamples(pkt)
			if err != nil {
				return OpusPacket{}, err
			}
			return OpusPacket{Data: pkt, Samples: n}, nil
		}
		if err := o.readPage(); err != nil {
			return OpusPacket{}, err
		}
	}
}

func (o *oggReader) readPage() error {
	var hdr [27]byte
	if _, err := io.ReadFull(o.r, hdr[:]); err != nil {
		return err // io.EOF here is the clean end of the stream
	}
	if string(hdr[0:4]) != oggCapture {
		return fmt.Errorf("ogg: bad capture pattern %q", hdr[0:4])
	}
	if hdr[4] != 0 {
		return fmt.Errorf("ogg: unsupported stream structure version %d", hdr[4])
	}
	continued := hdr[5]&0x01 != 0
	nsegs := int(hdr[26])

	table := make([]byte, nsegs)
	if _, err := io.ReadFull(o.r, table); err != nil {
		return fmt.Errorf("ogg: segment table: %w", err)
	}
	total := 0
	for _, n := range table {
		total += int(n)
	}
	body := make([]byte, total)
	if _, err := io.ReadFull(o.r, body); err != nil {
		return fmt.Errorf("ogg: page body: %w", err)
	}

	// A packet is the concatenation of segments up to and including the first
	// one shorter than 255; a trailing run of 255s continues onto the next
	// page.
	if !continued && len(o.partial) > 0 {
		// The previous page ended mid-packet but this one does not continue
		// it. Treat the fragment as lost rather than emitting a corrupt packet.
		o.partial = nil
	}
	cur := o.partial
	o.partial = nil
	pos := 0
	for _, n := range table {
		cur = append(cur, body[pos:pos+int(n)]...)
		pos += int(n)
		if n < 255 {
			o.queue = append(o.queue, cur)
			cur = nil
		}
	}
	o.partial = cur
	return nil
}

var errShortPacket = errors.New("opus: empty packet")

// opusSamples derives a packet's duration from its TOC byte. config selects
// the mode and frame size; the low two bits of the TOC say how many frames the
// packet holds, with code 3 putting the count in the following byte.
func opusSamples(pkt []byte) (int, error) {
	if len(pkt) == 0 {
		return 0, errShortPacket
	}
	toc := pkt[0]
	config := toc >> 3
	var frameNS int
	switch {
	case config < 12: // SILK: 10, 20, 40, 60 ms
		frameNS = []int{10, 20, 40, 60}[config%4] * 1e6
	case config < 16: // Hybrid: 10, 20 ms
		frameNS = []int{10, 20}[config%2] * 1e6
	default: // CELT: 2.5, 5, 10, 20 ms
		frameNS = []int{2500000, 5000000, 10000000, 20000000}[config%4]
	}
	frames := 1
	switch toc & 0x03 {
	case 0:
		frames = 1
	case 1, 2:
		frames = 2
	case 3:
		if len(pkt) < 2 {
			return 0, errShortPacket
		}
		frames = int(pkt[1] & 0x3f)
		if frames == 0 {
			return 0, errors.New("opus: zero frames in code 3 packet")
		}
	}
	// 48 kHz throughout: the push step pins the sample rate, and a WebRTC
	// Opus track is 48 kHz by definition. Every standard frame size divides
	// exactly at 48 kHz (2.5 ms -> 120 samples, 20 ms -> 960).
	return int(int64(frames) * int64(frameNS) * 48000 / 1e9), nil
}

// ReadOggOpus decodes an Ogg Opus stream into packets, sending each on out.
// It returns nil at the clean end of the stream. The caller closes out.
func ReadOggOpus(r io.Reader, out chan<- OpusPacket) error {
	o := newOggReader(r)
	for {
		pkt, err := o.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		out <- pkt
	}
}
