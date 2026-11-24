//go:build windows

package sam2

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// writeStub creates an empty file that only has to exist for the detection
// probes, which never open or load it.
func writeStub(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
}

// stubInstall writes every file Detect looks for under a standard layout rooted
// at dir (DLL next to the executable, models in dir/models, sam2_prompt.bin
// next to the decoder .param).
func stubInstall(t *testing.T, dir string) Config {
	t.Helper()
	writeStub(t, filepath.Join(dir, dllName))
	writeStub(t, filepath.Join(dir, modelSubdir, encoderParamName))
	writeStub(t, filepath.Join(dir, modelSubdir, strings.TrimSuffix(encoderParamName, ".param")+".bin"))
	writeStub(t, filepath.Join(dir, modelSubdir, decoderParamName))
	writeStub(t, filepath.Join(dir, modelSubdir, strings.TrimSuffix(decoderParamName, ".param")+".bin"))
	writeStub(t, filepath.Join(dir, modelSubdir, promptName))
	return Config{
		DLL:          filepath.Join(dir, dllName),
		EncoderParam: filepath.Join(dir, modelSubdir, encoderParamName),
		DecoderParam: filepath.Join(dir, modelSubdir, decoderParamName),
	}
}

func TestDetectMissingInstall(t *testing.T) {
	dir := t.TempDir()
	if cfg, ok := Detect(dir); ok {
		t.Fatalf("Detect(%q) = %+v, true; want false", dir, cfg)
	}

	// Every required file missing in turn must fail detection.
	stubInstall(t, dir)
	if _, ok := Detect(dir); !ok {
		t.Fatalf("Detect(%q) = false after a complete install", dir)
	}
	for _, drop := range []string{
		filepath.Join(dir, modelSubdir, promptName),
		filepath.Join(dir, modelSubdir, strings.TrimSuffix(decoderParamName, ".param")+".bin"),
		filepath.Join(dir, modelSubdir, strings.TrimSuffix(encoderParamName, ".param")+".bin"),
		filepath.Join(dir, modelSubdir, decoderParamName),
		filepath.Join(dir, modelSubdir, encoderParamName),
		filepath.Join(dir, dllName),
	} {
		if err := os.Remove(drop); err != nil {
			t.Fatalf("remove %s: %v", drop, err)
		}
		if _, ok := Detect(dir); ok {
			t.Fatalf("Detect(%q) = true without %s", dir, filepath.Base(drop))
		}
		writeStub(t, drop)
	}
}

func TestDetectStandardLayout(t *testing.T) {
	dir := t.TempDir()
	want := stubInstall(t, dir)
	got, ok := Detect(dir)
	if !ok {
		t.Fatalf("Detect(%q) = false, want true", dir)
	}
	if got != (Config{DLL: want.DLL, EncoderParam: want.EncoderParam, DecoderParam: want.DecoderParam, DeviceID: 0}) {
		t.Fatalf("Detect(%q) = %+v, want %+v", dir, got, want)
	}
}

func TestDetectEnvOverrides(t *testing.T) {
	exeDir := t.TempDir() // deliberately empty: everything must come from the env
	other := t.TempDir()
	dll := filepath.Join(other, "custom.dll")
	enc := filepath.Join(other, "enc.param")
	dec := filepath.Join(other, "dec.param")
	modelDir := filepath.Join(other, "weights")
	writeStub(t, dll)
	writeStub(t, enc)
	writeStub(t, dec)
	writeStub(t, filepath.Join(modelDir, encoderParamName))
	writeStub(t, filepath.Join(modelDir, decoderParamName))

	t.Run("per file paths", func(t *testing.T) {
		t.Setenv(envDLL, dll)
		t.Setenv(envEncoder, enc)
		t.Setenv(envDecoder, dec)
		t.Setenv(envModelDir, "")
		writeStub(t, strings.TrimSuffix(enc, ".param")+".bin")
		writeStub(t, strings.TrimSuffix(dec, ".param")+".bin")
		writeStub(t, filepath.Join(other, promptName))
		got, ok := Detect(exeDir)
		if !ok {
			t.Fatal("Detect = false, want true")
		}
		if got.DLL != dll || got.EncoderParam != enc || got.DecoderParam != dec {
			t.Fatalf("Detect = %+v, want the three env paths", got)
		}
	})

	t.Run("model dir only", func(t *testing.T) {
		t.Setenv(envDLL, dll)
		t.Setenv(envEncoder, "")
		t.Setenv(envDecoder, "")
		t.Setenv(envModelDir, modelDir)
		writeStub(t, filepath.Join(modelDir, strings.TrimSuffix(encoderParamName, ".param")+".bin"))
		writeStub(t, filepath.Join(modelDir, strings.TrimSuffix(decoderParamName, ".param")+".bin"))
		writeStub(t, filepath.Join(modelDir, promptName))
		got, ok := Detect(exeDir)
		if !ok {
			t.Fatal("Detect = false, want true")
		}
		if got.EncoderParam != filepath.Join(modelDir, encoderParamName) ||
			got.DecoderParam != filepath.Join(modelDir, decoderParamName) {
			t.Fatalf("Detect = %+v, want the .param files from %s", got, modelDir)
		}
	})

	t.Run("model dir missing one file", func(t *testing.T) {
		thin := t.TempDir()
		writeStub(t, filepath.Join(thin, encoderParamName))
		t.Setenv(envDLL, dll)
		t.Setenv(envEncoder, "")
		t.Setenv(envDecoder, "")
		t.Setenv(envModelDir, thin)
		if _, ok := Detect(exeDir); ok {
			t.Fatalf("Detect = true with only one .param in %s", thin)
		}
	})
}

