// Command checkrunpatterns fails when a `go test -run` pattern in a GitHub
// workflow no longer matches the tests it was written for.
//
// go test passes with "[no tests to run]" when a -run pattern matches
// nothing, so renaming or deleting a test silently drops it from any CI step
// that selects tests by name. This command reads every `go test ... -run`
// invocation in the given workflow files, splits each pattern into its
// alternatives (`^Test(A|B)$` becomes `^TestA$` and `^TestB$`), and requires
// every alternative to match at least one test function in the named
// packages. Test files are read regardless of build constraints, so tests
// that only build on Windows still count.
//
// Usage, from the backend module root:
//
//	go run ./cmd/checkrunpatterns ../.github/workflows/*.yml
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxAlternatives bounds pattern expansion so a pathological pattern cannot
// explode; patterns past it are checked as a whole instead.
const maxAlternatives = 256

// invocation is one `go test -run` command found in a workflow.
type invocation struct {
	source   string // file:line, for messages
	pattern  string
	packages []string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: checkrunpatterns WORKFLOW.yml...")
		os.Exit(2)
	}
	var problems []string
	checked := 0
	for _, path := range os.Args[1:] {
		invs, err := readInvocations(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, inv := range invs {
			checked++
			problems = append(problems, check(inv, ".")...)
		}
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "These go test -run patterns match no tests, so CI would skip them silently:")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		os.Exit(1)
	}
	fmt.Printf("checked %d go test -run invocations; every pattern matches a test\n", checked)
}

var (
	runFlag = regexp.MustCompile(`-run[= ](?:'([^']*)'|"([^"]*)"|([^\s;&|()]+))`)
	goTest  = regexp.MustCompile(`\bgo test\b`)
)

// readInvocations returns the go test commands with a -run flag in a
// workflow file. It decodes the YAML so folded and literal `run` scalars come
// back as the shell text GitHub runs, then joins shell line continuations.
func readInvocations(path string) ([]invocation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var invs []invocation
	for _, run := range runScripts(&doc) {
		script := strings.ReplaceAll(run.Value, "\\\n", " ")
		for _, cmd := range strings.Split(script, "\n") {
			if inv, ok := parseInvocation(cmd); ok {
				inv.source = fmt.Sprintf("%s:%d", path, run.Line)
				invs = append(invs, inv)
			}
		}
	}
	return invs, nil
}

// runScripts returns every scalar value stored under a `run` key.
func runScripts(n *yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if key, val := n.Content[i], n.Content[i+1]; key.Value == "run" && val.Kind == yaml.ScalarNode {
				out = append(out, val)
			}
		}
	}
	for _, child := range n.Content {
		out = append(out, runScripts(child)...)
	}
	return out
}

func parseInvocation(cmd string) (invocation, bool) {
	loc := goTest.FindStringIndex(cmd)
	if loc == nil {
		return invocation{}, false
	}
	cmd = cmd[loc[1]:]
	m := runFlag.FindStringSubmatch(cmd)
	if m == nil {
		return invocation{}, false
	}
	inv := invocation{pattern: m[1] + m[2] + m[3]}
	for _, field := range strings.Fields(cmd) {
		field = strings.Trim(field, "()")
		if strings.HasPrefix(field, "./") {
			inv.packages = append(inv.packages, field)
		}
	}
	return inv, len(inv.packages) > 0
}

// check reports each alternative of inv's pattern that matches no test in its
// packages. Package paths resolve against root.
func check(inv invocation, root string) []string {
	names, err := testNames(root, inv.packages)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", inv.source, err)}
	}
	alts, err := alternatives(inv.pattern)
	if err != nil {
		return []string{fmt.Sprintf("%s: invalid -run pattern %q: %v", inv.source, inv.pattern, err)}
	}
	var problems []string
	for _, alt := range alts {
		re, err := regexp.Compile(alt)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: invalid alternative %q: %v", inv.source, alt, err))
			continue
		}
		if !anyMatch(re, names) {
			problems = append(problems, fmt.Sprintf("%s: %q matches no test in %s", inv.source, alt, strings.Join(inv.packages, " ")))
		}
	}
	return problems
}

func anyMatch(re *regexp.Regexp, names []string) bool {
	for _, n := range names {
		if re.MatchString(n) {
			return true
		}
	}
	return false
}

