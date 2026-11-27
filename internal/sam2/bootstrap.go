//go:build windows

package sam2

// First-run provisioning of the AI assist runtime. A fresh install ships the
// native DLL (resources/windows/sam2_ncnn.dll is bundled by mygo build) but
// not the model weights (~106 MB): EnsureDLL fetches the DLL's dist zip at
// startup when even that is missing, while the model set downloads only on
// the user's word, through DownloadModel, with byte-level progress and
// cancellation. Everything here uses only the standard library.

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download sources. The dist zip carries the Vulkan-capable sam2_ncnn.dll;
// the ModelScope repo mirrors the converted ncnn model sets.
var (
	distZipURL   = "https://gitee.com/mashiro_F/libsam2_ncnn/releases/download/v0.2.2/dist-v0.2.2-vulkan.zip"
	modelBaseURL = "https://modelscope.cn/models/xxx32qw/SAM2-NCNN/resolve/master/sam2.1-small"
)

// modelFile is one file of the model set: its name in the model repo
// directory and the exact byte size the download must have.
type modelFile struct {
	name string
	size int64
}

// modelSet is the SAM2.1-small five-piece set (the best of the three
// converted models on the coco128 single-point benchmark).
var modelSet = []modelFile{
	{"sam2.1-small-encoder_fixed.ncnn.param", 36_892},
	{"sam2.1-small-encoder_fixed.ncnn.bin", 93_838_868},
	{"sam2.1-small-decoder_fixed.ncnn.param", 7_796},
	{"sam2.1-small-decoder_fixed.ncnn.bin", 8_189_244},
	{"sam2_prompt.bin", 4_201_582},
}

// progressEvery throttles download progress callbacks to this many bytes, so
// the UI refreshes at a human pace instead of per network read.
const progressEvery = 512 * 1024

// ModelProgress reports one file's download progress: which file, how many
// bytes of it are on disk, its total size, and how many of the set's files
// finished already (Done == Count means the set is complete).
type ModelProgress struct {
	Name  string
	Bytes int64
	Total int64
	Done  int
	Count int
}

// ModelPresent reports whether the whole model set sits under exeDir/models
// with the exact expected sizes.
func ModelPresent(exeDir string) bool {
	for _, mf := range modelSet {
		info, err := os.Stat(filepath.Join(exeDir, modelSubdir, mf.name))
		if err != nil || info.Size() != mf.size {
			return false
		}
	}
	return true
}

// EnsureDLL makes only the native DLL exist next to the executable,
// downloading the dist zip when it is missing. It never touches the models.
func EnsureDLL(exeDir string) error {
	if _, err := os.Stat(filepath.Join(exeDir, dllName)); err == nil {
		return nil
	}
	return downloadDLL(exeDir)
}

// DownloadModel fetches the missing model files into exeDir/models, after
// making sure the DLL is there too (EnsureDLL is idempotent, so a portable
// copy that skipped the startup download is repaired here as well).
// onProgress, when non-nil, is called as the bytes land — throttled to
// progressEvery — and also once per already-complete file with Bytes == Total.
// Cancelling ctx aborts the transfer; a half-written file never appears at
// its final name (downloads publish via .tmp + rename).
func DownloadModel(ctx context.Context, exeDir string, onProgress func(ModelProgress)) error {
	if ModelPresent(exeDir) {
		return nil
	}
	if err := EnsureDLL(exeDir); err != nil {
		return fmt.Errorf("%s: %w", dllName, err)
	}
	modelDir := filepath.Join(exeDir, modelSubdir)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return err
	}
	for i, mf := range modelSet {
		dst := filepath.Join(modelDir, mf.name)
		if info, err := os.Stat(dst); err == nil && info.Size() == mf.size {
			if onProgress != nil {
				onProgress(ModelProgress{Name: mf.name, Bytes: mf.size, Total: mf.size, Done: i, Count: len(modelSet)})
			}
			continue
		}
		report := func(bytes int64) {
			if onProgress != nil {
				onProgress(ModelProgress{Name: mf.name, Bytes: bytes, Total: mf.size, Done: i, Count: len(modelSet)})
			}
		}
		if err := downloadFile(ctx, modelBaseURL+"/"+mf.name, dst, mf.size, report); err != nil {
			return fmt.Errorf("%s: %w", mf.name, err)
		}
		report(mf.size)
	}
	return nil
}

// downloadDLL fetches the dist zip and extracts bin/sam2_ncnn.dll next to the
// executable. The zip also carries sam2qc.exe, which the app never runs; only
// the DLL is extracted.
func downloadDLL(exeDir string) error {
	// A fixed overall ceiling only: the DLL is ~15 MB, but the zip comes from
	// a release mirror that can be slow to start streaming.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp("", "sam2-dist-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := downloadTo(ctx, distZipURL, tmp, nil); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != "bin/"+dllName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.CreateTemp(exeDir, ".sam2dll-*")
		if err != nil {
			rc.Close()
			return err
		}
		tmpName := out.Name()
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			os.Remove(tmpName)
			return err
		}
		return os.Rename(tmpName, filepath.Join(exeDir, dllName))
	}
	return errors.New("dist zip has no bin/" + dllName)
}

// downloadFile fetches one model file into dst, verifies its size and
// publishes it atomically, so a cancelled or crashed download never leaves a
// truncated file where ModelPresent would accept it. The temp name is unique
// per call: a cancelled run must never delete the next attempt's partial
// file through a shared fixed name.
func downloadFile(ctx context.Context, url, dst string, want int64, onBytes func(int64)) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".sam2dl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTmp := func() { os.Remove(tmpName) }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		tmp.Close()
		removeTmp()
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tmp.Close()
		removeTmp()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		removeTmp()
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	_, err = io.Copy(tmp, &progressWriter{r: resp.Body, every: progressEvery, on: onBytes})
	if err != nil {
		tmp.Close()
		removeTmp()
		return err
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return err
	}
	info, err := os.Stat(tmpName)
	if err != nil {
		removeTmp()
		return err
	}
	if info.Size() != want {
		removeTmp()
		return fmt.Errorf("size %d, want %d", info.Size(), want)
	}
	return os.Rename(tmpName, dst)
}

// downloadTo streams url into w, failing on non-2xx statuses.
func downloadTo(ctx context.Context, url string, w io.Writer, onBytes func(int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	_, err = io.Copy(w, &progressWriter{r: resp.Body, every: progressEvery, on: onBytes})
	return err
}

// progressWriter passes reads through, firing on after every `every` bytes.
type progressWriter struct {
	r     io.Reader
	every int64
	on    func(int64)
	n     int64
	last  int64
}

func (p *progressWriter) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.on != nil && p.n-p.last >= p.every {
		p.last = p.n
		p.on(p.n)
	}
	return n, err
}
