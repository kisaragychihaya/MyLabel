//go:build !windows

// The stand-in for the Windows binding (sam2.go): the same API, with the
// native library — a Windows DLL — permanently absent. Detect reports false
// and every operation returns an error, which is how the app hides the AI
// assist where the DLL cannot run (docs/phase7-design.md §1, §4).

package sam2

import "errors"

// errUnavailable is what every operation reports off Windows.
var errUnavailable = errors.New("sam2: unavailable on this platform")

// Embedding is one encoder output on the Windows client. Nothing produces one
// here, and the zero Embedding is valid, so the app's cache field compiles
// unchanged on every platform.
type Embedding struct{}

// Release frees the native embedding. There is none to free here, so it is a
// no-op; like the Windows one it may be called repeatedly and on the zero
// Embedding.
func (e *Embedding) Release() {}

// Client mirrors the Windows client: New never fails here, every operation
// reports that the AI assist is unavailable on this platform.
type Client struct{ cfg Config }

// New returns the stub client for cfg.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

// Detect always reports false: the DLL it would look for is a Windows library,
// so the feature stays hidden on every other platform.
func Detect(exeDir string) (Config, bool) { return Config{}, false }

// Available reports that the native library cannot be used here.
func (c *Client) Available() error { return errUnavailable }

// Encode reports that the native library cannot be used here.
func (c *Client) Encode(rgb []byte, w, h, stride int) (Embedding, error) {
	return Embedding{}, errUnavailable
}

// Decode reports that the native library cannot be used here.
func (c *Client) Decode(e Embedding, pts []float32, labels []int32) (Result, error) {
	return Result{}, errUnavailable
}

// Close is a no-op: nothing was ever loaded. It is idempotent, as the Windows
// one is.
func (c *Client) Close() {}