// alternatives splits a -run pattern into the regexps its alternation stands for,
// distributing alternation over concatenation: `^Test(A|B)$` becomes
// `^TestA$` and `^TestB$`. It works on the pattern text rather than a parsed
// regexp because regexp/syntax folds `A|B` into the class `[AB]`. A group
// followed by a quantifier is kept whole.
func alternatives(pattern string) ([]string, error) {
	if _, err := syntax.Parse(pattern, syntax.Perl); err != nil {
		return nil, err
	}
	// Like go test, split top-level alternatives first; in each, the part
	// before the first unbracketed slash names the test function and the
	// rest selects subtests.
	var alts []string
	for _, branch := range splitTopLevel(pattern) {
		alts = append(alts, expand(cutSubtests(branch))...)
		if len(alts) > maxAlternatives {
			return []string{pattern}, nil
		}
	}
	return alts, nil
}

// cutSubtests returns the part of a branch before its first slash outside
// groups, classes and escapes.
func cutSubtests(branch string) string {
	depth := 0
	for i := 0; i < len(branch); i++ {
		switch branch[i] {
		case '\\':
			i++
		case '[':
			i = classEnd(branch, i)
		case '(':
			depth++
		case ')':
			depth--
		case '/':
			if depth == 0 {
				return branch[:i]
			}
		}
	}
	return branch
}

// expand returns the alternatives of a syntactically valid pattern.
func expand(pattern string) []string {
	var out []string
	for _, branch := range splitTopLevel(pattern) {
		product := []string{""}
		for _, seg := range segments(branch) {
			opts := []string{seg}
			if inner, ok := expandableGroup(seg); ok {
				opts = expand(inner)
			}
			next := make([]string, 0, len(product)*len(opts))
			for _, prefix := range product {
				for _, opt := range opts {
					next = append(next, prefix+opt)
				}
			}
			if len(next) > maxAlternatives {
				return []string{pattern}
			}
			product = next
		}
		out = append(out, product...)
	}
	return out
}

// splitTopLevel splits a pattern on the | characters outside groups,
// classes and escapes.
func splitTopLevel(pattern string) []string {
	var parts []string
	depth, last := 0, 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = classEnd(pattern, i)
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				parts = append(parts, pattern[last:i])
				last = i + 1
			}
		}
	}
	return append(parts, pattern[last:])
}

// segments cuts a branch into atoms: whole groups (with any quantifier that
// follows them), classes, escapes and single characters.
func segments(branch string) []string {
	var segs []string
	for i := 0; i < len(branch); {
		j := i + 1
		switch branch[i] {
		case '\\':
			j = i + 2
		case '[':
			j = classEnd(branch, i) + 1
		case '(':
			depth := 0
			for j = i; j < len(branch); j++ {
				if branch[j] == '\\' {
					j++
					continue
				}
				if branch[j] == '[' {
					j = classEnd(branch, j)
					continue
				}
				if branch[j] == '(' {
					depth++
				} else if branch[j] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			j++
		}
		for j < len(branch) && strings.ContainsRune("*+?{", rune(branch[j])) {
			if branch[j] == '{' {
				if k := strings.IndexByte(branch[j:], '}'); k >= 0 {
					j += k
				}
			}
			j++
		}
		if j > len(branch) {
			j = len(branch)
		}
		segs = append(segs, branch[i:j])
		i = j
	}
	return segs
}

// expandableGroup returns the body of a plain or non-capturing group with no
// quantifier, the only groups whose alternatives can be distributed.
func expandableGroup(seg string) (string, bool) {
	if len(seg) < 2 || seg[0] != '(' || seg[len(seg)-1] != ')' {
		return "", false
	}
	inner := seg[1 : len(seg)-1]
	if strings.HasPrefix(inner, "?:") {
		return inner[2:], true
	}
	if strings.HasPrefix(inner, "?") {
		return "", false
	}
	return inner, true
}

// classEnd returns the index of the ] that closes the class opening at i.
func classEnd(pattern string, i int) int {
	j := i + 1
	if j < len(pattern) && pattern[j] == '^' {
		j++
	}
	if j < len(pattern) && pattern[j] == ']' {
		j++
	}
	for ; j < len(pattern); j++ {
		switch pattern[j] {
		case '\\':
			j++
		case ']':
			return j
		}
	}
	return len(pattern) - 1
}

// testNames lists the Test, Example and Fuzz functions in the _test.go files
// of the given package paths. A path ending in /... includes subdirectories.
func testNames(root string, packages []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pkg := range packages {
		dir, recursive := strings.CutSuffix(pkg, "/...")
		dir = filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(dir, "/")))
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("package %s not found", pkg)
		}
		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != dir && (!recursive || strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			// go ignores files whose names start with _ or . .
			if name := d.Name(); !strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return nil
			}
			return addTestNames(path, seen)
		})
		if err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

var testFunc = regexp.MustCompile(`^(Test|Example|Fuzz)([^a-z]|$)`)

func addTestNames(path string, seen map[string]bool) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && testFunc.MatchString(fn.Name.Name) {
			seen[fn.Name.Name] = true
		}
	}
	return nil
}
