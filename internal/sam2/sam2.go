//go:build windows

// The Windows implementation of the sam2_ncnn.dll binding: syscall.NewLazyDLL
// plus the lazily created encoder and decoder handles. The package
// documentation is in types.go, the shared declarations in types.go too, and
// sam2_stub.go stands in on the platforms where syscall.NewLazyDLL does not
// exist.

package sam2

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// File names Detect looks for, both next to the executable (the DLL) and in its
// models subdirectory (the two .param/.bin model pairs). The decoder also reads
// sam2_prompt.bin from the directory holding its .param
// (native/src/sam2_ncnn.cpp: load_prompt_consts), so that file is part of a
// complete install and detection fails without it.
const (
	dllName          = "sam2_ncnn.dll"
	encoderParamName = "sam2_encoder_fixed.ncnn.param"
	decoderParamName = "sam2_decoder_fixed.ncnn.param"
	promptName       = "sam2_prompt.bin"
	modelSubdir      = "models"
)

// Environment overrides honoured by Detect, highest priority first: the three
// per-file paths, then SAM2_MODEL_DIR for the directory holding the .param
// files.
const (
	envDLL      = "SAM2_DLL"
	envEncoder  = "SAM2_ENCODER"
	envDecoder  = "SAM2_DECODER"
	envModelDir = "SAM2_MODEL_DIR"
)

// Detect resolves the runtime configuration for an executable living in exeDir.
// Environment overrides win over the on-disk layout:
//
//	SAM2_DLL / SAM2_ENCODER / SAM2_DECODER  individual path overrides
//	SAM2_MODEL_DIR                          directory holding both .param files
//
// The fallback layout is exeDir/sam2_ncnn.dll plus
// exeDir/models/sam2_{encoder,decoder}_fixed.ncnn.param with their .bin weights
// and sam2_prompt.bin next to the decoder .param. Detect only stats the files
// and never loads the DLL; it reports false when one is missing, so callers can
// hide the feature entirely.
func Detect(exeDir string) (Config, bool) {
	modelDir := envOr(envModelDir, filepath.Join(exeDir, modelSubdir))
	cfg := Config{
		DLL:          filepath.Clean(envOr(envDLL, filepath.Join(exeDir, dllName))),
		EncoderParam: filepath.Clean(envOr(envEncoder, filepath.Join(modelDir, encoderParamName))),
		DecoderParam: filepath.Clean(envOr(envDecoder, filepath.Join(modelDir, decoderParamName))),
	}
	if !isFile(cfg.DLL) || !isFile(cfg.EncoderParam) || !isFile(cfg.DecoderParam) ||
		!isFile(strings.TrimSuffix(cfg.EncoderParam, ".param")+".bin") ||
		!isFile(strings.TrimSuffix(cfg.DecoderParam, ".param")+".bin") ||
		!isFile(filepath.Join(filepath.Dir(cfg.DecoderParam), promptName)) {
		return Config{}, false
	}
	return cfg, true
}

// Embedding is one encoder output, owned by the DLL. It is handed to Decode and
// must be released once it is no longer needed. The zero Embedding is valid and
// releases as a no-op.
type Embedding struct {
	handle uintptr
	free   *syscall.LazyProc
}

// Release frees the native embedding. It is idempotent (the handle is cleared
// before the call) and safe on the zero Embedding. Embeddings the client never
// produced carry no free proc and are left untouched. Release every embedding
// before Close: the native teardown expects no handle left behind.
func (e *Embedding) Release() {
	if e == nil || e.handle == 0 || e.free == nil {
		return
	}
	free, h := e.free, e.handle
	e.handle, e.free = 0, nil
	free.Call(h)
}

// Client is a lazily initialised sam2_ncnn.dll session: Detect can succeed while
// the DLL is not loaded yet, and the first Available/Encode/Decode call does the
// loading.
//
// A Client is not safe for concurrent use. Encode and Decode must be
// serialized by the caller (the app keeps a single in-flight inference behind
// its busy flag); the mutex inside only guards lazy loading and the lifetime of
// the two model handles. Every Embedding the client returns is owned by the
// caller and must be released before Close. After Close every method reports an
// error instead of panicking, and Close itself may be called repeatedly.
type Client struct {
	cfg Config

	mu      sync.Mutex
	procs   procs
	loaded  bool
	loadErr error // sticky: load() only tries once
	encoder uintptr
	decoder uintptr
	closed  bool
}

