package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FindRepos searches for git repositories under the given base paths.
// Returns paths relative to the base, sorted by match relevance.
func FindRepos(basePaths []string, query string) []string {
	seen := make(map[string]bool)
	var results []string

	for _, base := range basePaths {
		expanded := ExpandHome(base)
		repos := findGitRepos(expanded, 3) // max depth 3
		for _, repo := range repos {
			if seen[repo] {
				continue
			}
			seen[repo] = true
			results = append(results, repo)
		}
	}

	if query == "" {
		sort.Strings(results)
		return results
	}

	// Fuzzy match: score by how well the query matches
	type scored struct {
		path  string
		score int
	}
	var matched []scored
	lowerQuery := strings.ToLower(query)

	for _, r := range results {
		name := strings.ToLower(filepath.Base(r))
		full := strings.ToLower(r)

		score := 0
		// Exact base name match
		if name == lowerQuery {
			score = 100
		} else if strings.HasPrefix(name, lowerQuery) {
			score = 80
		} else if strings.Contains(name, lowerQuery) {
			score = 60
		} else if strings.Contains(full, lowerQuery) {
			score = 40
		}

		if score > 0 {
			matched = append(matched, scored{r, score})
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].path < matched[j].path
	})

	out := make([]string, len(matched))
	for i, m := range matched {
		out[i] = m.path
	}
	return out
}

// FindRemoteRepos searches for git repos on a remote machine via SSH.
//
// The SSH is bounded the same way the discovery probe is, and for a sharper
// reason: this runs off the path input in the new-session form. Plain
// `ssh host` against a machine that has dropped off the network sits in the
// kernel's TCP retry for over a minute, so every keystroke used to leave
// another wedged process behind. ConnectTimeout caps the handshake, the
// context caps the whole call, and BatchMode stops ssh trying to prompt for
// a password on a terminal the dashboard is busy drawing to.
func FindRemoteRepos(machine, basePath, query string, timeout time.Duration) []string {
	if basePath == "" {
		basePath = "~"
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Use find via SSH to locate .git directories (max depth 3).
	// `find` exits non-zero on any unreadable dir even with 2>/dev/null,
	// so we treat captured stdout as truth and only bail when there's
	// nothing at all (real SSH/connection failure).
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(machine,
		"find", basePath,
		"-maxdepth", "4", "-name", ".git", "-type", "d",
		"2>/dev/null")...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}

	var repos []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove trailing /.git
		repo := strings.TrimSuffix(line, "/.git")
		repos = append(repos, repo)
	}

	if query == "" {
		sort.Strings(repos)
		return repos
	}

	// Fuzzy filter
	lowerQuery := strings.ToLower(query)
	var matched []string
	for _, r := range repos {
		name := strings.ToLower(filepath.Base(r))
		if strings.Contains(name, lowerQuery) || strings.Contains(strings.ToLower(r), lowerQuery) {
			matched = append(matched, r)
		}
	}
	return matched
}

// findGitRepos walks directories up to maxDepth looking for .git dirs.
func findGitRepos(root string, maxDepth int) []string {
	var repos []string

	_ = walkDepth(root, 0, maxDepth, func(path string, d os.DirEntry, depth int) error {
		if d.Name() == ".git" && d.IsDir() {
			repos = append(repos, filepath.Dir(path))
			return filepath.SkipDir
		}
		// Skip hidden dirs and common non-repo dirs
		if d.IsDir() && d.Name() != "." {
			if strings.HasPrefix(d.Name(), ".") ||
				d.Name() == "node_modules" ||
				d.Name() == "vendor" ||
				d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
		}
		return nil
	})

	return repos
}

// walkDepth is like filepath.WalkDir but tracks depth.
func walkDepth(root string, currentDepth, maxDepth int, fn func(string, os.DirEntry, int) error) error {
	if currentDepth > maxDepth {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil // skip unreadable dirs
	}

	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		err := fn(path, entry, currentDepth)
		if err == filepath.SkipDir {
			continue
		}
		if err != nil {
			return err
		}
		if entry.IsDir() && currentDepth < maxDepth {
			_ = walkDepth(path, currentDepth+1, maxDepth, fn)
		}
	}
	return nil
}
