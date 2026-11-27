//go:build windows

package sam2

// The download tests run against a local httptest server standing in for the
// Gitee/ModelScope downloads: the suite never touches the real network.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// stubServer serves a dist zip carrying bin/sam2_ncnn.dll and the five model
// files, counting requests per path so tests can assert what was fetched.
type stubServer struct {
	*httptest.Server
	hits map[string]int
}

func newStubServer(t *testing.T, dllEntry bool) *stubServer {
	t.Helper()
	// Freeze the manifest sizes: the size-mismatch test mutates modelSet and
	// the server must keep serving the real byte counts.
	sizes := make([]int64, len(modelSet))
	for i, mf := range modelSet {
		sizes[i] = mf.size
	}
	s := &stubServer{hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		switch {
		case r.URL.Path == "/dist.zip":
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			if dllEntry {
				fw, _ := zw.Create("bin/" + dllName)
				fw.Write(bytes.Repeat([]byte{7}, 1024))
			}
			fw, _ := zw.Create("bin/sam2qc.exe")
			fw.Write([]byte{1})
			zw.Close()
			w.Write(buf.Bytes())
		case r.URL.Path == "/model/sam2.1-small-encoder_fixed.ncnn.param":
			w.Write(make([]byte, sizes[0]))
		case r.URL.Path == "/model/sam2.1-small-encoder_fixed.ncnn.bin":
			w.Write(make([]byte, sizes[1]))
		case r.URL.Path == "/model/sam2.1-small-decoder_fixed.ncnn.param":
			w.Write(make([]byte, sizes[2]))
		case r.URL.Path == "/model/sam2.1-small-decoder_fixed.ncnn.bin":
			w.Write(make([]byte, sizes[3]))
		case r.URL.Path == "/model/sam2_prompt.bin":
			w.Write(make([]byte, sizes[4]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// swapSources points the download URLs at the stub and restores them after.
func swapSources(t *testing.T, s *stubServer) {
	t.Helper()
	oldZip, oldModel := distZipURL, modelBaseURL
	distZipURL = s.URL + "/dist.zip"
	modelBaseURL = s.URL + "/model"
	t.Cleanup(func() { distZipURL, modelBaseURL = oldZip, oldModel })
}

// writeSizedStub writes size zero bytes: the size checks only skip a file
// when its size already matches the manifest, so the stub must look real.
func writeSizedStub(t *testing.T, path string, size int64) {
	t.Helper()
	writeStub(t, path)
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}
}

func stubModelSet(t *testing.T, dir string, files int) {
	t.Helper()
	writeStub(t, filepath.Join(dir, dllName))
	modelDir := filepath.Join(dir, modelSubdir)
	for i := 0; i < files && i < len(modelSet); i++ {
		writeSizedStub(t, filepath.Join(modelDir, modelSet[i].name), modelSet[i].size)
	}
}

func TestEnsureDLLDownloadsAndExtracts(t *testing.T) {
	dir := t.TempDir()
	s := newStubServer(t, true)
	swapSources(t, s)
	if err := EnsureDLL(dir); err != nil {
		t.Fatalf("EnsureDLL: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, dllName)); err != nil || info.Size() != 1024 {
		t.Fatalf("DLL missing or wrong: info=%v err=%v", info, err)
	}
	if s.hits["/dist.zip"] != 1 {
		t.Fatalf("dist zip fetched %d times", s.hits["/dist.zip"])
	}
	// EnsureDLL never touches the models.
	if _, err := os.Stat(filepath.Join(dir, modelSubdir)); !os.IsNotExist(err) {
		t.Fatalf("EnsureDLL created the model dir: %v", err)
	}
}

func TestEnsureDLLKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, filepath.Join(dir, dllName))
	s := newStubServer(t, true)
	swapSources(t, s)
	if err := EnsureDLL(dir); err != nil {
		t.Fatalf("EnsureDLL: %v", err)
	}
	if len(s.hits) != 0 {
		t.Fatalf("network touched on an existing DLL: %v", s.hits)
	}
}

func TestEnsureDLLFailsWithoutEntry(t *testing.T) {
	dir := t.TempDir()
	s := newStubServer(t, false) // zip without bin/sam2_ncnn.dll
	swapSources(t, s)
	if err := EnsureDLL(dir); err == nil {
		t.Fatal("EnsureDLL = nil with a DLL-less dist zip")
	}
}

func TestDownloadModelFetchesSet(t *testing.T) {
	dir := t.TempDir()
	s := newStubServer(t, true)
	swapSources(t, s)
	var reports []ModelProgress
	err := DownloadModel(context.Background(), dir, func(p ModelProgress) {
		reports = append(reports, p)
	})
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if !ModelPresent(dir) {
		t.Fatal("ModelPresent = false after DownloadModel")
	}
	if _, ok := Detect(dir); !ok {
		t.Fatal("Detect = false after DownloadModel")
	}
	// Every file reported completion, in manifest order.
	completed := map[string]int64{}
	for _, p := range reports {
		if p.Bytes == p.Total {
			completed[p.Name] = p.Bytes
		}
	}
	if len(completed) != len(modelSet) {
		t.Fatalf("completed files %v, want %d", completed, len(modelSet))
	}
	for _, mf := range modelSet {
		if completed[mf.name] != mf.size {
			t.Fatalf("%s completed at %d, want %d", mf.name, completed[mf.name], mf.size)
		}
	}
}

func TestDownloadModelResumesPartialSet(t *testing.T) {
	dir := t.TempDir()
	stubModelSet(t, dir, 2) // DLL + the first two model files
	s := newStubServer(t, true)
	swapSources(t, s)
	if err := DownloadModel(context.Background(), dir, nil); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if !ModelPresent(dir) {
		t.Fatal("ModelPresent = false after resuming DownloadModel")
	}
	if s.hits["/dist.zip"] != 0 {
		t.Fatalf("dist zip re-downloaded although the DLL exists: %v", s.hits)
	}
	for _, mf := range modelSet[:2] {
		if s.hits["/model/"+mf.name] != 0 {
			t.Fatalf("existing file re-downloaded: %v", s.hits)
		}
	}
}

func TestDownloadModelNoopWhenPresent(t *testing.T) {
	dir := t.TempDir()
	stubModelSet(t, dir, len(modelSet))
	s := newStubServer(t, true)
	swapSources(t, s)
	if err := DownloadModel(context.Background(), dir, nil); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if len(s.hits) != 0 {
		t.Fatalf("network touched on a complete set: %v", s.hits)
	}
}

func TestDownloadModelCancelledContext(t *testing.T) {
	dir := t.TempDir()
	s := newStubServer(t, true)
	swapSources(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled: the request must never start
	err := DownloadModel(ctx, dir, nil)
	if err == nil {
		t.Fatal("DownloadModel = nil with a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The models must not be published; the DLL may well be there, because
	// DownloadModel repairs it through the idempotent EnsureDLL first.
	for _, mf := range modelSet {
		if _, err := os.Stat(filepath.Join(dir, modelSubdir, mf.name)); !os.IsNotExist(err) {
			t.Fatalf("model file exists after a cancelled download: %s", mf.name)
		}
	}
	// No download temp file may survive, in the model dir or the exe dir.
	for _, d := range []string{dir, filepath.Join(dir, modelSubdir)} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if len(e.Name()) >= 8 && e.Name()[:8] == ".sam2dl-" {
				t.Fatalf("left-over download temp file: %s", filepath.Join(d, e.Name()))
			}
		}
	}
}

func TestDownloadModelFailsOnSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	s := newStubServer(t, true)
	swapSources(t, s)
	old := modelSet[1].size
	modelSet[1].size = old + 1
	t.Cleanup(func() { modelSet[1].size = old })
	if err := DownloadModel(context.Background(), dir, nil); err == nil {
		t.Fatal("DownloadModel = nil with a size mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, modelSubdir, modelSet[1].name)); !os.IsNotExist(err) {
		t.Fatalf("mismatched file must not be published: %v", err)
	}
	// The unique temp file must be gone too; anything left starts with .sam2dl-.
	entries, _ := os.ReadDir(filepath.Join(dir, modelSubdir))
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".param" && filepath.Ext(e.Name()) != ".bin" {
			t.Fatalf("left-over temp file: %s", e.Name())
		}
	}
}