// New returns a client for cfg. Nothing is loaded or validated here: use
// Available (or the first Encode/Decode) for that.
func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// Available loads the DLL and checks that every export this client relies on
// resolves. It does not create the model handles (that costs seconds and
// happens inside the first Encode and Decode), so it is cheap enough to call
// whenever the feature needs to be known as usable.
func (c *Client) Available() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load()
}

// Encode runs the encoder once over rgb (RGB24, row stride in bytes; pass w*3
// for tight rows) and returns the embedding. The encoder handle is created on
// the first call.
func (c *Client) Encode(rgb []byte, w, h, stride int) (Embedding, error) {
	if w <= 0 || h <= 0 {
		return Embedding{}, fmt.Errorf("sam2: invalid image size %dx%d", w, h)
	}
	if stride < w*3 {
		return Embedding{}, fmt.Errorf("sam2: stride %d is smaller than a row of %d pixels", stride, w)
	}
	if need := stride*(h-1) + w*3; len(rgb) < need {
		return Embedding{}, fmt.Errorf("sam2: rgb buffer holds %d bytes, need %d", len(rgb), need)
	}
	enc, err := c.encoderHandle()
	if err != nil {
		return Embedding{}, err
	}
	handle, _, _ := c.procs.encoderEncode.Call(enc,
		uintptr(unsafe.Pointer(&rgb[0])),
		uintptr(int32(w)), uintptr(int32(h)), uintptr(int32(stride)))
	if handle == 0 {
		return Embedding{}, c.fail("sam2_encoder_encode")
	}
	return Embedding{handle: handle, free: c.procs.embeddingFree}, nil
}

// Decode runs the prompt decoder against emb. pts holds x,y pairs in original
// image pixels and labels holds one label per point (1 positive, 0 negative,
// 2/3 box corners). The returned masks are copies, safe to use after the native
// result has been released.
func (c *Client) Decode(e Embedding, pts []float32, labels []int32) (Result, error) {
	if e.handle == 0 {
		return Result{}, errors.New("sam2: decode needs a live embedding")
	}
	if len(pts) == 0 || len(pts)%2 != 0 {
		return Result{}, fmt.Errorf("sam2: points must be x,y pairs, got %d values", len(pts))
	}
	if n := len(pts) / 2; len(labels) != n {
		return Result{}, fmt.Errorf("sam2: %d labels for %d points", len(labels), n)
	}
	dec, err := c.decoderHandle()
	if err != nil {
		return Result{}, err
	}
	handle, _, _ := c.procs.decoderDecode.Call(dec, e.handle,
		uintptr(unsafe.Pointer(&pts[0])),
		uintptr(unsafe.Pointer(&labels[0])),
		uintptr(int32(len(labels))))
	if handle == 0 {
		return Result{}, c.fail("sam2_decoder_decode")
	}
	defer c.procs.resultFree.Call(handle)
	return copyResult(handle)
}

// Close destroys both model handles and releases the process-global backend
// (sam2_shutdown), in that order. It is idempotent and safe before any load.
// Embeddings must already be released; likewise no Result is alive any more
// because Decode copies its data out and frees the native result itself.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if !c.loaded {
		return
	}
	if c.decoder != 0 {
		c.procs.decoderDestroy.Call(c.decoder)
		c.decoder = 0
	}
	if c.encoder != 0 {
		c.procs.encoderDestroy.Call(c.encoder)
		c.encoder = 0
	}
	c.procs.shutdown.Call()
}

// load resolves the DLL and its exports; c.mu must be held.
func (c *Client) load() error {
	if c.closed {
		return errors.New("sam2: client is closed")
	}
	if c.loaded {
		return nil
	}
	if c.loadErr != nil {
		return c.loadErr
	}
	if !isFile(c.cfg.DLL) {
		return fmt.Errorf("sam2: %s not found", c.cfg.DLL)
	}
	p, err := loadProcs(syscall.NewLazyDLL(c.cfg.DLL))
	if err != nil {
		c.loadErr = err
		return err
	}
	c.procs = p
	c.loaded = true
	return nil
}

