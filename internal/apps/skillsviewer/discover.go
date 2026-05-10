package skillsviewer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one discovered skill on disk.
type Skill struct {
	// Name is the skill's frontmatter `name:` value if present, otherwise
	// the directory path relative to the skills root (e.g. "gstack/qa").
	Name string
	// Description is the frontmatter `description:` value, possibly
	// multi-line. Empty if missing.
	Description string
	// Group is the top-level dir under skills/ — useful for grouping in
	// the UI (e.g. "gstack", "smm-stack", or "" for top-level skills).
	Group string
	// Path is the absolute path of the SKILL.md file.
	Path string
	// Body is the SKILL.md contents (frontmatter included). Read on
	// discovery so the viewer is fully populated without per-skill I/O.
	Body string
}

// Discover walks every known skill source on this machine and returns the
// merged list, sorted by Group then Name. Sources, in order:
//
//  1. ~/.claude/skills          (user-level + plugin-installed skills)
//  2. <cwd>/.claude/skills      (project-level)
//
// Any source that doesn't exist is skipped silently.
func Discover() []Skill {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "skills"))
	}
	if cwd, err := os.Getwd(); err == nil {
		project := filepath.Join(cwd, ".claude", "skills")
		// Avoid double-listing if cwd happens to be inside ~/.claude.
		if !alreadyCovered(project, roots) {
			roots = append(roots, project)
		}
	}

	seen := make(map[string]bool)
	var skills []Skill
	for _, root := range roots {
		for _, sk := range discoverIn(root) {
			if seen[sk.Path] {
				continue
			}
			seen[sk.Path] = true
			skills = append(skills, sk)
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Group != skills[j].Group {
			return skills[i].Group < skills[j].Group
		}
		return skills[i].Name < skills[j].Name
	})
	return skills
}

func alreadyCovered(path string, roots []string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, r := range roots {
		rabs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if abs == rabs {
			return true
		}
	}
	return false
}

func discoverIn(root string) []Skill {
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	var out []Skill
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtree
		}
		if d.IsDir() {
			name := d.Name()
			// Skip noisy / large subtrees that won't contain SKILL.md.
			if name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			// Skip hidden dirs (`.git`, `.factory`, `.agents`, …). Tools
			// stash internal/templated copies of skills under hidden
			// dirs and we don't want to surface those as duplicates.
			// We don't skip the root (path == root) which may itself
			// be a hidden dir (e.g. ~/.claude/skills).
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sk := parseSkill(root, path, string(body))
		out = append(out, sk)
		return nil
	})
	return out
}

func parseSkill(root, path, body string) Skill {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		rel = filepath.Base(filepath.Dir(path))
	}
	rel = filepath.ToSlash(rel)

	group := ""
	if i := strings.Index(rel, "/"); i > 0 {
		group = rel[:i]
	} else {
		group = rel
	}

	name := rel
	desc := ""
	if fmName, fmDesc, ok := frontmatter(body); ok {
		if fmName != "" {
			name = fmName
		}
		desc = fmDesc
	}

	return Skill{
		Name:        name,
		Description: desc,
		Group:       group,
		Path:        path,
		Body:        body,
	}
}

// frontmatter extracts `name` and `description` from a YAML frontmatter
// block at the top of body (a `---`-delimited region). It's a hand-rolled
// micro-parser — pulling in a YAML lib just for two fields would be
// overkill. Multi-line block scalars (using `|` or `>`) are handled
// minimally: we collect indented continuation lines until the indent
// drops back.
func frontmatter(body string) (name, description string, ok bool) {
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return "", "", false
	}
	// Strip leading "---" line.
	rest := body[4:]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	block := rest[:end]
	lines := strings.Split(block, "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// We don't try to handle every YAML feature — just `key: value` and
		// `key: |` block scalars at column 0.
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		// Bail if the key is indented (we're inside a nested mapping or
		// continuation we already handled).
		if colon > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])

		switch key {
		case "name":
			if value != "" && value != "|" && value != ">" {
				name = stripQuotes(value)
			}
		case "description":
			if value == "|" || value == ">" || value == "|-" || value == ">-" {
				// Block scalar — collect indented continuation lines.
				var b strings.Builder
				for j := i + 1; j < len(lines); j++ {
					cont := lines[j]
					if cont == "" {
						b.WriteByte('\n')
						continue
					}
					if !(cont[0] == ' ' || cont[0] == '\t') {
						break
					}
					if b.Len() > 0 {
						b.WriteByte(' ')
					}
					b.WriteString(strings.TrimSpace(cont))
				}
				description = b.String()
			} else if value != "" {
				description = stripQuotes(value)
			}
		}
	}
	return name, description, true
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
