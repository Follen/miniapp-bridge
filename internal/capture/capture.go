package capture

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const MaxFrameSize = 64 << 20

var ErrRecorderClosed = errors.New("capture recorder is closed")

type Recorder struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

func Start(path string) (*Recorder, error) {
	f, e := os.Create(path)
	if e != nil {
		return nil, e
	}
	return &Recorder{f: f, w: bufio.NewWriter(f)}, nil
}
func (r *Recorder) Write(frame []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return ErrRecorderClosed
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(frame)))
	if _, e := r.w.Write(n[:]); e != nil {
		return e
	}
	if _, e := r.w.Write(frame); e != nil {
		return e
	}
	return r.w.Flush()
}
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil && r.w == nil {
		return nil
	}
	var out error
	if r.w != nil {
		if err := r.w.Flush(); err != nil {
			out = err
		}
	}
	if r.f != nil {
		if err := r.f.Close(); err != nil && out == nil {
			out = err
		}
	}
	r.w = nil
	r.f = nil
	return out
}
func Replay(path string) ([][]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var out [][]byte
	for {
		var n uint32
		if e := binary.Read(f, binary.BigEndian, &n); e == io.EOF {
			break
		} else if e != nil {
			return nil, e
		}
		if n > MaxFrameSize {
			return nil, fmt.Errorf("capture frame length %d exceeds limit %d", n, MaxFrameSize)
		}
		b := make([]byte, n)
		if _, e := io.ReadFull(f, b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, nil
}
