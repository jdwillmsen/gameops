package trainer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const maxCmdOutput = 8 << 20

// CmdEvaluator fights by running an external command. The command gets
// {"profile":...,"scenario":...} as JSON on stdin and the scenario in
// TRAINER_* environment variables, and prints one Scorecard per line on
// stdout. Only the cards naming the evaluated profile are kept, so a duel
// command may print the opponent's card too. Argv is split on whitespace and
// run without a shell.
type CmdEvaluator struct {
	Command string
	Timeout time.Duration // per fight; zero means the run's own limits
	Env     []string
}

type cmdRequest struct {
	Profile  tactics.Profile `json:"profile"`
	Scenario Scenario        `json:"scenario"`
}

func (e CmdEvaluator) Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
	argv := strings.Fields(e.Command)
	if len(argv) == 0 {
		return nil, errors.New("trainer: empty evaluator command")
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	in, err := json.Marshal(cmdRequest{Profile: p, Scenario: s})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = append(append(cmd.Environ(), e.Env...),
		"TRAINER_SCENARIO="+s.Name, "TRAINER_KIND="+s.Kind, "TRAINER_FAMILY="+string(s.Family),
		"TRAINER_SIDE="+s.Side, fmt.Sprintf("TRAINER_SEED=%d", s.Seed), "TRAINER_PROFILE="+p.Name)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 2048}
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("trainer: evaluator %q: %w: %s", argv[0], err, strings.TrimSpace(stderr.String()))
	}
	cards, err := parseScorecards(io.LimitReader(bytes.NewReader(out), maxCmdOutput))
	if err != nil {
		return nil, err
	}
	return ownCards(cards, p.Name), nil
}

func parseScorecards(r io.Reader) ([]tactics.Scorecard, error) {
	var cards []tactics.Scorecard
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var c tactics.Scorecard
		if err := json.Unmarshal([]byte(text), &c); err != nil {
			return nil, fmt.Errorf("trainer: scorecard line %d: %w", line, err)
		}
		cards = append(cards, c)
	}
	return cards, sc.Err()
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
