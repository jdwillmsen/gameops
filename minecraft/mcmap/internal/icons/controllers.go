package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Bounds on the render controllers: 173 files of 11 KB at most, each
// holding one to a dozen controllers whose expressions are a line long.
const (
	maxControllerBytes = 128 << 10
	maxControllerFiles = 600
	maxControllers     = 2000
	maxExpression      = 1024
	maxExpressionDepth = 24
	maxArrayEntries    = 256
	// maxArrays is how many arrays a controller may define, and
	// maxFileControllers how many controllers one file may hold.
	maxArrays          = 64
	maxFileControllers = 64
	// How much work reading may take, in steps: one for every operand and
	// every operator. The real expressions take under fifty and a real
	// file under two thousand; an expression can name an array whose
	// entries are expressions naming arrays, and without a count of the
	// work, and a memory of what each entry came to, a file of a few
	// kilobytes takes days to read.
	maxExpressionSteps = 4_000
	maxFileSteps       = 200_000
)

// errSpent is why an expression was not read to its end.
var errSpent = errors.New("a render controller took more work to read than any is allowed")

// effort is the work the controllers of one file have left to be read
// with, and what has been worked out of them already.
type effort struct {
	ctx context.Context
	// file is the steps left for the whole file, and expression those left
	// for the expression being read from its start.
	file, expression int
	// spent is set once either runs out or the context ends. Nothing more
	// is read from the file after that: its mobs fall back to what their
	// definitions call default.
	spent bool
	// entries is what each entry of an array came to, read once however
	// many times it is named, and reading marks the ones being read now,
	// which an entry that names itself would otherwise read for ever.
	entries map[string]value
	reading map[string]bool
}

func newEffort(ctx context.Context) *effort {
	return &effort{ctx: ctx, file: maxFileSteps, entries: map[string]value{}, reading: map[string]bool{}}
}

// step spends one step and reports whether there was one to spend.
func (e *effort) step() bool {
	if e.spent {
		return false
	}
	e.file--
	e.expression--
	// The context is asked every so often and not at every step.
	if e.file <= 0 || e.expression <= 0 || (e.file%256 == 0 && e.ctx.Err() != nil) {
		e.spent = true
	}
	return !e.spent
}

var controllerID = regexp.MustCompile(`^controller\.render\.[A-Za-z0-9_.-]{1,96}$`)

// controller is the part of a render controller that says which model and
// which textures a mob is drawn with. Each is an expression in the game's
// own small language, over what the mob is at that moment: a baby, a
// variant, sheared.
type controller struct {
	Geometry string   `json:"geometry"`
	Textures []string `json:"textures"`
	// Visibility is which bones are drawn, as rules read in order: each
	// names bones, with * for any run of letters, and says yes, no or an
	// expression. A horse's saddle is drawn only on a saddled one.
	Visibility []map[string]json.RawMessage `json:"part_visibility"`

	// id is the controller's name, which tells its arrays from those of
	// the same name in another controller of the file, and work is shared
	// by every controller of one file.
	id     string
	work   *effort
	Arrays struct {
		Textures   map[string][]string `json:"textures"`
		Geometries map[string][]string `json:"geometries"`
	} `json:"arrays"`
}

