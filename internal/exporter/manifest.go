package exporter

import (
	"encoding/json"

	"mylabel/internal/ir"
)

// manifestDoc is the mylabel-export.json written at the export root
// (docs/phase4-design.md §2.2). It embeds the dry-run report verbatim so the
// on-disk record matches what the user confirmed.
type manifestDoc struct {
	Tool             string        `json:"tool"`
	IRVersion        string        `json:"ir_version"`
	Format           Format        `json:"format"`
	Task             Task          `json:"task,omitempty"`
	Options          Options       `json:"options"`
	Categories       []ir.Category `json:"categories"`
	CoordinateBasis  string        `json:"coordinate_basis"`
	NonstandardNotes []string      `json:"nonstandard_notes,omitempty"`
	Report           Report        `json:"report"`
}

// coordinateBasis documents the coordinate convention of the target format.
func coordinateBasis(f Format) string {
	switch f {
	case FormatYOLO:
		return "归一化 [0,1]（先裁剪到图像范围），0-based 类别"
	case FormatCOCO:
		return "绝对像素 [x,y,w,h] 左上角原点（先裁剪到图像范围）"
	case FormatVOC:
		return "绝对像素（先裁剪到图像范围），基准见 options.voc_one_based"
	}
	return ""
}

// marshalManifest renders the manifest for a finished plan.
func marshalManifest(plan *Plan) ([]byte, error) {
	doc := manifestDoc{
		Tool:             "mylabel",
		IRVersion:        ir.Version,
		Format:           plan.Opts.Format,
		Task:             plan.Opts.Task,
		Options:          plan.Opts,
		Categories:       plan.Categories,
		CoordinateBasis:  coordinateBasis(plan.Opts.Format),
		NonstandardNotes: plan.Report.Warnings,
		Report:           plan.Report,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
