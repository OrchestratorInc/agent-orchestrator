package reviewpolicy

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// submitJSONLiteral is a single-quoted shell literal, with quote-backslash-
// quote-quote for embedded single quotes, exactly as the review prompt
// instructs. `printf '%s'` performs no format interpretation and a
// single-quoted operand cannot expand or run anything.
const submitJSONLiteral = `'[^']*(?:'\\''[^']*)*'`

// submitPipePattern matches the only two piped commands a reviewer may run:
// the review JSON fed through `printf '%s'` into either the GitHub review POST
// or `ao review submit`. The captured session must match the grant.
var submitPipePattern = regexp.MustCompile(`^printf '%s' ` + submitJSONLiteral + ` \| (?:` +
	`gh api --method POST repos/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pulls/[0-9]+/reviews --input - --jq '\.id'` +
	`|ao review submit --session (?P<session>[A-Za-z0-9_-]+) --reviews -)$`)

// readOnlyGitSubcommands only inspect history and the working tree.
var readOnlyGitSubcommands = []string{
	"status", "diff", "log", "show", "blame", "grep", "ls-files", "ls-tree",
	"rev-parse", "rev-list", "merge-base", "cat-file", "describe", "shortlog",
}

// forbiddenGitArgPrefixes are read-subcommand options that write files
// (--output), run programs (--ext-diff, --textconv, -O/--open-files-in-pager),
// or would let a later option redirect git elsewhere.
var forbiddenGitArgPrefixes = []string{
	"--output", "--ext-diff", "--textconv", "-O", "--open-files-in-pager", "--exec",
}

// readOnlyGHSubcommands are the `gh pr` subcommands that only read.
var readOnlyGHSubcommands = []string{"view", "diff", "checks"}

// readOnlyUtilities print data and cannot write once redirection is
// rejected by the tokenizer. Codex reads code through the shell, so the
// search and listing tools it reaches for are here too.
var readOnlyUtilities = []string{"ls", "cat", "head", "tail", "wc", "pwd", "echo", "printf", "true", "grep", "nl"}

// forbiddenFindArgs are find actions that run programs or write files.
var forbiddenFindArgs = []string{"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls"}

// sedPrintScript is the only sed program a reviewer may run: print a line
// range, as in `sed -n '1,200p' file`. Any other script could write (`w`)
// or edit in place.
var sedPrintScript = regexp.MustCompile(`^(?:\d+|\$)(?:,(?:\d+|\$))?p$`)

// ReadOnlyShellPrefixes lists the command prefixes the policy admits, for
// harnesses whose native permission engine matches command prefixes instead
// of asking Evaluate. Prefix engines cannot see options such as --output, so
// those harnesses must also deny the fragments in ForbiddenShellFragments.
func ReadOnlyShellPrefixes() []string {
	prefixes := make([]string, 0, len(readOnlyGitSubcommands)+len(readOnlyGHSubcommands)+1)
	for _, sub := range readOnlyGitSubcommands {
		prefixes = append(prefixes, "git "+sub)
	}
	for _, sub := range readOnlyGHSubcommands {
		prefixes = append(prefixes, "gh pr "+sub)
	}
	return append(prefixes, "ao review submit")
}

// ForbiddenShellFragments are substrings that make an otherwise admitted
// prefix unsafe: shell control operators, redirection, substitution, and the
// git options that write files or run programs.
func ForbiddenShellFragments() []string {
	operators := []string{";", "&", "|", ">", "<", "$(", "`", "\n"}
	fragments := make([]string, 0, len(operators)+len(forbiddenGitArgPrefixes))
	fragments = append(fragments, operators...)
	return append(fragments, forbiddenGitArgPrefixes...)
}

func evaluateShell(command string, grant Grant) Decision {
	if command == "" {
		return deny("An empty shell command is not allowed.")
	}
	if m := submitPipePattern.FindStringSubmatch(command); m != nil {
		session := m[submitPipePattern.SubexpIndex("session")]
		if session != "" && !grantsWorker(grant, session) {
			return deny("The submit command names worker session " + session + ", which is not the session under review.")
		}
		if grant.WorkerSessionID == "" {
			return deny("This reviewer has no worker session to submit for.")
		}
		return allow()
	}
	argv, err := splitCommand(command)
	if err != nil {
		return deny("The shell command was blocked: " + err.Error() + ".")
	}
	return evaluateArgv(argv, grant)
}

