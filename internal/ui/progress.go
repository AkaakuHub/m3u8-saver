package ui

import (
	"io"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

type Progress struct {
	pool *mpb.Progress
}

func NewProgress(output io.Writer) *Progress {
	return &Progress{
		pool: mpb.New(
			mpb.WithOutput(output),
			mpb.WithRefreshRate(180*time.Millisecond),
		),
	}
}

func (p *Progress) NewProxyReader(label string, total int64, reader io.ReadCloser) io.ReadCloser {
	if p == nil {
		return reader
	}

	bar := p.pool.AddBar(
		total,
		mpb.PrependDecorators(
			decor.Name(label+" "),
			decor.CountersKibiByte("% .1f / % .1f"),
		),
		mpb.AppendDecorators(
			decor.Percentage(),
			decor.Name(" "),
			decor.EwmaETA(decor.ET_STYLE_GO, 60),
		),
	)

	return bar.ProxyReader(reader)
}

func (p *Progress) Wait() {
	if p == nil {
		return
	}

	p.pool.Wait()
}
