package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type Progress struct {
	output        io.Writer
	enabled       bool
	ticker        *time.Ticker
	done          chan struct{}
	renderedLines int
	nextID        int
	entries       []*progressEntry
	mutex         sync.Mutex
	waitGroup     sync.WaitGroup
}

type progressEntry struct {
	id        int
	label     string
	total     int64
	current   int64
	startedAt time.Time
}

type ProxyReader struct {
	io.ReadCloser
	progress *Progress
	entryID  int
}

func NewProgress(output io.Writer) *Progress {
	progress := &Progress{
		output:  output,
		enabled: isTerminalWriter(output),
		done:    make(chan struct{}),
		entries: make([]*progressEntry, 0),
	}

	if !progress.enabled {
		return progress
	}

	progress.ticker = time.NewTicker(200 * time.Millisecond)
	progress.waitGroup.Add(1)
	go progress.renderLoop()

	return progress
}

func (p *Progress) NewProxyReader(label string, total int64, reader io.ReadCloser) *ProxyReader {
	if p == nil {
		return &ProxyReader{ReadCloser: reader}
	}

	entryID := p.addEntry(label, total)
	return &ProxyReader{
		ReadCloser: reader,
		progress:   p,
		entryID:    entryID,
	}
}

func (r *ProxyReader) Read(buffer []byte) (int, error) {
	readSize, err := r.ReadCloser.Read(buffer)
	if readSize > 0 && r.progress != nil {
		r.progress.increment(r.entryID, int64(readSize))
	}

	return readSize, err
}

func (r *ProxyReader) Abort() {
	if r == nil || r.progress == nil {
		return
	}

	r.progress.remove(r.entryID)
}

func (r *ProxyReader) Wait() {}

func (p *Progress) Complete(label string, total int64) {
	if p == nil {
		return
	}

	p.removeAndLog(label, total)
}

func (p *Progress) HasActive() bool {
	if p == nil {
		return false
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	return len(p.entries) > 0
}

func (p *Progress) Wait() {
	if p == nil {
		return
	}

	if p.enabled {
		close(p.done)
		p.waitGroup.Wait()
	}
}

func (p *Progress) addEntry(label string, total int64) int {
	if !p.enabled {
		return 0
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.nextID++
	p.entries = append(p.entries, &progressEntry{
		id:        p.nextID,
		label:     label,
		total:     total,
		startedAt: time.Now(),
	})

	return p.nextID
}

func (p *Progress) increment(entryID int, delta int64) {
	if !p.enabled {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, entry := range p.entries {
		if entry.id == entryID {
			entry.current += delta
			if entry.current > entry.total {
				entry.current = entry.total
			}
			return
		}
	}
}

func (p *Progress) remove(entryID int) {
	if !p.enabled {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.entries = removeEntryByID(p.entries, entryID)
}

func (p *Progress) removeAndLog(label string, total int64) {
	if !p.enabled {
		_, _ = fmt.Fprintf(p.output, "%s complete %s\n", label, formatBytes(total))
		return
	}

	p.mutex.Lock()
	line := fmt.Sprintf("%s complete %s", label, formatBytes(total))
	p.clearRenderedLocked()
	_, _ = fmt.Fprintln(p.output, line)
	p.renderedLines = 0
	p.entries = removeEntryByLabel(p.entries, label)
	lines := p.renderLinesLocked()
	if len(lines) > 0 {
		_, _ = fmt.Fprint(p.output, strings.Join(lines, "\n")+"\n")
		p.renderedLines = len(lines)
	}
	p.mutex.Unlock()
}

func (p *Progress) renderLoop() {
	defer p.waitGroup.Done()
	defer p.ticker.Stop()

	for {
		select {
		case <-p.ticker.C:
			p.render()
		case <-p.done:
			p.finish()
			return
		}
	}
}

func (p *Progress) render() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.clearRenderedLocked()
	lines := p.renderLinesLocked()
	if len(lines) == 0 {
		p.renderedLines = 0
		return
	}

	_, _ = fmt.Fprint(p.output, strings.Join(lines, "\n")+"\n")
	p.renderedLines = len(lines)
}

func (p *Progress) finish() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.clearRenderedLocked()
	p.renderedLines = 0
}

func (p *Progress) clearRenderedLocked() {
	for index := 0; index < p.renderedLines; index++ {
		_, _ = fmt.Fprint(p.output, "\033[1A\r\033[2K")
	}
}

func (p *Progress) renderLinesLocked() []string {
	terminalWidth := detectTerminalWidth(p.output)
	lines := make([]string, 0, len(p.entries))
	for _, entry := range p.entries {
		lines = append(lines, formatProgressLine(entry, terminalWidth))
	}
	return lines
}

func detectTerminalWidth(output io.Writer) int {
	file, ok := output.(*os.File)
	if !ok {
		return 120
	}

	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 120
	}

	return width
}
