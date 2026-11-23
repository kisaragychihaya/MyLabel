// Package ir defines the MyLabel intermediate representation: the exchange
// model between the app and the YOLO/COCO/VOC importers and exporters.
// Coordinates are always absolute image pixels (float64). See
// docs/phase3-design.md §2 for the binding contract.
package ir

// Version is the current ir_version written by WriteProject.
const Version = "1.0"

// Task values for Project.Task.
const (
	TaskDet   = "det"
	TaskSeg   = "seg"
	TaskObb   = "obb"
	TaskMixed = "mixed"
)

// Kind is the annotation geometry discriminant.
type Kind string

const (
	KindBBox    Kind = "bbox"         // axis-aligned box: X/Y/W/H, top-left origin
	KindPolygon Kind = "polygon"      // Rings[0] outer ring, rest holes/parts
	KindRotated Kind = "rotated_bbox" // Points (4, authoritative) + derived cache
)

// Annotation is one object on one image.
type Annotation struct {
	ID         string            `json:"id"`
	Category   string            `json:"category"` // stable category key, not an index
	Kind       Kind              `json:"kind"`
	X          float64           `json:"x,omitempty"` // bbox only, absolute pixels
	Y          float64           `json:"y,omitempty"`
	W          float64           `json:"w,omitempty"`
	H          float64           `json:"h,omitempty"`
	Points     [][2]float64      `json:"points,omitempty"` // rotated_bbox: 4 vertices, image pixels
	CX         float64           `json:"cx,omitempty"`     // rotated_bbox derived cache
	CY         float64           `json:"cy,omitempty"`
	RW         float64           `json:"rw,omitempty"`
	RH         float64           `json:"rh,omitempty"`
	Angle      float64           `json:"angle,omitempty"` // radians, direction of p0->p1
	Rings      [][][2]float64    `json:"rings,omitempty"` // polygon: Rings[0] outer, rest holes/parts
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Category is one project category. Key is stable across conversions; Index
// is only assigned at export time (YOLO class id etc.).
type Category struct {
	Key           string `json:"key"`
	Index         int    `json:"index"`
	Name          string `json:"name"`
	Color         string `json:"color,omitempty"`
	SuperCategory string `json:"supercategory,omitempty"`
	COCOID        *int   `json:"coco_id,omitempty"`
}

// Image is one image's metadata and annotations.
type Image struct {
	File        string       `json:"file"` // slash-separated relative path
	Width       int          `json:"width"`
	Height      int          `json:"height"`
	SHA256      string       `json:"sha256,omitempty"`
	Annotations []Annotation `json:"annotations"`
}

// Project is project.json.
type Project struct {
	IRVersion  string     `json:"ir_version"`
	Name       string     `json:"name,omitempty"`
	Task       string     `json:"task"` // det|seg|obb|mixed
	Categories []Category `json:"categories"`
}

// Issue is a locatable validation/normalization/parsing finding.
type Issue struct {
	File    string // image/txt/xml file, "" when project-level
	Line    int    // 1-based line for text formats, 0 when not applicable
	Index   int    // annotation index within the image, -1 when not applicable
	Field   string // offending field, "" when not applicable
	Message string
}