// parseControllers reads every controller in one file, by name.
func parseControllers(ctx context.Context, raw []byte) (map[string]controller, error) {
	if len(raw) > maxControllerBytes {
		return nil, fmt.Errorf("the controller file is %d bytes, over the limit of %d", len(raw), maxControllerBytes)
	}
	raw = stripComments(raw)
	if !jsonWithin(raw, maxJSONDepth) {
		return nil, fmt.Errorf("the controller file is nested deeper than %d", maxJSONDepth)
	}
	var file struct {
		Controllers map[string]json.RawMessage `json:"render_controllers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if len(file.Controllers) > maxFileControllers {
		return nil, fmt.Errorf("the controller file holds %d controllers, over the limit of %d", len(file.Controllers), maxFileControllers)
	}
	out := map[string]controller{}
	work := newEffort(ctx)
	for id, body := range file.Controllers {
		var c controller
		// One controller in a shape this does not read is left out, and
		// the mobs that use it fall back to their own default.
		if !controllerID.MatchString(id) || json.Unmarshal(body, &c) != nil || len(c.Textures) > maxArrayEntries ||
			len(c.Arrays.Textures)+len(c.Arrays.Geometries) > maxArrays || len(c.Visibility) > maxArrayEntries {
			continue
		}
		c.id, c.work = id, work
		out[id] = c
	}
	return out, nil
}

// hides reports whether the controller leaves a bone undrawn on a grown
// mob of the default variant. The last rule that names the bone decides.
func (c controller) hides(bone string) bool {
	if c.work != nil && c.work.spent {
		return false
	}
	shown := true
	for _, rule := range c.Visibility[:min(len(c.Visibility), maxArrayEntries)] {
		for pattern, raw := range rule {
			if ok, err := path.Match(pattern, bone); err != nil || !ok {
				continue
			}
			var flag bool
			var expression string
			switch {
			case json.Unmarshal(raw, &flag) == nil:
				shown = flag
			case json.Unmarshal(raw, &expression) == nil:
				v, err := c.evaluate(expression, 0)
				shown = err == nil && v.truthy()
			}
		}
	}
	return !shown
}

// array is the list an Array.<name> names, whichever of the two kinds it
// is. The files are not consistent about its capitals.
func (c controller) array(name string) []string {
	for _, group := range []map[string][]string{c.Arrays.Textures, c.Arrays.Geometries} {
		for key, list := range group {
			if strings.EqualFold(key, name) && len(list) <= maxArrayEntries {
				return list
			}
		}
	}
	return nil
}

// pick works out which texture or model an expression chooses for a grown
// mob of the default variant: every query and variable is nought, so it is
// no baby, variant 0, not sheared, not angry. The answer is the name after
// Texture. or Geometry., which is a key in the mob's own definition. ok is
// false for an expression that does not come to one.
func (c controller) pick(expression string) (name string, ok bool) {
	v, err := c.evaluate(expression, 0)
	if err != nil || v.ref == "" {
		return "", false
	}
	_, name, _ = strings.Cut(v.ref, ".")
	return name, name != ""
}

// holds reports whether a condition is true of that same default mob. One
// that cannot be read is taken as not holding, which leaves out a layer
// rather than drawing one that may only be for a mob on fire.
func holds(condition string) bool {
	v, err := controller{}.evaluate(condition, 0)
	return err == nil && v.truthy()
}

// value is a number, a word in quotes, or a reference to a texture, a
// model or a material.
type value struct {
	num  float64
	text string
	ref  string
}

func (v value) truthy() bool { return v.ref != "" || v.text != "" || v.num != 0 }

func (c controller) evaluate(expression string, depth int) (value, error) {
	if len(expression) > maxExpression || depth > maxExpressionDepth {
		return value{}, fmt.Errorf("an expression is too long or too deep")
	}
	// A controller made by hand, and a condition that is no controller's,
	// has an allowance of its own.
	if c.work == nil {
		c.work = newEffort(context.Background())
	}
	if depth == 0 {
		c.work.expression = maxExpressionSteps
	}
	if c.work.spent || c.work.ctx.Err() != nil {
		c.work.spent = true
		return value{}, errSpent
	}
	p := &expr{in: expression, of: c, depth: depth}
	v := p.ternary()
	p.space()
	if p.err == nil && p.at < len(p.in) {
		p.err = fmt.Errorf("an expression does not end where it should")
	}
	return v, p.err
}

type expr struct {
	in    string
	at    int
	of    controller
	depth int
	err   error
}

func (p *expr) space() {
	for p.at < len(p.in) && strings.ContainsRune(" \t\r\n", rune(p.in[p.at])) {
		p.at++
	}
}

func (p *expr) eat(token string) bool {
	p.space()
	if !strings.HasPrefix(p.in[p.at:], token) {
		return false
	}
	p.at += len(token)
	return true
}

// deeper counts one more level of nesting and reports whether that is
// still within bounds; every rule that can recurse asks it.
func (p *expr) deeper() bool {
	if p.depth++; p.depth > maxExpressionDepth && p.err == nil {
		p.err = fmt.Errorf("an expression is nested too deep")
	}
	return p.err == nil
}

func truth(b bool) value {
	if b {
		return value{num: 1}
	}
	return value{}
}

func (p *expr) ternary() value {
	if !p.deeper() {
		return value{}
	}
	defer func() { p.depth-- }()
	cond := p.binary(0)
	if !p.eat("?") {
		return cond
	}
	yes := p.ternary()
	if !p.eat(":") {
		// The language allows a ? b with no else, which is nothing when
		// the condition fails.
		if cond.truthy() {
			return yes
		}
		return value{}
	}
	no := p.ternary()
	if cond.truthy() {
		return yes
	}
	return no
}

// The operators with two sides, loosest first.
var operators = [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<=", ">=", "<", ">"}, {"+", "-"}, {"*", "/"}}

func (p *expr) binary(level int) value {
	if level == len(operators) {
		return p.unary()
	}
	left := p.binary(level + 1)
	for p.err == nil {
		op := ""
		for _, candidate := range operators[level] {
			if p.eat(candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			return left
		}
		right := p.binary(level + 1)
		switch op {
		case "||":
			left = truth(left.truthy() || right.truthy())
		case "&&":
			left = truth(left.truthy() && right.truthy())
		case "==":
			left = truth(left == right)
		case "!=":
			left = truth(left != right)
		case "<":
			left = truth(left.num < right.num)
		case ">":
			left = truth(left.num > right.num)
		case "<=":
			left = truth(left.num <= right.num)
		case ">=":
			left = truth(left.num >= right.num)
		case "+":
			left = value{num: left.num + right.num}
		case "-":
			left = value{num: left.num - right.num}
		case "*":
			left = value{num: left.num * right.num}
		case "/":
			if right.num == 0 {
				left = value{}
			} else {
				left = value{num: left.num / right.num}
			}
		}
	}
	return left
}

func (p *expr) unary() value {
	if !p.deeper() {
		return value{}
	}
	defer func() { p.depth-- }()
	if p.eat("!") {
		return truth(!p.unary().truthy())
	}
	if p.eat("-") {
		return value{num: -p.unary().num}
	}
	return p.primary()
}

func (p *expr) primary() value {
	p.space()
	if !p.of.work.step() {
		if p.err == nil {
			p.err = errSpent
		}
		return value{}
	}
	if p.at >= len(p.in) {
		p.err = fmt.Errorf("an expression ends early")
		return value{}
	}
	switch c := p.in[p.at]; {
	case c == '(':
		p.at++
		v := p.ternary()
		if !p.eat(")") && p.err == nil {
			p.err = fmt.Errorf("an expression has a bracket left open")
		}
		return v
	case c == '\'':
		end := strings.IndexByte(p.in[p.at+1:], '\'')
		if end < 0 {
			p.err = fmt.Errorf("an expression has a quote left open")
			return value{}
		}
		text := p.in[p.at+1 : p.at+1+end]
		p.at += end + 2
		return value{text: text}
	case c >= '0' && c <= '9' || c == '.':
		start := p.at
		for p.at < len(p.in) && (p.in[p.at] >= '0' && p.in[p.at] <= '9' || p.in[p.at] == '.') {
			p.at++
		}
		// A number may carry the f the game's own files write after it.
		n, err := strconv.ParseFloat(p.in[start:p.at], 64)
		if err != nil || math.IsInf(n, 0) {
			p.err = fmt.Errorf("an expression has a number that is not one")
		}
		p.eat("f")
		return value{num: n}
	case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		return p.named()
	}
	p.err = fmt.Errorf("an expression has a character it should not")
	return value{}
}

// named is a dotted name with whatever follows it: the arguments of a
// query, which are read and dropped, or the index into an array.
func (p *expr) named() value {
	start := p.at
	for p.at < len(p.in) {
		c := p.in[p.at]
		if c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			break
		}
		p.at++
	}
	name := p.in[start:p.at]
	kind, _, _ := strings.Cut(strings.ToLower(name), ".")
	if p.eat("(") {
		for !p.eat(")") && p.err == nil {
			p.ternary()
			if !p.eat(",") && !strings.HasPrefix(strings.TrimLeft(p.in[p.at:], " \t"), ")") && p.err == nil {
				p.err = fmt.Errorf("an expression has a bracket left open")
			}
		}
	}
	switch kind {
	case "texture", "geometry", "material":
		return value{ref: name}
	case "array":
		if !p.eat("[") {
			p.err = fmt.Errorf("an array is named without an index")
			return value{}
		}
		index := p.ternary()
		if !p.eat("]") && p.err == nil {
			p.err = fmt.Errorf("an expression has a bracket left open")
		}
		list := p.of.array(name)
		if p.err != nil || len(list) == 0 {
			return value{}
		}
		// Sums of numbers that are each within range can still run off
		// the end of them, and what is left is no place in a list.
		if math.IsNaN(index.num) || math.IsInf(index.num, 0) {
			p.err = fmt.Errorf("an array is indexed by what is not a number")
			return value{}
		}
		// The game counts round an array from either end.
		i := int(math.Mod(math.Trunc(index.num), float64(len(list))))
		if i < 0 {
			i += len(list)
		}
		// Each entry is read once: an expression may name the same one
		// eighty times, and its entries theirs.
		work, entry := p.of.work, p.of.id+"\x00"+strings.ToLower(name)+"\x00"+strconv.Itoa(i)
		if v, read := work.entries[entry]; read {
			return v
		}
		if work.reading[entry] {
			p.err = fmt.Errorf("an array's entry names itself")
			return value{}
		}
		work.reading[entry] = true
		v, err := p.of.evaluate(list[i], p.depth+1)
		delete(work.reading, entry)
		if err != nil {
			p.err = err
			return value{}
		}
		work.entries[entry] = v
		return v
	}
	// A query, a variable or a function: nought, which is what each is
	// for a mob that is nothing in particular.
	return value{}
}