func TestAvailableWithoutDLL(t *testing.T) {
	dir := t.TempDir()
	c := New(Config{
		DLL:          filepath.Join(dir, "missing.dll"),
		EncoderParam: filepath.Join(dir, "missing-enc.param"),
		DecoderParam: filepath.Join(dir, "missing-dec.param"),
	})
	defer c.Close()

	if err := c.Available(); err == nil {
		t.Fatal("Available = nil without a DLL, want an error")
	}
	if _, err := c.Encode(make([]byte, 8*8*3), 8, 8, 8*3); err == nil {
		t.Fatal("Encode = nil without a DLL, want an error")
	}
	if _, err := c.Decode(Embedding{handle: 1}, []float32{1, 2}, []int32{1}); err == nil {
		t.Fatal("Decode = nil without a DLL, want an error")
	}
}

func TestArgumentValidation(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	if _, err := c.Encode(nil, 0, 4, 0); err == nil {
		t.Fatal("Encode accepted a zero width")
	}
	if _, err := c.Encode(make([]byte, 4), 8, 8, 24); err == nil {
		t.Fatal("Encode accepted a short buffer")
	}
	if _, err := c.Decode(Embedding{}, []float32{1, 2}, []int32{1}); err == nil {
		t.Fatal("Decode accepted the zero embedding")
	}
	if _, err := c.Decode(Embedding{handle: 1}, []float32{1}, []int32{1}); err == nil {
		t.Fatal("Decode accepted a dangling x coordinate")
	}
	if _, err := c.Decode(Embedding{handle: 1}, []float32{1, 2}, []int32{1, 1}); err == nil {
		t.Fatal("Decode accepted a label count mismatch")
	}

	c.cfg.DeviceID = -1
	if err := c.checkDevice(); err == nil {
		t.Fatal("checkDevice accepted a negative device id")
	}
}

func TestCloseIsIdempotentAndFinal(t *testing.T) {
	c := New(Config{DLL: "does-not-matter.dll"})
	c.Close()
	c.Close() // must not panic

	if err := c.Available(); err == nil {
		t.Fatal("Available = nil after Close, want an error")
	}
	if _, err := c.Encode(make([]byte, 4*4*3), 4, 4, 12); err == nil {
		t.Fatal("Encode = nil after Close, want an error")
	}
	if _, err := c.Decode(Embedding{handle: 1}, []float32{1, 2}, []int32{1}); err == nil {
		t.Fatal("Decode = nil after Close, want an error")
	}
}

func TestEmbeddingRelease(t *testing.T) {
	var zero Embedding
	zero.Release()
	zero.Release()

	var nilEmbedding *Embedding
	nilEmbedding.Release()

	// An Embedding the client never produced (no free proc) has nothing to
	// release; Release must not touch it or call into the DLL.
	e := Embedding{handle: 42}
	e.Release()
	if e.handle != 42 || e.free != nil {
		t.Fatalf("Release modified a free-less embedding: %+v", e)
	}
}

func TestResultBest(t *testing.T) {
	cases := []struct {
		name string
		iou  []float32
		want int
	}{
		{"empty", nil, -1},
		{"single", []float32{0.5}, 0},
		{"highest in the middle", []float32{0.1, 0.9, 0.5}, 1},
		{"tie keeps the first", []float32{0.7, 0.7}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Result{IoU: tc.iou}).Best(); got != tc.want {
				t.Fatalf("Best = %d, want %d", got, tc.want)
			}
		})
	}
}

