// Package importer parses external annotation formats (YOLO, VOC, COCO)
// into the IR exchange model. See docs/phase3-design.md §3 for the binding
// contract.
package importer

import (
	"errors"

	"mylabel/internal/ir"
)

var errNotImplemented = errors.New("importer: not implemented")

// Dataset is a parsed external annotation set ready for IR conversion.
type Dataset struct {
	Categories []ir.Category
	Images     []ir.Image // File is a slash-separated relative path
	Warnings   []ir.Issue // lossy/degraded conversions (RLE->bbox, ring drops, ...)
}

// Options tunes parsing.
type Options struct {
	// Task disambiguates YOLO 9-field lines (obb vs 4-point seg):
	// "auto" (default) guesses from the first data line: 5 fields det,
	// 9 obb, anything else seg. "det"/"seg"/"obb" force the reading.
	Task string
	// VOCOneBased subtracts 1 from VOC bndbox coordinates.
	VOCOneBased bool
}

// ParseYOLO parses a YOLO dataset: either its root directory (the one holding
// labels/, as in the Ultralytics images/ + labels/ layout) or a labels
// directory directly. dims maps slash-separated relative image paths to
// [width,height] for denormalization; each txt is matched to dims by its own
// path, then by the labels->images mirror of that path (labels/train/a.txt ->
// images/train/a.jpg), then by "images/" + its path, and finally by being a
// unique trailing path suffix of exactly one dims entry (dims keyed from a
// parent of the dataset dir), and last by a unique base name (dims keyed
// from an image subdirectory such as images/train). A txt that matches
// nothing is skipped with an
// Issue. Class names come from a data.yaml/data.yml found at or above the
// labels dir, or else from the only *.yaml/*.yml at the nearest level
// (coco8.yaml and friends; names: inline list or mapping); missing, classes are
// synthesized as class_N.
func ParseYOLO(labelsDir string, dims map[string][2]int, opts Options) (*Dataset, []ir.Issue, error) {
	return parseYOLO(labelsDir, dims, opts)
}

// ParseVOC parses a directory of VOC XML files. robndbox wins over bndbox;
// difficult/truncated become Attributes; a missing or mismatched <size>
// skips the file with an Issue.
func ParseVOC(xmlDir string, opts Options) (*Dataset, []ir.Issue, error) {
	return parseVOC(xmlDir, opts)
}

// ParseCOCO parses a COCO JSON file. segmentation may be polygon lists
// (kept as rings), array-count RLE, or compressed-string RLE (both decoded
// and degraded to the mask bounding box with an iscrowd attribute and a
// warning). Category ids are remapped by the categories array order, the
// original id preserved in COCOID.
func ParseCOCO(path string) (*Dataset, []ir.Issue, error) {
	return parseCOCO(path)
}
