package arena

import "strings"

// FakeConsole records commands and answers with a scripted response, so the
// director and its command builders can run without a server.
type FakeConsole struct {
	Cmds    []string
	Respond func(cmd string) (string, error)
}

func (f *FakeConsole) Exec(cmd string) (string, error) {
	f.Cmds = append(f.Cmds, cmd)
	if f.Respond == nil {
		return "", nil
	}
	return f.Respond(cmd)
}

// Count returns how many recorded commands contain sub, for assertions.
func (f *FakeConsole) Count(sub string) int {
	n := 0
	for _, cmd := range f.Cmds {
		if strings.Contains(cmd, sub) {
			n++
		}
	}
	return n
}