// smokeConfigs lists the installations the optional end-to-end test may use: a
// Detect-able layout of each candidate directory first, then the loose staging
// layout of libsam2_ncnn where the DLL and both .param files share one
// directory. Candidates come from the environment, then from the workspace
// staging directory three levels up from this package (autosetup/ncnn_run).
func smokeConfigs() []Config {
	var dirs []string
	if d := os.Getenv(envModelDir); d != "" {
		dirs = append(dirs, d)
	}
	if p := os.Getenv(envDLL); p != "" {
		dirs = append(dirs, filepath.Dir(p))
	}
	if abs, err := filepath.Abs(filepath.Join("..", "..", "..", "ncnn_run")); err == nil {
		dirs = append(dirs, abs)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "testdata"))
	}

	var out []Config
	seen := map[string]bool{}
	add := func(cfg Config) {
		if seen[cfg.DLL] {
			return
		}
		seen[cfg.DLL] = true
		out = append(out, cfg)
	}
	for _, dir := range dirs {
		if cfg, ok := Detect(dir); ok {
			add(cfg)
		}
		add(Config{
			DLL:          filepath.Join(dir, dllName),
			EncoderParam: filepath.Join(dir, encoderParamName),
			DecoderParam: filepath.Join(dir, decoderParamName),
		})
	}
	return out
}

// abiComplete reports whether the DLL at path exposes every export this client
// binds. Loading it here is harmless: no model handle is created.
func abiComplete(path string) error {
	_, err := loadProcs(syscall.NewLazyDLL(path))
	return err
}

// syntheticRGB builds a deterministic 640x480 RGB24 frame: a two-axis gradient
// background with a warm disk around the prompt point.
func syntheticRGB(w, h int) []byte {
	const cx, cy, r = 320, 240, 140
	pix := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rr := byte(20 + 50*x/(w-1))
			g := byte(30 + 70*y/(h-1))
			b := byte(110 + 60*(x+y)/(w+h-2))
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy <= r*r {
				rr, g, b = 232, 64, 48
			}
			i := (y*w + x) * 3
			pix[i], pix[i+1], pix[i+2] = rr, g, b
		}
	}
	return pix
}

// TestEncodeDecodeSmoke is the optional end-to-end pass: it needs the real
// sam2_ncnn.dll plus both .param files and skips otherwise (no network, one
// encode call only).
func TestEncodeDecodeSmoke(t *testing.T) {
	var (
		cfg     Config
		usable  bool
		rejects []string
	)
	for _, cand := range smokeConfigs() {
		if !isFile(cand.DLL) || !isFile(cand.EncoderParam) || !isFile(cand.DecoderParam) {
			continue
		}
		if err := abiComplete(cand.DLL); err != nil {
			rejects = append(rejects, err.Error())
			continue
		}
		cfg, usable = cand, true
		break
	}
	if !usable {
		if len(rejects) > 0 {
			t.Skipf("installed DLL does not expose this client's ABI (a pre sam2_shutdown build?): %v", rejects)
		}
		t.Skip("no sam2_ncnn.dll + .param installation found; skipping the end-to-end smoke test")
	}
	t.Logf("using DLL %s", cfg.DLL)

	c := New(cfg)
	defer c.Close()
	if err := c.Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}

	const w, h = 640, 480
	emb, err := c.Encode(syntheticRGB(w, h), w, h, w*3)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	defer emb.Release()

	res, err := c.Decode(emb, []float32{w / 2, h / 2}, []int32{1})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(res.Masks) == 0 {
		t.Fatal("Decode returned no masks")
	}
	if len(res.IoU) != len(res.Masks) {
		t.Fatalf("%d masks but %d IoU scores", len(res.Masks), len(res.IoU))
	}
	if res.W <= 0 || res.H <= 0 {
		t.Fatalf("mask geometry %dx%d, want positive", res.W, res.H)
	}
	for i, m := range res.Masks {
		if len(m) != res.W*res.H {
			t.Fatalf("mask %d holds %d values, want %d", i, len(m), res.W*res.H)
		}
	}

	best := res.Best()
	mask := res.Masks[best]
	positives := 0
	for _, v := range mask {
		if v > 0 {
			positives++
		}
	}
	ratio := float64(positives) / float64(len(mask))
	t.Logf("%d masks %dx%d, best %d iou %.6f, foreground %.4f", len(res.Masks), res.W, res.H, best, res.IoU[best], ratio)
	if ratio <= 0.10 || ratio >= 0.90 {
		t.Fatalf("best mask covers %.1f%% of the frame, want a prompt-scale object (10%%..90%%)", 100*ratio)
	}

	// The data must survive the native result teardown that already happened.
	if mask[0] == 0 && mask[len(mask)-1] == 0 && positives == 0 {
		t.Fatal("copied mask looks empty after the native result was freed")
	}
}
