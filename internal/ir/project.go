package ir

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	projectName = "project.json"
	irDirName   = "ir"
	jsonExt     = ".json"
)

// WriteProject writes project.json and one JSON document per image under
// dir/ir/, mirroring the image relative paths (ir/sub/a.jpg.json). Writes
// are atomic (tmp + rename) and directories are created as needed.
//
// The written IRVersion is always Version; the field of p is ignored. A
// duplicated Image.File is an error. project.json is written last, so its
// presence means every image document was written.
func WriteProject(dir string, p *Project, images []Image) error {
	if dir == "" {
		return errors.New("ir: 项目目录为空")
	}
	if p == nil {
		return errors.New("ir: 项目为 nil")
	}

	targets := make([]string, len(images))
	seen := make(map[string]bool, len(images))
	for i, img := range images {
		if seen[img.File] {
			return fmt.Errorf("ir: 图片 %q 重复", img.File)
		}
		seen[img.File] = true
		target, err := imageDocPath(dir, img.File)
		if err != nil {
			return err
		}
		targets[i] = target
	}

	for i, img := range images {
		data, err := json.MarshalIndent(img, "", "  ")
		if err != nil {
			return fmt.Errorf("ir: 编码 %s 失败: %w", targets[i], err)
		}
		if err := os.MkdirAll(filepath.Dir(targets[i]), 0o755); err != nil {
			return fmt.Errorf("ir: 创建目录失败: %w", err)
		}
		if err := writeFileAtomic(targets[i], data); err != nil {
			return err
		}
	}

	doc := *p
	doc.IRVersion = Version
	projData, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("ir: 编码 %s 失败: %w", projectName, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ir: 创建目录失败: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, projectName), projData)
}

// ReadProject reads project.json and every image document under dir/ir/.
// A missing project.json returns an error wrapping fs.ErrNotExist; a
// corrupt image document fails with the path in the message.
//
// Images are returned sorted by File. A project whose major IR version
// differs from Version's is rejected as incompatible.
func ReadProject(dir string) (*Project, []Image, error) {
	if dir == "" {
		return nil, nil, errors.New("ir: 项目目录为空")
	}

	projPath := filepath.Join(dir, projectName)
	data, err := os.ReadFile(projPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("ir: 项目文件不存在: %w", err)
		}
		return nil, nil, fmt.Errorf("ir: 读取 %s 失败: %w", projPath, err)
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, nil, fmt.Errorf("ir: 解析 %s 失败: %w", projPath, err)
	}
	if err := checkVersion(p.IRVersion); err != nil {
		return nil, nil, err
	}

	root := filepath.Join(dir, irDirName)
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &p, nil, nil // a project without any image document
		}
		return nil, nil, fmt.Errorf("ir: 读取 %s 失败: %w", root, err)
	}

	var images []Image
	err = filepath.WalkDir(root, func(pth string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("ir: 遍历 %s 失败: %w", pth, err)
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(pth), jsonExt) {
			return nil
		}
		raw, err := os.ReadFile(pth)
		if err != nil {
			return fmt.Errorf("ir: 读取 %s 失败: %w", pth, err)
		}
		var img Image
		if err := json.Unmarshal(raw, &img); err != nil {
			return fmt.Errorf("ir: 解析 %s 失败: %w", pth, err)
		}
		if img.File == "" {
			rel, err := filepath.Rel(root, pth)
			if err != nil {
				return fmt.Errorf("ir: 无法推导 %s 的图片路径: %w", pth, err)
			}
			img.File = strings.TrimSuffix(filepath.ToSlash(rel), jsonExt)
		}
		images = append(images, img)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(images, func(a, b Image) int {
		return strings.Compare(a.File, b.File)
	})
	return &p, images, nil
}

// imageDocPath is dir/ir/<File>.json, with directories mirrored.
func imageDocPath(dir, file string) (string, error) {
	rel, err := cleanRelPath(file)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, irDirName, rel+jsonExt), nil
}

// cleanRelPath validates that file is a relative path inside the project and
// converts it to the OS form. Escaping the project root is rejected so a
// malformed File can never write outside dir.
func cleanRelPath(file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", errors.New("ir: 图片 file 为空")
	}
	if filepath.IsAbs(file) || filepath.VolumeName(file) != "" {
		return "", fmt.Errorf("ir: 图片 file %q 必须是项目内的相对路径", file)
	}
	slashed := path.Clean(filepath.ToSlash(file))
	if slashed == "." || slashed == ".." || strings.HasPrefix(slashed, "../") || path.IsAbs(slashed) {
		return "", fmt.Errorf("ir: 图片 file %q 必须是项目内的相对路径", file)
	}
	return filepath.FromSlash(slashed), nil
}

// writeFileAtomic writes data to target via a sibling tmp file and a rename.
// On Windows os.Rename cannot replace an existing file, so the target is
// removed first — the same trade-off internal/app/persist.go makes.
func writeFileAtomic(target string, data []byte) error {
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("ir: 写入 %s 失败: %w", tmp, err)
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(tmp)
		return fmt.Errorf("ir: 覆盖 %s 失败: %w", target, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ir: 重命名 %s 失败: %w", target, err)
	}
	return nil
}

// checkVersion rejects a project whose major IR version differs from the
// one this package writes; an absent or unparsable version is tolerated.
func checkVersion(v string) error {
	want, ok := versionMajor(Version)
	if !ok {
		return nil
	}
	got, ok := versionMajor(v)
	if !ok || got == want {
		return nil
	}
	return fmt.Errorf("ir: 版本不兼容: 项目为 %q，本程序支持 %s.x", strings.TrimSpace(v), want)
}

// versionMajor returns the leading numeric component of a dotted version.
func versionMajor(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return "", false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return v, true
}
