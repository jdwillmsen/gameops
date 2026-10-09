package icons

import (
	"encoding/json"
	"fmt"
	"math"
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
)

var controllerID = regexp.MustCompile(`^controller\.render\.[A-Za-z0-9_.-]{1,96}$`)

// controller is the part of a render controller that says which model and
// which textures a mob is drawn with. Each is an expression in the game's
// own small language, over what the mob is at that moment: a baby, a
// variant, sheared.
type controller struct {
	Geometry string   `json:"geometry"`
	Textures []string `json:"textures"`
	Arrays   struct {
		Textures   map[string][]string `json:"textures"`
		Geometries map[string][]string `json:"geometries"`
	} `json:"arrays"`
}

// parseControllers reads every controller in one file, by name.
func parseControllers(raw []byte) (map[string]controller, error) {
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
	out := map[string]controller{}
	for id, body := range file.Controllers {
		var c controller
		// One controller in a shape this does not read is left out, and
		// the mobs that use it fall back to their own default.
		if !controllerID.MatchString(id) || json.Unmarshal(body, &c) != nil || len(c.Textures) > maxArrayEntries {
			continue
		}
		out[id] = c
	}
	return out, nil
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
		// The game counts round an array from either end.
		i := int(math.Mod(math.Trunc(index.num), float64(len(list))))
		if i < 0 {
			i += len(list)
		}
		v, err := p.of.evaluate(list[i], p.depth+1)
		if err != nil {
			p.err = err
		}
		return v
	}
	// A query, a variable or a function: nought, which is what each is
	// for a mob that is nothing in particular.
	return value{}
}
