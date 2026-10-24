package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The output: log lines, and - in a terminal - a status line at the bottom (inverted): which image of how many, the
// current download with a bar, its % and speed. Redrawn twice a second, so a stalled download shows as slow, not
// stuck. Not a terminal (redirected to a file, ...): just the log lines.

const (
	statusRedraw = 500 * time.Millisecond
	speedWindow  = 3 * time.Second // speed = bytes in the last few seconds
	barWidth     = 10
)

type speedSample struct {
	at   time.Time
	done int64
}

type console struct {
	mu       sync.Mutex
	out      io.Writer
	terminal bool
	width    func() int // terminal width in chars

	active  bool // downloading now - the status line is shown
	n       int  // image n of total
	total   int
	name    string
	size    int64 // of the download, 0 = unknown
	done    int64
	samples []speedSample
	shown   bool // the status line is on the screen

	stop chan struct{}
}

func newConsole(out io.Writer, terminal bool, width func() int) *console {
	c := &console{out: out, terminal: terminal, width: width}
	if terminal {
		c.stop = make(chan struct{})
		go c.redrawLoop(c.stop)
	}
	return c
}

func (c *console) redrawLoop(stop chan struct{}) {
	t := time.NewTicker(statusRedraw)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.mu.Lock()
			c.drawStatus()
			c.mu.Unlock()
		}
	}
}

// close removes the status line, stops the redrawing
func (c *console) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		close(c.stop)
		c.stop = nil
	}
	c.active = false
	c.clearStatus()
}

// logf prints a line above the status line
func (c *console) logf(format string, a ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearStatus()
	fmt.Fprintf(c.out, format+"\n", a...)
	c.drawStatus()
}

// startFile - downloading the archive for image n of total
func (c *console) startFile(n, total int, name string, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active, c.n, c.total, c.name, c.size, c.done = true, n, total, name, size, 0
	c.samples = []speedSample{{time.Now(), 0}}
	c.drawStatus()
}

// progress - bytes of the current download; total from the server, if it told (else the size from the list)
func (c *console) progress(done, total int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done = done
	if total > 0 {
		c.size = total
	}
	now := time.Now()
	if len(c.samples) == 0 || now.Sub(c.samples[len(c.samples)-1].at) >= 100*time.Millisecond {
		c.samples = append(c.samples, speedSample{now, done})
	}
}

func (c *console) endFile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = false
	c.clearStatus()
}

func (c *console) clearStatus() {
	if c.terminal && c.shown {
		fmt.Fprint(c.out, "\r\x1b[K")
		c.shown = false
	}
}

func (c *console) drawStatus() {
	if !c.terminal || !c.active {
		return
	}
	w := c.width() - 1 // not into the last column - some terminals wrap then
	if w < 20 {
		return
	}
	line := statusLine(c.n, c.total, c.name, c.done, c.size, c.speed(), w)
	fmt.Fprint(c.out, "\r\x1b[7m"+line+"\x1b[0m\x1b[K")
	c.shown = true
}

// speed in bytes/s over the last speedWindow - goes down to 0 when nothing comes
func (c *console) speed() float64 {
	now := time.Now()
	for len(c.samples) > 1 && now.Sub(c.samples[1].at) > speedWindow { // keep one sample older than the window
		c.samples = c.samples[1:]
	}
	if len(c.samples) == 0 {
		return 0
	}
	first := c.samples[0]
	elapsed := now.Sub(first.at).Seconds()
	if elapsed < 0.3 {
		return 0
	}
	return float64(c.done-first.done) / elapsed
}

// statusLine: " 12/4381  0% | ACO-B_Flight-of-the-Intruder.msa [====      ]  41%  245 kB/s ", exactly w chars
func statusLine(n, total int, name string, done, size int64, speed float64, w int) string {
	overall := 0
	if total > 0 {
		overall = (n - 1) * 100 / total
	}
	left := fmt.Sprintf(" %d/%d %3d%% | ", n, total, overall)

	bar, pct := strings.Repeat(" ", barWidth), " ?%"
	if size > 0 {
		f := float64(done) / float64(size)
		if f > 1 {
			f = 1
		}
		filled := int(f*barWidth + 0.5)
		bar = strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)
		pct = fmt.Sprintf("%3d%%", int(f*100))
	}
	right := fmt.Sprintf(" [%s] %s %s ", bar, pct, formatSpeed(speed))

	room := w - len(left) - len(right)
	if room < 8 { // too narrow for the name - without it
		return pad(left+right, w)
	}
	return pad(left+fit(name, room)+right, w)
}

func formatSpeed(bps float64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%5.1f MB/s", bps/(1<<20))
	default:
		return fmt.Sprintf("%5.0f kB/s", bps/1024)
	}
}

// fit cuts s to n chars, "..." at the end when cut
func fit(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

func pad(s string, n int) string {
	if l := utf8.RuneCountInString(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return string([]rune(s)[:n])
}
