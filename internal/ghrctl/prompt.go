package ghrctl

import (
	"context"
	"errors"
	"os"

	"golang.org/x/term"
)

// Do not buffer beyond the newline: the next prompt may disable terminal echo.
// Restore terminal settings on cancellation even if a read is still blocked.
func readInput(ctx context.Context, f *os.File, hidden bool) (string, error) {
	type result struct {
		s string
		e error
	}
	var state *term.State
	var e error
	if hidden {
		state, e = term.GetState(int(f.Fd()))
		if e != nil {
			return "", e
		}
		defer term.Restore(int(f.Fd()), state)
	}
	ch := make(chan result, 1)
	go func() {
		if hidden {
			b, e := term.ReadPassword(int(f.Fd()))
			ch <- result{string(b), e}
			return
		}
		var b []byte
		one := make([]byte, 1)
		for {
			_, e := f.Read(one)
			if e != nil {
				ch <- result{"", e}
				return
			}
			if one[0] == '\n' {
				ch <- result{string(b), nil}
				return
			}
			b = append(b, one[0])
			if len(b) > 8192 {
				ch <- result{"", errors.New("input too long")}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		return r.s, r.e
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
