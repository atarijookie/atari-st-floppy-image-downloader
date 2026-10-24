package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const requestTimeout = 5 * time.Minute // one archive / the list

// networkError - couldn't talk to the source, or it had trouble (5xx, 429) - may work later
type networkError struct{ err error }

func (e *networkError) Error() string { return e.err.Error() }
func (e *networkError) Unwrap() error { return e.err }

// badFileError - the source answered, but the file is wrong or missing (404, bad hash, not a zip, ...)
type badFileError struct{ msg string }

func (e *badFileError) Error() string { return e.msg }

func badFile(format string, a ...interface{}) error { return &badFileError{fmt.Sprintf(format, a...)} }

type fetcher struct {
	client    *http.Client
	userAgent string
}

func newFetcher() *fetcher {
	return &fetcher{
		client:    &http.Client{},
		userAgent: "atari-st-floppy-image-downloader/" + version + " (+https://github.com/atarijookie/atari-st-floppy-image-downloader)",
	}
}

// open - reader of what's at rawURL: http(s)://, file:// or a local path; at most maxSize bytes. Also its size, if
// known (-1 if not).
func (f *fetcher) open(ctx context.Context, rawURL string, maxSize int64) (io.ReadCloser, int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || len(u.Scheme) == 1 { // no scheme, or a Windows drive letter - a local file
		return openLocal(rawURL)
	}
	if u.Scheme == "file" {
		return openLocal(localPath(u))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, ctx.Err()
		}
		return nil, -1, &networkError{err}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err := fmt.Errorf("HTTP %s", resp.Status)
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return nil, -1, &networkError{err}
		}
		return nil, -1, &badFileError{err.Error()}
	}
	if resp.ContentLength > maxSize {
		resp.Body.Close()
		return nil, -1, badFile("too big (%d MB)", resp.ContentLength>>20)
	}
	return resp.Body, resp.ContentLength, nil
}

func openLocal(path string) (io.ReadCloser, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, -1, badFile("not found: %s", path)
		}
		return nil, -1, err
	}
	size := int64(-1)
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	return f, size, nil
}

// progressWriter counts the written bytes for a progress callback
type progressWriter struct {
	w     io.Writer
	done  int64
	total int64
	cb    func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.cb != nil {
		p.cb(p.done, p.total)
	}
	return n, err
}

// localPath of a file:// URL - file:///C:/x on Windows is C:/x
func localPath(u *url.URL) string {
	p := u.Path
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// toFile downloads rawURL into file, at most maxSize bytes; progress (may be nil) gets the bytes so far and the size
// (-1 if not known)
func (f *fetcher) toFile(ctx context.Context, rawURL, file string, maxSize int64, progress func(done, total int64)) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	body, size, err := f.open(ctx, rawURL, maxSize)
	if err != nil {
		return err
	}
	defer body.Close()

	out, err := os.Create(file)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(&progressWriter{w: out, total: size, cb: progress}, io.LimitReader(body, maxSize+1))
	closeErr := out.Close()

	switch {
	case copyErr != nil && ctx.Err() != nil:
		return ctx.Err()
	case copyErr != nil:
		return &networkError{copyErr}
	case closeErr != nil:
		return closeErr
	case n > maxSize:
		return badFile("bigger than %d MB", maxSize>>20)
	}
	return nil
}

// bytes of what's at rawURL
func (f *fetcher) bytes(ctx context.Context, rawURL string, maxSize int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	body, _, err := f.open(ctx, rawURL, maxSize)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, maxSize+1))
	if err != nil {
		return nil, &networkError{err}
	}
	if int64(len(data)) > maxSize {
		return nil, badFile("bigger than %d MB", maxSize>>20)
	}
	return data, nil
}

// isURL - not a local path
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "file://")
}
