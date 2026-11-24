package exporter

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"mylabel/internal/ir"
)

// assignSplits maps every image File to "train" or "val"
// (docs/phase4-design.md §3, docs/phase5-design.md §7.1).
//
// SplitPreserve: a case-insensitive leading "train"/"val" path segment of
// File — after stripping one optional leading "images/" segment, so an
// Ultralytics layout like images/train/x.jpg is recognized — is kept;
// everything else becomes "train". SplitRandom: sort the File list, shuffle
// deterministically with rand.New(rand.NewPCG(seed, seed^0x9E3779B9)), take
// round(n*TrainRatio) as train — at least 1 train, and at least 1 val when
// n >= 2. Same input and seed reproduce the same map.
//
// pol.Overrides is applied last in both modes: each entry forces that image
// into the given split. Unknown keys (not in images) and values other than
// "train"/"val" are errors, so the caller never writes a half-intended split.
func assignSplits(images []ir.Image, pol SplitPolicy) (map[string]string, error) {
	out := make(map[string]string, len(images))
	if pol.Mode == "" || pol.Mode == SplitPreserve {
		for _, img := range images {
			out[img.File] = "train"
			f := splitHint(img.File)
			if i := strings.IndexByte(f, '/'); i > 0 {
				switch strings.ToLower(f[:i]) {
				case "train", "val":
					out[img.File] = strings.ToLower(f[:i])
				}
			}
		}
		return out, applySplitOverrides(out, pol.Overrides)
	}

	files := make([]string, len(images))
	for i, img := range images {
		files[i] = img.File
	}
	slices.Sort(files)
	rng := rand.New(rand.NewPCG(uint64(pol.Seed), uint64(pol.Seed)^0x9E3779B9))
	rng.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })

	nTrain := int(math.Round(float64(len(files)) * pol.TrainRatio))
	if nTrain < 1 {
		nTrain = 1
	}
	if len(files) >= 2 && nTrain > len(files)-1 {
		nTrain = len(files) - 1
	}
	for i, f := range files {
		if i < nTrain {
			out[f] = "train"
		} else {
			out[f] = "val"
		}
	}
	return out, applySplitOverrides(out, pol.Overrides)
}

// applySplitOverrides forces the requested images into their split, rejecting
// unknown image keys and values other than train/val. Keys are visited in
// sorted order so the reported error is deterministic.
func applySplitOverrides(out map[string]string, overrides map[string]string) error {
	if len(overrides) == 0 {
		return nil
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := overrides[k]
		if _, ok := out[k]; !ok {
			return fmt.Errorf("exporter: 逐图划分指定的图片 %q 不在待导出图片中", k)
		}
		switch v {
		case "train", "val":
			out[k] = v
		default:
			return fmt.Errorf("exporter: 图片 %q 的逐图划分值必须为 train 或 val，得到 %q", k, v)
		}
	}
	return nil
}

// splitHint strips one leading case-insensitive "images/" segment so split
// detection sees the split segment of an Ultralytics source layout
// (images/train/x.jpg -> train/x.jpg). Only assignSplits uses this.
func splitHint(file string) string {
	f := strings.ReplaceAll(file, "\\", "/")
	if len(f) > 7 && strings.EqualFold(f[:7], "images/") {
		return f[7:]
	}
	return f
}

// exportRel maps an image's relative path to its destination-relative form
// beneath images/<split>/ (or labels/<split>/): it strips one leading
// case-insensitive "images/" segment and then one leading train/val/test
// segment, so an Ultralytics source layout (images/train/x.jpg) round-trips
// to images/train/x.jpg instead of doubling directories
// (docs/phase4-design.md §3). The source path (ImageCopy.Src) is never
// stripped.
func exportRel(file string) string {
	f := splitHint(file)
	if i := strings.IndexByte(f, '/'); i > 0 {
		switch strings.ToLower(f[:i]) {
		case "train", "val", "test":
			return f[i+1:]
		}
	}
	return f
}
