package importer

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// yoloDataYAMLCandidates are the dataset descriptor names searched for, in
// priority order.
var yoloDataYAMLCandidates = []string{"data.yaml", "data.yml"}

// findDataYAML returns the first data.yaml/data.yml found in dir or in any of
// its parents (nearest first), or "" when there is none.
func findDataYAML(dir string) string {
	for cur := filepath.Clean(dir); ; {
		for _, name := range yoloDataYAMLCandidates {
			p := filepath.Join(cur, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// yoloNamesFor returns the class names declared by the yaml descriptor of the
// dataset owning labelsDir, or nil when there is none or it carries no usable
// names.
//
// A data.yaml/data.yml at labelsDir or at any parent wins outright. Otherwise
// the nearest directory holding exactly one *.yaml/*.yml file supplies the
// names: Ultralytics names its descriptor after the dataset (coco8.yaml, ...).
// Two or more yaml files at one level are ambiguous and end the search, since
// guessing would silently attach another dataset's class list.
func yoloNamesFor(labelsDir string) map[int]string {
	if p := findDataYAML(labelsDir); p != "" {
		return parseYOLONamesFile(p)
	}
	for _, dir := range ancestorDirs(labelsDir) {
		p, unique := uniqueYAMLIn(dir)
		if !unique {
			return nil
		}
		if p != "" {
			return parseYOLONamesFile(p)
		}
	}
	return nil
}

// ancestorDirs lists dir and its parents, nearest first, ending at the volume
// root.
func ancestorDirs(dir string) []string {
	dirs := []string{}
	for cur := filepath.Clean(dir); ; {
		dirs = append(dirs, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			return dirs
		}
		cur = parent
	}
}

// uniqueYAMLIn returns the only *.yaml/*.yml file directly inside dir, "" when
// there is none, and unique=false when several make the choice ambiguous.
func uniqueYAMLIn(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", true
	}
	found := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
			if found != "" {
				return "", false
			}
			found = filepath.Join(dir, e.Name())
		}
	}
	return found, true
}

// parseYOLONamesFile reads path and extracts its names table.
func parseYOLONamesFile(path string) map[int]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseYOLONames(string(data))
}

// parseYOLONames extracts the `names:` table of a data.yaml, accepting the
// inline list form (`names: ['a', 'b']`), the indented mapping form
// (`names:\n  0: a`) and the indented sequence form (`names:\n  - a`).
// Anything it does not recognize yields nil: the caller then synthesizes
// class_N names, so an exotic descriptor costs names but never an error.
func parseYOLONames(content string) map[int]string {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(content, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if isYAMLComment(line) {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		rest, ok := cutYAMLKey(trimmed, "names")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest != "" {
			return parseInlineNames(rest)
		}
		return parseBlockNames(lines[i+1:], indentWidth(line))
	}
	return nil
}

// cutYAMLKey reports whether line is `key:` / `key: value` and returns the
// text after the colon.
func cutYAMLKey(line, key string) (string, bool) {
	if !strings.HasPrefix(line, key) {
		return "", false
	}
	rest := line[len(key):]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	return rest[1:], true
}

func isYAMLComment(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
}

func indentWidth(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// parseInlineNames parses `['a', "b", c]` into an index->name table.
func parseInlineNames(s string) map[int]string {
	if !strings.HasPrefix(s, "[") {
		return nil
	}
	end := inlineBracketEnd(s)
	if end < 0 {
		return nil
	}
	items := splitYAMLList(s[1:end])
	out := make(map[int]string, len(items))
	for i, item := range items {
		name := yamlScalar(item)
		if name == "" {
			return nil
		}
		out[i] = name
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// inlineBracketEnd returns the index of the "]" closing the leading "[",
// honouring quotes, or -1.
func inlineBracketEnd(s string) int {
	var quote byte
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ']':
			return i
		}
	}
	return -1
}

// splitYAMLList splits on commas that are not inside quotes.
func splitYAMLList(s string) []string {
	var (
		quote byte
		items []string
		start int
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ',':
			items = append(items, s[start:i])
			start = i + 1
		}
	}
	items = append(items, s[start:])
	return items
}

// parseBlockNames parses the indented block following a bare `names:` line.
// baseIndent is the indentation of the names key itself; the block ends at the
// first line indented no deeper than it.
func parseBlockNames(lines []string, baseIndent int) map[int]string {
	out := map[int]string{}
	seq := 0
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || isYAMLComment(line) {
			continue
		}
		if indentWidth(line) <= baseIndent {
			break
		}
		body := strings.TrimSpace(line)
		if strings.HasPrefix(body, "-") {
			name := yamlScalar(strings.TrimPrefix(body, "-"))
			if name == "" {
				return nil
			}
			out[seq] = name
			seq++
			continue
		}
		key, value, ok := strings.Cut(body, ":")
		if !ok {
			return nil
		}
		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || id < 0 {
			return nil
		}
		name := yamlScalar(value)
		if name == "" {
			return nil
		}
		out[id] = name
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// yamlScalar unquotes a scalar, dropping a trailing ` # comment` when it is
// unquoted.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '\'' || s[0] == '"' {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			return s[1 : 1+end]
		}
		return s[1:]
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