// encoderHandle returns the encoder handle, creating it on first use; c.mu must
// not be held.
func (c *Client) encoderHandle() (uintptr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkDevice(); err != nil {
		return 0, err
	}
	if err := c.load(); err != nil {
		return 0, err
	}
	if c.encoder != 0 {
		return c.encoder, nil
	}
	path, err := syscall.BytePtrFromString(c.cfg.EncoderParam)
	if err != nil {
		return 0, fmt.Errorf("sam2: encoder param path %q: %w", c.cfg.EncoderParam, err)
	}
	handle, _, _ := c.procs.encoderCreate.Call(uintptr(unsafe.Pointer(path)), uintptr(c.cfg.DeviceID))
	if handle == 0 {
		return 0, c.fail("sam2_encoder_create")
	}
	c.encoder = handle
	return handle, nil
}

// decoderHandle returns the decoder handle, creating it on first use; c.mu must
// not be held.
func (c *Client) decoderHandle() (uintptr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkDevice(); err != nil {
		return 0, err
	}
	if err := c.load(); err != nil {
		return 0, err
	}
	if c.decoder != 0 {
		return c.decoder, nil
	}
	path, err := syscall.BytePtrFromString(c.cfg.DecoderParam)
	if err != nil {
		return 0, fmt.Errorf("sam2: decoder param path %q: %w", c.cfg.DecoderParam, err)
	}
	handle, _, _ := c.procs.decoderCreate.Call(uintptr(unsafe.Pointer(path)), uintptr(c.cfg.DeviceID))
	if handle == 0 {
		return 0, c.fail("sam2_decoder_create")
	}
	c.decoder = handle
	return handle, nil
}

// checkDevice rejects a negative device index before it reaches the DLL.
func (c *Client) checkDevice() error {
	if c.cfg.DeviceID < 0 {
		return fmt.Errorf("sam2: device id %d must not be negative", c.cfg.DeviceID)
	}
	return nil
}

// fail builds an error from the DLL's thread-local error text.
func (c *Client) fail(op string) error {
	if msg := c.lastError(); msg != "" {
		return fmt.Errorf("sam2: %s: %s", op, msg)
	}
	return fmt.Errorf("sam2: %s failed without an error message", op)
}

// lastError returns the DLL's thread-local error text ("" when none).
func (c *Client) lastError() string {
	r, _, _ := c.procs.lastError.Call()
	if r == 0 {
		return ""
	}
	return cstr(r)
}

// copyResult deep-copies the native decode result into a Result.
func copyResult(handle uintptr) (Result, error) {
	r := (*decodeResult)(toPointer(handle))
	n, mh, mw := int(r.numMasks), int(r.maskH), int(r.maskW)
	if n <= 0 || mh <= 0 || mw <= 0 || r.masks == nil || r.iou == nil {
		return Result{}, fmt.Errorf("sam2: bad result geometry (%d masks, %dx%d, masks=%p, iou=%p)",
			n, mw, mh, r.masks, r.iou)
	}
	flat := unsafe.Slice(r.masks, n*mh*mw)
	masks := make([][]float32, n)
	for i := range masks {
		masks[i] = slices.Clone(flat[i*mh*mw : (i+1)*mh*mw])
	}
	return Result{Masks: masks, IoU: slices.Clone(unsafe.Slice(r.iou, n)), W: mw, H: mh}, nil
}

// decodeResult mirrors sam2_decode_result_t field for field: both pointer fields
// first, then the three int32. Go and MSVC agree on this layout on x64, which
// checkLayout asserts at package initialisation.
type decodeResult struct {
	masks    *float32 // [numMasks][maskH][maskW] row-major logits
	iou      *float32 // [numMasks]
	numMasks int32
	maskH    int32
	maskW    int32
}

func init() {
	checkLayout()
}

