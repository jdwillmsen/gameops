// Command tacticsctl is the human control surface over warrior Profiles.
//
// usage: tacticsctl [--dir D] [--trust T] [--store S] <command>
//
//	status [--json]                  what is live per stage, with parent chain
//	list                             candidate profiles
//	add <file>                       file a profile as a candidate
//	training on|off                  enable (HUMAN ONLY: needs a terminal) or disable training
//	kill on|off                      the kill switch: "on" disables training, "off" enables
//	                                 it and needs a terminal
//	lab-set <profile>                make a candidate the lab's profile
//	rollback <lab|production> [--to NAME]
//	                                 restore the parent, or any ancestor, or "default"
//	promote <profile> --to production
//	                                 needs a signed approval naming the exact profile hash,
//	                                 a prior lab-set, a profile that carries its scorecards
//	                                 evidence path, and an explicit --trust (or
//	                                 TACTICSCTL_TRUST) directory outside --dir that root
//	                                 owns and nobody else can write. Each approval is spent
//	                                 by the promotion it signs off. With --store (or
//	                                 TRAINER_STORE) the evidence file must exist there;
//	                                 without it only its path is checked, and promote says so
//	approve <profile> --by NAME --key FILE [--valid-for D]
//	                                 HUMAN ONLY: refuses unless stdin is a real terminal.
//	                                 The signature covers the hash production runs now
//	                                 and an expiry (default 24h), and promote checks both
//	keygen --name NAME --out DIR     HUMAN ONLY: make an approver key pair; run it off the
//	                                 devbox, because agents share this account
//
// The trust directory holds the public keys approvals are checked against, so
// whoever can write it can approve anything. promote therefore refuses the
// default <dir>/trusted, a directory under --dir, and any directory (or parent,
// or key file) that root does not own or that group or others can write. That
// check is defence in depth, not a boundary: agents on the devbox run as the
// owner's uid with passwordless sudo, so only a private key that never touched
// this box makes an approval mean anything. One-time setup, by a human:
//
//	sudo install -d -o root -m 0755 /etc/crew-tactics-trust
//	sudo install -m 0644 owner.pub /etc/crew-tactics-trust/owner.pub
//
// and then run with TACTICSCTL_TRUST=/etc/crew-tactics-trust. Each approval
// is single-use.
//
// Training is disabled unless the kill-switch file says "on": a missing file is
// disabled, so a fresh or wiped state directory never trains. The terminal
// checks on enabling are accident guards; only the off direction is reliable.
// Only `promote` and `rollback production` write production; nothing else can.
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

// defaultApprovalTTL is long enough to approve, then promote in a working day,
// and short enough that a forgotten approval does not linger.
const defaultApprovalTTL = 24 * time.Hour

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, isTerminal(os.Stdin)); err != nil {
		fmt.Fprintln(os.Stderr, "tacticsctl:", strings.TrimPrefix(err.Error(), "tacticsctl: "))
		os.Exit(1)
	}
}

