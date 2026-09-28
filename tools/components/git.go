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

// -m lists a merge commit's changes against each parent, and --root lets the
// first commit report its files instead of nothing.
func commitFiles(root, sha string) ([]string, error) {
	return gitLines(root, "diff-tree", "--no-commit-id", "--name-only", "-r", "-m", "--root", sha)
}

func repoRoot() (string, error) {
	lines, err := gitLines(".", "rev-parse", "--show-toplevel")
	if err != nil || len(lines) != 1 {
		return "", fmt.Errorf("not inside a git repository: %v", err)
	}
	return lines[0], nil
}
