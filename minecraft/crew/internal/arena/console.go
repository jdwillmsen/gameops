package arena

// Console runs one server command and returns what the server logged for it.
// The real implementation sends through the lab container's send-command and
// reads the server log; tests use FakeConsole.
type Console interface {
	Exec(cmd string) (output string, err error)
}
