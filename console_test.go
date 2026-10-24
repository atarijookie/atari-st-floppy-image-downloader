package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStatusLine(t *testing.T) {
	line := statusLine(12, 4381, "ACO-B_Flight-of-the-Intruder.msa", 400, 1000, 250*1024, 90)
	if utf8.RuneCountInString(line) != 90 {
		t.Errorf("width %d: %q", utf8.RuneCountInString(line), line)
	}
	for _, want := range []string{" 12/4381   0% | ", "ACO-B_Flight-of-the-Intruder.msa", "[====      ]", " 40%", "250 kB/s"} {
		if !strings.Contains(line, want) {
			t.Errorf("no %q in %q", want, line)
		}
	}

	if l := statusLine(4381, 4381, "x.msa", 2000, 1000, 3<<20, 80); !strings.Contains(l, "[==========] 100%") ||
		!strings.Contains(l, "  3.0 MB/s") || !strings.Contains(l, " 99% |") {
		t.Errorf("full: %q", l)
	}
	if l := statusLine(1, 10, "x.msa", 500, 0, 0, 80); !strings.Contains(l, "[          ]  ?%") { // size unknown
		t.Errorf("unknown size: %q", l)
	}

	long := strings.Repeat("Long-Name_", 20) + ".msa"
	l := statusLine(1, 10, long, 0, 100, 0, 60)
	if utf8.RuneCountInString(l) != 60 || !strings.Contains(l, "...") {
		t.Errorf("long name not cut: %q", l)
	}
	if l := statusLine(1, 10, long, 0, 100, 0, 30); utf8.RuneCountInString(l) != 30 || strings.Contains(l, "Long") {
		t.Errorf("narrow: %q", l)
	}
}

func TestConsoleNotTerminalHasNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	c := newConsole(&buf, false, nil)
	c.startFile(1, 2, "a.msa", 100)
	c.progress(50, 100)
	c.logf("[1/2] a.msa  <- https://example.org/a.zip")
	c.endFile()
	c.close()
	if buf.String() != "[1/2] a.msa  <- https://example.org/a.zip\n" {
		t.Errorf("%q", buf.String())
	}
}

func TestConsoleTerminal(t *testing.T) {
	var buf safeBuffer
	c := newConsole(&buf, true, func() int { return 80 })
	c.startFile(3, 10, "a.msa", 1000)
	c.progress(300, 1000)
	time.Sleep(statusRedraw + 200*time.Millisecond) // redrawn by itself
	c.logf("line")
	c.endFile()
	c.close()

	out := buf.String()
	if !strings.Contains(out, "\x1b[7m") || !strings.Contains(out, "[===       ]  30%") {
		t.Errorf("no inverted status line with progress: %q", out)
	}
	if !strings.Contains(out, "\r\x1b[Kline\n") { // status line cleared before the log line, not mixed with it
		t.Errorf("log line not on its own: %q", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[K") { // nothing left on the screen at the end
		t.Errorf("status line left: %q", out[len(out)-30:])
	}
}

func TestSpeedDropsWhenStalled(t *testing.T) {
	c := newConsole(&bytes.Buffer{}, false, nil)
	c.startFile(1, 1, "a", 0)
	c.samples = []speedSample{{time.Now().Add(-10 * time.Second), 0}, {time.Now().Add(-5 * time.Second), 1 << 20}}
	c.done = 1 << 20 // nothing since 5 s ago
	if s := c.speed(); s > 1<<20/5 {
		t.Errorf("speed %f while stalled", s)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
