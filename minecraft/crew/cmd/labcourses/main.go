// Command labcourses prints the console commands that build a crew lab test
// field: the work-yard grounds east of the spawn hub, the physics, shapes
// and pad courses west of it, the tree field beyond the courses, the works
// field north of the grounds, or the hub's name-tag signs. lab/courses.sh
// feeds the output to a server.
//
// usage: labcourses grounds|courses|trees|works|signs
//
// Output starts with "# area x0 z0 x1 z1" lines, the ticking areas the
// commands need loaded, followed by one command per line.
package main

import (
	"bufio"
	"fmt"
	"os"
)

type course struct {
	build func(*builder)
	areas func() []box
}

var courses = map[string]course{
	"grounds": {buildGrounds, groundsAreas},
	"courses": {buildCourses, coursesAreas},
	"trees":   {buildTrees, treesAreas},
	"works":   {buildWorks, worksAreas},
	"signs":   {buildSigns, signsAreas},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: labcourses grounds|courses|trees|works|signs")
		os.Exit(2)
	}
	c, ok := courses[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "labcourses: unknown field %q (want grounds, courses, trees, works or signs)\n", os.Args[1])
		os.Exit(2)
	}
	var b builder
	c.build(&b)
	w := bufio.NewWriter(os.Stdout)
	for _, a := range c.areas() {
		fmt.Fprintf(w, "# area %d %d %d %d\n", a.x0, a.z0, a.x1, a.z1)
	}
	for _, cmd := range b.cmds {
		fmt.Fprintln(w, cmd)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "labcourses:", err)
		os.Exit(1)
	}
}
