package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `usage: components <command> [flags]
  validate                                 check every component.yaml
  affected -base <sha> -head <sha>         JSON matrix of components CI must run
  commits -component <name>                filter stdin SHAs to that component's
  releasable                               "<name> <tag>" per releasable component
  get -release-tag <component>-v<semver>   key=value facts for the release workflow`

func main() {
	root, err := repoRoot()
	if err == nil {
		err = run(root, os.Args[1:], os.Stdin, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "components:", err)
		os.Exit(1)
	}
}

func run(root string, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	ms, err := Load(root)
	if err != nil {
		return err
	}
	if errs := Validate(root, ms); len(errs) > 0 {
		return errors.Join(errs...)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	base := fs.String("base", "", "base commit")
	head := fs.String("head", "HEAD", "head commit")
	component := fs.String("component", "", "component name")
	releaseTag := fs.String("release-tag", "", "full release tag")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "validate":
		_, err = fmt.Fprintf(stdout, "%d components valid\n", len(ms))
		return err
	case "affected":
		return affectedCmd(root, ms, *base, *head, stdout)
	case "commits":
		return commitsCmd(root, ms, *component, stdin, stdout)
	case "releasable":
		for _, m := range ms {
			if m.Release != nil {
				fmt.Fprintf(stdout, "%s %s\n", m.Name, m.Release.Tag)
			}
		}
		return nil
	case "get":
		return getCmd(ms, *releaseTag, stdout)
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}

type matrixRow struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Language string `json:"language"`
	Build    string `json:"build"`
	Test     string `json:"test"`
	Lint     string `json:"lint"`
	Image    bool   `json:"image"`
}

func affectedCmd(root string, ms []Manifest, base, head string, stdout io.Writer) error {
	selected := ms
	// A push with no usable before-SHA (a new branch, a force push) has no
	// diff to trust, so everything runs rather than nothing.
	if strings.Trim(base, "0") != "" {
		paths, err := changedFiles(root, base, head)
		if err != nil {
			return err
		}
		selected = Affected(ms, paths, ModeCI)
	}
	rows := []matrixRow{}
	for _, m := range selected {
		img := m.Release != nil && m.Release.Image != nil
		rows = append(rows, matrixRow{m.Name, strings.TrimSuffix(m.Dir, "/"), m.Language,
			m.Tasks.Build, m.Tasks.Test, m.Tasks.Lint, img})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", b)
	return err
}

func find(ms []Manifest, match func(Manifest) bool) (Manifest, bool) {
	for _, m := range ms {
		if match(m) {
			return m, true
		}
	}
	return Manifest{}, false
}

func commitsCmd(root string, ms []Manifest, name string, stdin io.Reader, stdout io.Writer) error {
	m, ok := find(ms, func(m Manifest) bool { return m.Name == name })
	if !ok {
		return fmt.Errorf("no component named %q", name)
	}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		sha := strings.TrimSpace(sc.Text())
		if sha == "" {
			continue
		}
		files, err := commitFiles(root, sha)
		if err != nil {
			return err
		}
		if affects(m, files, ModeRelease) {
			fmt.Fprintln(stdout, sha)
		}
	}
	return sc.Err()
}

func getCmd(ms []Manifest, tag string, stdout io.Writer) error {
	prefix, version, err := ParseReleaseTag(tag)
	if err != nil {
		return err
	}
	m, ok := find(ms, func(m Manifest) bool { return m.Release != nil && m.Release.Tag == prefix })
	if !ok {
		return fmt.Errorf("no component releases under tag prefix %q", prefix)
	}
	kv := [][2]string{
		{"name", m.Name}, {"dir", strings.TrimSuffix(m.Dir, "/")}, {"version", version}, {"test", m.Tasks.Test},
	}
	if img := m.Release.Image; img != nil {
		kv = append(kv, [2]string{"image", img.Name}, [2]string{"description", img.Description},
			[2]string{"short_description", img.ShortDescription})
	}
	for _, p := range kv {
		fmt.Fprintf(stdout, "%s=%s\n", p[0], p[1])
	}
	return nil
}
