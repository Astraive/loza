package cortex

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// repeatByteReader emits an endless stream of a single byte without allocating.
type repeatByteReader byte

func (r repeatByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// countingReader records how many bytes the client pulls from the response body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// oversizedHealthReadyBody is one valid JSON object whose value spans well
// past maxResponseBytes, so the object is only complete once the whole body
// has been consumed. A decoder that reads the body directly must read all of
// it; a readResponseBody-based implementation stops at maxResponseBytes+1.
func oversizedHealthReadyBody() *countingReader {
	const prefix = `{"status":"ok","pad":"`
	const suffix = `"}`
	body := io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(repeatByteReader('A'), int64(2*maxResponseBytes)),
		strings.NewReader(suffix),
	)
	return &countingReader{r: body}
}

func responseWithBody(body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(body),
		Header:     make(http.Header),
	}
}

func TestHealthAndReadyCapResponseBody(t *testing.T) {
	cases := []struct {
		name string
		call func(*Client) bool
	}{
		{"health", func(c *Client) bool { return c.Health(context.Background()) }},
		{"ready", func(c *Client) bool { return c.Ready(context.Background()) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := oversizedHealthReadyBody()
			tr := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return responseWithBody(body), nil
			})
			c := NewClient("http://example.invalid").WithHTTPClient(&http.Client{Transport: tr})
			_ = tc.call(c)
			if body.n > int64(maxResponseBytes)+1 {
				t.Fatalf("read %d bytes from response body, want at most maxResponseBytes+1 (%d)", body.n, maxResponseBytes+1)
			}
		})
	}
}

func TestHealthAndReadyParseCappedBody(t *testing.T) {
	cases := []struct {
		name, payload string
		call          func(*Client) bool
		want          bool
	}{
		{"health ok", `{"status":"ok"}`, func(c *Client) bool { return c.Health(context.Background()) }, true},
		{"health degraded", `{"status":"degraded"}`, func(c *Client) bool { return c.Health(context.Background()) }, false},
		{"health non-json", `not-json`, func(c *Client) bool { return c.Health(context.Background()) }, false},
		{"ready ok", `{"status":"ok"}`, func(c *Client) bool { return c.Ready(context.Background()) }, true},
		{"ready true", `{"ready":true}`, func(c *Client) bool { return c.Ready(context.Background()) }, true},
		{"ready false", `{"ready":false}`, func(c *Client) bool { return c.Ready(context.Background()) }, false},
		{"ready empty", ``, func(c *Client) bool { return c.Ready(context.Background()) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return responseWithBody(strings.NewReader(tc.payload)), nil
			})
			c := NewClient("http://example.invalid").WithHTTPClient(&http.Client{Transport: tr})
			if got := tc.call(c); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
