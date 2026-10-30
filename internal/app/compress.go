package app

import (
	"bufio"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMinBytes skips compression for responses known to be smaller than this;
// gzip overhead outweighs the saving on tiny bodies.
const gzipMinBytes = 1024

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return w
}}

// compressibleType reports whether a Content-Type is worth gzipping.
func compressibleType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/json", ct == "application/javascript", ct == "image/svg+xml":
		return true
	}
	return false
}

// withGzip compresses text responses for clients that accept gzip.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	if compressibleType(h.Get("Content-Type")) {
		h.Add("Vary", "Accept-Encoding")
	}
	if g.shouldCompress(code) {
		h.Del("Content-Length")
		h.Del("Accept-Ranges") // byte ranges would refer to the uncompressed body
		h.Set("Content-Encoding", "gzip")
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) shouldCompress(code int) bool {
	h := g.Header()
	if code < 200 || code == http.StatusNoContent || code == http.StatusPartialContent || code == http.StatusNotModified {
		return false
	}
	if h.Get("Content-Encoding") != "" || !compressibleType(h.Get("Content-Type")) {
		return false
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < gzipMinBytes {
		return false
	}
	return true
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.wroteHeader {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(p))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := g.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijacking not supported")
}

func (g *gzipResponseWriter) finish() {
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}
