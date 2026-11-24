// Package sam2 is the pure-Go client for the sam2_ncnn.dll C ABI (SAM2
// Hiera-Small segmentation on ncnn, Vulkan or CPU). It talks to the DLL
// through syscall.NewLazyDLL, so CGO_ENABLED=0 builds keep working and no
// dependency is added; the ABI is the one declared in
// libsam2_ncnn/native/include/sam2_ncnn.h.
// See docs/phase7-design.md §5.2 for the binding contract.
//
// The package is implemented twice, behind the same API: sam2.go is the
// Windows binding (syscall.NewLazyDLL has no other-platform counterpart), and
// sam2_stub.go stands in everywhere else so the app still builds for the web
// (WASM) target. Off Windows Detect reports false, which hides the AI assist
// exactly as a missing DLL does (docs/phase7-design.md §1, §4).
package sam2

// Config locates one installation of the native library. The matching .bin file
// of each .param must sit next to it (the DLL resolves it itself).
type Config struct {
	DLL          string // sam2_ncnn.dll
	EncoderParam string // sam2_encoder_fixed.ncnn.param
	DecoderParam string // sam2_decoder_fixed.ncnn.param
	DeviceID     int    // Vulkan device index, 0 = default; ignored by the CPU backend
}

// Result is one decode output: one mask per candidate plus its predicted IoU.
// Masks[i] holds W*H logits in row-major order (no thresholding is applied);
// the data is copied out of the DLL, so it stays valid after the native result
// has been destroyed.
type Result struct {
	Masks [][]float32
	IoU   []float32
	W, H  int
}

// Best returns the index of the mask with the highest predicted IoU (-1 for an
// empty result).
func (r Result) Best() int {
	best := -1
	for i, v := range r.IoU {
		if best < 0 || v > r.IoU[best] {
			best = i
		}
	}
	return best
}
