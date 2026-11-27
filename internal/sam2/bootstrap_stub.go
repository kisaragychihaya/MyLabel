//go:build !windows

package sam2

import "context"

// ModelPresent and EnsureDLL are no-ops off Windows: Detect never succeeds
// there anyway (the stub build reports false), so there is no runtime to
// provision and no model to download.
func ModelPresent(exeDir string) bool { return false }

func EnsureDLL(exeDir string) error { return nil }

func DownloadModel(ctx context.Context, exeDir string, onProgress func(ModelProgress)) error {
	return nil
}
