// Command labcourses prints the console commands that build a crew lab test
// field: the work-yard grounds east of the spawn hub, the physics, shapes
// and pad courses west of it, the tree field beyond the courses, the works
// field north of the grounds, or the hub's name-tag signs. It also imports
// Bedrock structures into crew blueprints and prints the commands that
// stamp a named blueprint — embedded in the binary, so no checkout is
// needed — at a spot and rotation. lab/courses.sh feeds the output to a
// server.
//
// usage: labcourses grounds|courses|trees|works|signs
//
//	labcourses blueprint-import [-o <dir>] <file.mcstructure> <name> <kind>
//	labcourses blueprint <name> <x> <z> [0|90|180|270]
//	labcourses -o <dir> <field>...
//
// Output starts with "# area x0 z0 x1 z1" lines, the ticking areas the
// commands need loaded, followed by one command per line.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jdwillmsen/gameops/minecraft/crew/blueprints"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
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
	if len(os.Args) > 1 && os.Args[1] == "blueprint-import" {
		var pos []string
		outDir := ""
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if args[i] == "-o" {
				if i+1 == len(args) {
					fail("usage: labcourses blueprint-import [-o <dir>] <file.mcstructure> <name> <kind>")
				}
				outDir = args[i+1]
				i++
				continue
			}
			pos = append(pos, args[i])
		}
		if len(pos) != 3 {
			fail("usage: labcourses blueprint-import [-o <dir>] <file.mcstructure> <name> <kind>")
		}
		if outDir == "" {
			outDir = "minecraft/crew/blueprints"
			// The default only exists inside a checkout; refuse to sprout
			// a stray minecraft/ tree from some other directory.
			if _, err := os.Stat(outDir); err != nil {
				fail("labcourses: blueprint output directory " + outDir + " does not exist; run from the repository root or pass -o <dir>")
			}
		}
		bp, err := blueprint.Import(pos[0], pos[1], pos[2])
		if err != nil {
			fail("labcourses: " + err.Error())
		}
		warn(bp)
		data, err := json.MarshalIndent(bp, "", "  ")
		if err != nil {
			fail("labcourses: " + err.Error())
		}
		if err = os.MkdirAll(outDir, 0755); err != nil {
			fail("labcourses: " + err.Error())
		}
		if err = os.WriteFile(filepath.Join(outDir, bp.Name+".json"), append(data, '\n'), 0644); err != nil {
			fail("labcourses: " + err.Error())
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "blueprint" {
		if len(os.Args) != 5 && len(os.Args) != 6 {
			fail("usage: labcourses blueprint <name> <x> <z> [rot 0|90|180|270]")
		}
		bp, err := blueprint.LoadFS(blueprints.Files, os.Args[2]+".json")
		if err != nil {
			fail("labcourses: " + err.Error())
		}
		warn(bp)
		x, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fail("labcourses: invalid x coordinate")
		}
		z, err := strconv.Atoi(os.Args[4])
		if err != nil {
			fail("labcourses: invalid z coordinate")
		}
		rot := 0
		if len(os.Args) == 6 {
			rot, err = strconv.Atoi(os.Args[5])
			if err != nil {
				fail("labcourses: invalid rotation")
			}
		}
		if rot != 0 && rot != 90 && rot != 180 && rot != 270 {
			fail("labcourses: rotation must be 0, 90, 180 or 270")
		}
		printBlueprint(bp, x, z, rot)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-o" {
		os.Exit(writePlans(os.Args[2:]))
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: labcourses grounds|courses|trees|works|signs|blueprint <name> <x> <z> [rot]|blueprint-import <file> <name> <kind>|-o <dir> <field>...")
		os.Exit(2)
	}
	c, ok := courses[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "labcourses: unknown field %q (want grounds, courses, trees, works or signs)\n", os.Args[1])
		os.Exit(2)
	}
	if err := render(os.Stdout, c); err != nil {
		fmt.Fprintln(os.Stderr, "labcourses:", err)
		os.Exit(1)
	}
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }

func warn(bp *blueprint.Blueprint) {
	for _, w := range bp.Warnings() {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", bp.Name, w)
	}
}
