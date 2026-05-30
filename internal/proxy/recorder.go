package proxy

import "time"

// MaxCapturedBody caps how many bytes of each request and response body the
// proxy buffers for the raw on-disk capture. The bytes forwarded downstream
// are NEVER capped — the tee stops accumulating into the raw buffer past the
// cap but keeps streaming every byte to the client. A multi-MB cap covers any
// realistic Anthropic request/response while keeping a single pathological
// upload from buffering gigabytes; observations past the cap set the
// corresponding *Truncated flag so a reader knows the raw file is partial.
const MaxCapturedBody = 8 << 20 // 8 MiB

// RequestObservation is the transport-level capture the proxy hands the
// recorder once a round-trip finalises. It carries the request-line fields,
// the response status (0 when the round-trip failed before any response),
// the byte counts each way (the FULL streamed sizes, even when the buffered
// raw copy was capped), the round-trip duration, the start/end timestamps,
// and the buffered raw request + response bodies (capped at MaxCapturedBody).
//
// It is deliberately generic — no Anthropic body parsing. The recorder owns
// what to do with it (write the raw bytes to disk, insert an index row); the
// proxy package knows nothing about the store or the filesystem.
type RequestObservation struct {
	Method string
	Host   string
	Path   string
	Status int

	BytesIn  int64
	BytesOut int64

	Started  time.Time
	Ended    time.Time
	Duration time.Duration

	// RawRequest / RawResponse are the buffered bodies, truncated to
	// MaxCapturedBody. The *Truncated flag is set when the corresponding
	// body exceeded the cap, so a reader of the raw file knows it's partial.
	RawRequest          []byte
	RawResponse         []byte
	RequestTruncated    bool
	ResponseTruncated   bool
	RequestHeaderBlock  string
	ResponseHeaderBlock string
}

// Recorder observes every request that flows through the proxy. The proxy
// calls Record exactly once per round-trip, on the response body's Close (or
// immediately on an upstream error, with Status 0). Implementations MUST be
// non-blocking and MUST NOT touch the streamed bytes — capture happens off
// the request path. A nil recorder is replaced with nopRecorder by New.
type Recorder interface {
	Record(RequestObservation)
}

// nopRecorder is the default when New is given a nil Recorder. It keeps the
// transport's capture path branch-free — the proxy always has a recorder to
// call, it just does nothing.
type nopRecorder struct{}

func (nopRecorder) Record(RequestObservation) {}
