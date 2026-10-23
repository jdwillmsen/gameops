package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/lab"
)

func render(out io.Writer, c course) error {
	var b builder
	c.build(&b)
	w := bufio.NewWriter(out)
	for _, a := range c.areas() {
		fmt.Fprintf(w, "# area %d %d %d %d\n", a.x0, a.z0, a.x1, a.z1)
	}
	for _, cmd := range b.cmds {
		fmt.Fprintln(w, cmd)
	}
	return w.Flush()
}

// writePlans fills dir with <field>.plan for each field, plus the scripts
// that feed plans to a server. A job image with no shell or go toolchain
// runs this once in an init container and hands dir to the container that
// has kubectl; shipping the scripts from here keeps one copy of the feed
// logic for the job and for an operator's checkout.
func writePlans(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: labcourses -o <dir> <field>...")
		return 2
	}
	dir, fields := args[0], args[1:]
	for _, f := range fields {
		if _, ok := courses[f]; !ok {
			fmt.Fprintf(os.Stderr, "labcourses: unknown field %q (want %s)\n", f, strings.Join(fieldNames(), ", "))
			return 2
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "labcourses:", err)
		return 1
	}
	for _, f := range fields {
		if err := writePlan(filepath.Join(dir, f+".plan"), courses[f]); err != nil {
			fmt.Fprintln(os.Stderr, "labcourses:", err)
			return 1
		}
		if f == "arena" {
			if err := writeArenaLayout(filepath.Join(dir, "arena.layout.json")); err != nil {
				fmt.Fprintln(os.Stderr, "labcourses:", err)
				return 1
			}
		}
	}
	if err := writeScripts(dir); err != nil {
		fmt.Fprintln(os.Stderr, "labcourses:", err)
		return 1
	}
	return 0
}

func writeScripts(dir string) error {
	entries, err := fs.ReadDir(lab.Scripts, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		body, err := lab.Scripts.ReadFile(e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func writePlan(path string, c course) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return render(f, c)
}

// writeArenaLayout ships the director's pen/gate coordinates beside the plan,
// so a job image with no Go toolchain still has the layout the build used.
func writeArenaLayout(path string) error {
	data, err := json.MarshalIndent(arenaLayout(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fieldNames() []string {
	names := make([]string, 0, len(courses))
	for n := range courses {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