// parse lets flags follow positional arguments, as in `promote g7 --to production`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func run(args []string, stdin io.Reader, out io.Writer, tty bool) error {
	global := flag.NewFlagSet("tacticsctl", flag.ContinueOnError)
	dir := global.String("dir", tacticsctl.DefaultDir(), "control state directory (env TACTICSCTL_DIR)")
	trust := global.String("trust", os.Getenv("TACTICSCTL_TRUST"), "directory of approvers' public keys (env TACTICSCTL_TRUST); promote requires it, outside --dir and closed to group/others")
	evidence := global.String("store", os.Getenv("TRAINER_STORE"), "trainer store directory (env TRAINER_STORE); promote checks the evidence file exists there")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		return errors.New("a command is required: status, list, add, training, kill, lab-set, rollback, promote, approve, keygen")
	}
	s := tacticsctl.New(*dir, *trust)
	s.EvidenceStore = *evidence
	cmd, rest := rest[0], rest[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "status":
		asJSON := fs.Bool("json", false, "print JSON")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		return status(s, out, *asJSON)
	case "list":
		names, err := s.Candidates()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Fprintln(out, "no candidates")
		}
		for _, n := range names {
			fmt.Fprintln(out, n)
		}
		return nil
	case "add":
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 {
			return errors.New("usage: add <file>")
		}
		b, err := os.ReadFile(pos[0])
		if err != nil {
			return err
		}
		pr, err := tactics.Parse(b)
		if err != nil {
			return err
		}
		if err := s.AddCandidate(pr); err != nil {
			return err
		}
		fmt.Fprintf(out, "candidate %s filed\n", pr.Name)
		return nil
	case "training", "kill":
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
			return fmt.Errorf("usage: %s on|off", cmd)
		}
		enable := pos[0] == "on"
		if cmd == "kill" {
			enable = !enable
		}
		// Switching training off must always work, even from a script or an
		// agent; only switching it on is a human decision.
		if enable && !tty {
			return errors.New("enabling training is human-run only: it needs an interactive terminal on stdin")
		}
		if err := s.SetTraining(enable); err != nil {
			return err
		}
		fmt.Fprintf(out, "training %s\n", map[bool]string{true: "enabled", false: "disabled"}[enable])
		return nil
	case "lab-set":
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 {
			return errors.New("usage: lab-set <profile>")
		}
		if err := s.LabSet(pos[0]); err != nil {
			return err
		}
		fmt.Fprintf(out, "lab now runs %s\n", pos[0])
		return nil
	case "rollback":
		to := fs.String("to", "", "ancestor to restore, or \"default\"")
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 {
			return errors.New("usage: rollback <lab|production> [--to NAME]")
		}
		if err := s.Rollback(tacticsctl.Stage(pos[0]), *to); err != nil {
			return err
		}
		p, _, _ := s.Pointer(tacticsctl.Stage(pos[0]))
		fmt.Fprintf(out, "%s now runs %s\n", pos[0], p.Name)
		return nil
	case "promote":
		to := fs.String("to", "", "target stage; only \"production\"")
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 || *to != "production" {
			return errors.New("usage: promote <profile> --to production")
		}
		if s.EvidenceStore == "" {
			fmt.Fprintln(out, "note: no --store or TRAINER_STORE, so the evidence file's existence is not checked")
		}
		if err := s.Promote(pos[0]); err != nil {
			return err
		}
		fmt.Fprintf(out, "production now runs %s\n", pos[0])
		return nil
	case "approve":
		return approve(s, fs, rest, stdin, out, tty)
	case "keygen":
		return keygen(fs, rest, out, tty)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func status(s *tacticsctl.Store, out io.Writer, asJSON bool) error {
	st := s.Status()
	if asJSON {
		return json.NewEncoder(out).Encode(st)
	}
	fmt.Fprintf(out, "training: %s\n", map[bool]string{true: "enabled", false: "disabled"}[st.Training])
	for _, ss := range st.Stages {
		fmt.Fprintf(out, "%s: %s", ss.Stage, ss.Live)
		if len(ss.Chain) > 1 {
			fmt.Fprintf(out, " (chain %s)", strings.Join(ss.Chain, " <- "))
		}
		if ss.Since != "" {
			fmt.Fprintf(out, " since %s", ss.Since)
		}
		if ss.Error != "" {
			fmt.Fprintf(out, " ERROR %s", ss.Error)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "candidates: %d\n", len(st.Candidates))
	return nil
}

func approve(s *tacticsctl.Store, fs *flag.FlagSet, args []string, stdin io.Reader, out io.Writer, tty bool) error {
	by := fs.String("by", "", "approver name (its public key must be trusted)")
	keyFile := fs.String("key", "", "approver's private key file")
	validFor := fs.Duration("valid-for", defaultApprovalTTL, "how long the approval stays usable")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 || *by == "" || *keyFile == "" {
		return errors.New("usage: approve <profile> --by NAME --key FILE [--valid-for DURATION]")
	}
	if *validFor <= 0 {
		return errors.New("--valid-for must be positive")
	}
	if !tty {
		return errors.New("approve is human-run only: it needs an interactive terminal on stdin")
	}
	pr, err := s.Candidate(pos[0])
	if err != nil {
		return err
	}
	h, err := tacticsctl.Hash(pr)
	if err != nil {
		return err
	}
	priv, err := readKey(*keyFile)
	if err != nil {
		return err
	}
	prev, err := s.ProductionHash()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "approve profile %s (parent %q), replacing production %s, valid for %s\nhash %s\ntype the first 12 characters of the hash to approve: ", pr.Name, pr.Parent, prev, *validFor, h)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(line) != h[:12] {
		return errors.New("confirmation did not match; nothing approved")
	}
	now := time.Now().UTC()
	a := tacticsctl.NewApproval(priv, pr.Name, h, *by, now.Format(time.RFC3339), prev, now.Add(*validFor).Format(time.RFC3339))
	if err := s.SaveApproval(a); err != nil {
		return err
	}
	fmt.Fprintf(out, "approved %s as %s\n", pr.Name, *by)
	return nil
}

func readKey(path string) (ed25519.PrivateKey, error) {
	f, err := tacticsctl.OpenPrivate(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is not a tacticsctl private key", path)
	}
	return ed25519.PrivateKey(raw), nil
}

var approverNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

func keygen(fs *flag.FlagSet, args []string, out io.Writer, tty bool) error {
	name := fs.String("name", "", "approver name")
	outDir := fs.String("out", "", "directory to write <name>.key (0600) and <name>.pub")
	if _, err := parse(fs, args); err != nil || *name == "" || *outDir == "" {
		return errors.New("usage: keygen --name NAME --out DIR")
	}
	if !tty {
		return errors.New("keygen is human-run only: it needs an interactive terminal on stdin")
	}
	if !approverNameRE.MatchString(*name) {
		return fmt.Errorf("approver name %q must match %s", *name, approverNameRE)
	}
	fmt.Fprintln(out, "warning: generate approver keys off the devbox (your laptop, or an ed25519-sk/FIDO key); a key made here is readable by the agents that share this account")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(*outDir, *name+".key")
	pubPath := filepath.Join(*outDir, *name+".pub")
	if err := createNew(keyPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	if err := createNew(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		os.Remove(keyPath)
		return err
	}
	fmt.Fprintf(out, "wrote %s (keep private) and %s (a human installs it into the trust directory)\n", keyPath, pubPath)
	return nil
}

// createNew writes a file that must not exist yet, so an existing approver
// key is never replaced and a planted symlink is never followed.
func createNew(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; refusing to overwrite it", path)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		err = errors.Join(err, f.Close())
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