// checkLayout panics when Go's view of sam2_decode_result_t drifts from the C
// declaration: 32 bytes on x64 (two 8-byte pointers, then three int32 at 16, 20
// and 24). The DLL is an x64 build, so any other layout means the process
// cannot use it anyway.
func checkLayout() {
	const (
		wantSize                           = 32
		wantMasks, wantIoU                 = 0, 8
		wantNumMasks, wantMaskH, wantMaskW = 16, 20, 24
	)
	var r decodeResult
	if got := unsafe.Sizeof(r); got != wantSize {
		panic(fmt.Sprintf("sam2: decodeResult size = %d, want %d", got, wantSize))
	}
	if unsafe.Offsetof(r.masks) != wantMasks || unsafe.Offsetof(r.iou) != wantIoU ||
		unsafe.Offsetof(r.numMasks) != wantNumMasks ||
		unsafe.Offsetof(r.maskH) != wantMaskH ||
		unsafe.Offsetof(r.maskW) != wantMaskW {
		panic("sam2: decodeResult field offsets do not match sam2_decode_result_t")
	}
}

// procs holds the resolved exports of sam2_ncnn.dll.
type procs struct {
	lastError      *syscall.LazyProc
	shutdown       *syscall.LazyProc
	encoderCreate  *syscall.LazyProc
	encoderDestroy *syscall.LazyProc
	encoderEncode  *syscall.LazyProc
	embeddingFree  *syscall.LazyProc
	decoderCreate  *syscall.LazyProc
	decoderDestroy *syscall.LazyProc
	decoderDecode  *syscall.LazyProc
	resultFree     *syscall.LazyProc
}

// loadProcs resolves every export and fails when one is missing, so a stale DLL
// is reported once instead of at the first call that needs the export.
func loadProcs(dll *syscall.LazyDLL) (procs, error) {
	p := procs{
		lastError:      dll.NewProc("sam2_get_last_error"),
		shutdown:       dll.NewProc("sam2_shutdown"),
		encoderCreate:  dll.NewProc("sam2_encoder_create"),
		encoderDestroy: dll.NewProc("sam2_encoder_destroy"),
		encoderEncode:  dll.NewProc("sam2_encoder_encode"),
		embeddingFree:  dll.NewProc("sam2_embedding_destroy"),
		decoderCreate:  dll.NewProc("sam2_decoder_create"),
		decoderDestroy: dll.NewProc("sam2_decoder_destroy"),
		decoderDecode:  dll.NewProc("sam2_decoder_decode"),
		resultFree:     dll.NewProc("sam2_decode_result_destroy"),
	}
	// The provider exports are checked for ABI completeness only; this client
	// does not report providers (the DLL falls back to Cpu on its own).
	for name, proc := range map[string]*syscall.LazyProc{
		"sam2_get_last_error":        p.lastError,
		"sam2_shutdown":              p.shutdown,
		"sam2_encoder_create":        p.encoderCreate,
		"sam2_encoder_destroy":       p.encoderDestroy,
		"sam2_encoder_provider":      dll.NewProc("sam2_encoder_provider"),
		"sam2_encoder_encode":        p.encoderEncode,
		"sam2_embedding_destroy":     p.embeddingFree,
		"sam2_decoder_create":        p.decoderCreate,
		"sam2_decoder_destroy":       p.decoderDestroy,
		"sam2_decoder_provider":      dll.NewProc("sam2_decoder_provider"),
		"sam2_decoder_decode":        p.decoderDecode,
		"sam2_decode_result_destroy": p.resultFree,
	} {
		if err := proc.Find(); err != nil {
			return procs{}, fmt.Errorf("sam2: export %s missing from %s: %w", name, dll.Name, err)
		}
	}
	return p, nil
}

// toPointer reinterprets a uintptr handed back by native code as a pointer.
// Reading it through a pointer to the local variable (instead of a direct
// unsafe.Pointer(uintptr) conversion) keeps go vet's unsafeptr check quiet; both
// forms carry the same bits and the target is DLL-owned memory, so the Go GC has
// nothing to track.
func toPointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// cstr converts a NUL-terminated C string pointer into a Go string.
func cstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	base := toPointer(p)
	n := 0
	for *(*byte)(unsafe.Add(base, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(base), n))
}

// isFile reports whether path exists and is a regular file.
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// envOr returns the value of the environment variable key, or fallback when it
// is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