func evaluateArgv(argv []string, grant Grant) Decision {
	switch argv[0] {
	case "git":
		return evaluateGit(argv)
	case "gh":
		if len(argv) >= 3 && argv[1] == "pr" && slices.Contains(readOnlyGHSubcommands, argv[2]) {
			return allow()
		}
		return deny("Only `gh pr view`, `gh pr diff` and `gh pr checks` are allowed; the review itself is posted with the exact submit command from the task.")
	case "ao":
		return evaluateAO(argv, grant)
	case "rg":
		return evaluateRipgrep(argv)
	case "sed":
		if len(argv) >= 3 && argv[1] == "-n" && sedPrintScript.MatchString(argv[2]) && !hasOption(argv[3:]) {
			return allow()
		}
		return deny("Only `sed -n 'START,ENDp' FILE` is allowed; other sed programs can write or edit files.")
	case "find":
		for _, arg := range argv[1:] {
			if slices.Contains(forbiddenFindArgs, arg) {
				return deny("find " + arg + " is not allowed: it runs programs or writes files.")
			}
		}
		return allow()
	}
	if slices.Contains(readOnlyUtilities, argv[0]) {
		return allow()
	}
	return deny("`" + argv[0] + "` is not a reviewer command.")
}

func evaluateGit(argv []string) Decision {
	i := 1
	if i < len(argv) && argv[i] == "--no-pager" {
		i++
	}
	if i >= len(argv) {
		return deny("`git` needs a read-only subcommand.")
	}
	sub := argv[i]
	if strings.HasPrefix(sub, "-") {
		return deny("git global options such as " + sub + " are not allowed; run git from the checkout without -C, --git-dir, --work-tree or -c.")
	}
	if !slices.Contains(readOnlyGitSubcommands, sub) {
		return deny("`git " + sub + "` is not a read-only git subcommand.")
	}
	for _, arg := range argv[i+1:] {
		for _, prefix := range forbiddenGitArgPrefixes {
			if strings.HasPrefix(arg, prefix) {
				return deny("The git option " + arg + " is not allowed: it writes files or runs programs.")
			}
		}
	}
	return allow()
}

// evaluateRipgrep admits searches but not the options that run other
// programs: --pre/--pre-glob preprocessors and -z/--search-zip decompressors.
func evaluateRipgrep(argv []string) Decision {
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "--pre") || arg == "--search-zip" ||
			(strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "z")) {
			return deny("The rg option " + arg + " is not allowed: it runs other programs.")
		}
	}
	return allow()
}

func hasOption(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return true
		}
	}
	return false
}

func evaluateAO(argv []string, grant Grant) Decision {
	if len(argv) < 3 || argv[1] != "review" || argv[2] != "submit" {
		return deny("Reviewers may run only `ao review submit`; they never spawn, message, stop or manage sessions.")
	}
	session := ""
	for i := 3; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--session" && i+1 < len(argv):
			session = argv[i+1]
			i++
		case strings.HasPrefix(arg, "--session="):
			session = strings.TrimPrefix(arg, "--session=")
		case !strings.HasPrefix(arg, "-") && session == "":
			session = arg
		}
	}
	if !grantsWorker(grant, session) {
		return deny("`ao review submit` must name the worker session under review with --session.")
	}
	return allow()
}

func grantsWorker(grant Grant, session string) bool {
	return grant.WorkerSessionID != "" && session == grant.WorkerSessionID
}

// splitCommand tokenizes one simple shell command. It fails closed: any
// construct that could chain, redirect, substitute, expand or glob is
// rejected rather than interpreted, so an admitted argv is exactly what runs.
func splitCommand(command string) ([]string, error) {
	if strings.ContainsAny(command, "\n\r\x00") {
		return nil, errors.New("run one command per call")
	}
	var (
		argv    []string
		current strings.Builder
		inWord  bool
	)
	flush := func() {
		if inWord {
			argv = append(argv, current.String())
			current.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case ch == ' ' || ch == '\t':
			flush()
		case ch == '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			current.WriteString(command[i+1 : i+1+end])
			inWord = true
			i += end + 1
		case ch == '"':
			end := strings.IndexByte(command[i+1:], '"')
			if end < 0 {
				return nil, errors.New("unterminated double quote")
			}
			quoted := command[i+1 : i+1+end]
			if strings.ContainsAny(quoted, "$`\\!") {
				return nil, errors.New("expansions inside double quotes are not allowed")
			}
			current.WriteString(quoted)
			inWord = true
			i += end + 1
		case strings.IndexByte(";&|<>()$`\\*?[]{}!", ch) >= 0:
			return nil, errors.New("shell operators, redirection, substitution and globs are not allowed (" + string(ch) + ")")
		case (ch == '~' || ch == '#') && !inWord:
			return nil, errors.New("a word may not start with " + string(ch))
		default:
			current.WriteByte(ch)
			inWord = true
		}
	}
	flush()
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	return argv, nil
}
