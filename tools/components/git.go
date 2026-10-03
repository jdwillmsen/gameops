package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func gitLines(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func changedFiles(root, base, head string) ([]string, error) {
	return gitLines(root, "diff", "--name-only", base+"..."+head)
}

// A merge commit is judged against its first parent only: listing it against
// every parent would credit it with everything its other side already had.
// --no-renames keeps a file moved out of a component counting for that
// component, and show (unlike diff-tree) lets the first commit report its
// files instead of nothing.
func commitFiles(root, sha string) ([]string, error) {
	return gitLines(root, "show", "--first-parent", "--no-renames", "--name-only", "--format=", sha)
}

// commitExists reports whether sha names a commit this clone has, which a
// force push's "before" SHA may not.
func commitExists(root, sha string) bool {
	if strings.HasPrefix(sha, "-") {
		return false
	}
	cmd := exec.Command("git", "cat-file", "-e", sha+"^{commit}")
	cmd.Dir = root
	return cmd.Run() == nil
}

func repoRoot() (string, error) {
	lines, err := gitLines(".", "rev-parse", "--show-toplevel")
	if err != nil || len(lines) != 1 {
		return "", fmt.Errorf("not inside a git repository: %v", err)
	}
	return lines[0], nil
}
