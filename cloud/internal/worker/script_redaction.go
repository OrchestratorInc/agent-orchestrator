package worker

import (
	"regexp"
	"strings"
)

// assignmentPattern matches a shell assignment (NAME=value, export NAME=value,
// declare -x NAME=value, local, readonly or typeset NAME=value, NAME=value
// before a command) at the start of a word or of a quoted string (echo
// 'TOKEN=x' > env.sh, which a later step sources): its groups are the name and the
// value, double-quoted, single-quoted or bare.
var assignmentPattern = regexp.MustCompile("(?:^|[\\s;&|(`\"'])(?:(?:export|declare|local|readonly|typeset)[ \t]+(?:-[A-Za-z]+[ \t]+)*)?([A-Za-z_][A-Za-z0-9_]*)=(\"(?:[^\"\\\\]|\\\\.)*\"|'[^']*'|[^\\s;&|)`'\"]*)")

// assignedSecretValues are the values commands assign to secret-named
// variables (isSecretName) in shell assignments, literally: a value that
// expands something ($, a backtick) is not known until it runs. A step that
// sets one prints it with nothing naming it ("$MY_SERVICE_SECRET"), and the
// steps after it may inherit it, so its run masks it wherever it appears.
func assignedSecretValues(commands []string) []string {
	var values []string
	for _, command := range commands {
		if !strings.Contains(command, "=") {
			continue
		}
		for _, match := range assignmentPattern.FindAllStringSubmatch(command, -1) {
			name, value := match[1], match[2]
			if !isSecretName(name) {
				continue
			}
			switch {
			case strings.HasPrefix(value, `"`):
				value = strings.ReplaceAll(strings.TrimSuffix(value[1:], `"`), `\"`, `"`)
			case strings.HasPrefix(value, "'"):
				value = strings.TrimSuffix(value[1:], "'")
			}
			if value == "" || strings.ContainsAny(value, "$`") {
				continue
			}
			values = append(values, value)
		}
	}
	return values
}

// The script redaction's passes for credentials a secret-named pair does not
// name: a YAML block scalar's lines, a password given to a known command in a
// short flag, a Kubernetes Secret's data, a token used as a URL's user, and a
// signed URL's signature. Each pass reads the text once.

// blockScalars finds the YAML block scalars among one text's pair values
// (private_key: | then its indented lines). Pairs are looked up in order, so
// it keeps the line it last read and the block it last found for a line, and
// each line of the text is read a bounded number of times however many pairs
// it holds.
type blockScalars struct {
	text string
	// lineStart and lineEnd bound the line last looked up (lineEnd is its
	// newline or the text's end; -1 before the first lookup).
	lineStart, lineEnd int
	// The block found for the pairs whose name and value are on nameLine and
	// valueLine (their starts): it ends at blockEnd, which is not past the
	// value line's end when there is none.
	known               bool
	nameLine, valueLine int
	blockEnd            int
}

func newBlockScalars(text string) *blockScalars {
	return &blockScalars{text: text, lineEnd: -1}
}

// line returns the start and end of the line holding position, which is never
// before the one last looked up.
func (b *blockScalars) line(position int) (int, int) {
	if position > b.lineEnd || position < b.lineStart {
		from := b.lineEnd + 1
		if position < b.lineStart {
			from = 0
		}
		b.lineStart = from + strings.LastIndexByte(b.text[from:position], '\n') + 1
		b.lineEnd = len(b.text)
		if index := strings.IndexByte(b.text[position:], '\n'); index >= 0 {
			b.lineEnd = position + index
		}
	}
	return b.lineStart, b.lineEnd
}

// end is where the block scalar that is the value at valueStart of the pair
// whose name starts at nameStart ends (the end of its last line indented
// deeper than the name's line), or valueStart when the value is not one or
// has no lines.
func (b *blockScalars) end(nameStart, valueStart int) int {
	nameLine, _ := b.line(nameStart)
	valueLine, valueEnd := b.line(valueStart)
	if valueEnd == len(b.text) || !isBlockScalarHeader(b.text[valueStart:valueEnd]) {
		return valueStart
	}
	if !b.known || b.nameLine != nameLine || b.valueLine != valueLine {
		b.known, b.nameLine, b.valueLine = true, nameLine, valueLine
		b.blockEnd = blockLinesEnd(b.text, valueEnd+1, indentation(b.text[nameLine:]))
	}
	if b.blockEnd <= valueEnd {
		return valueStart
	}
	return b.blockEnd
}

// isBlockScalarHeader reports whether the rest of a pair's line after its
// separator opens a YAML block scalar: | or > with its chomping and
// indentation indicators, then blanks or a comment.
func isBlockScalarHeader(rest string) bool {
	if rest == "" || (rest[0] != '|' && rest[0] != '>') {
		return false
	}
	index := 1
	for index < len(rest) && (rest[index] == '-' || rest[index] == '+' || (rest[index] >= '0' && rest[index] <= '9')) {
		index++
	}
	for index < len(rest) && (rest[index] == ' ' || rest[index] == '\t' || rest[index] == '\r') {
		index++
	}
	return index == len(rest) || rest[index] == '#'
}

// blockLinesEnd is the end of the last of the lines from position on that are
// indented deeper than keyIndent (blank lines between them included), or
// position - 1 when the first line that is not blank is not.
func blockLinesEnd(text string, position, keyIndent int) int {
	end := position - 1
	for position < len(text) {
		indent := indentation(text[position:])
		if next := position + indent; next < len(text) && text[next] != '\n' && text[next] != '\r' && indent <= keyIndent {
			break
		}
		lineEnd := len(text)
		if index := strings.IndexByte(text[position:], '\n'); index >= 0 {
			lineEnd = position + index
		}
		if strings.TrimSpace(text[position:lineEnd]) != "" {
			end = lineEnd
		}
		position = lineEnd + 1
	}
	return end
}

// maskedBlockScalar is a YAML block scalar value (its header line and its
// lines) with its lines replaced by one masked line at their indentation.
func maskedBlockScalar(block string) string {
	header, body, _ := strings.Cut(block, "\n")
	// A line's end as the block's last line had it (a command's printf \n
	// is read as \r\n, see redactCommand).
	end := ""
	if strings.HasSuffix(block, "\r") {
		end = "\r"
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) != "" {
			return header + "\n" + line[:indentation(line)] + redactedMarker + end
		}
	}
	return header
}

// indentation is the count of blanks text starts with (on its first line).
func indentation(text string) int {
	index := 0
	for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
		index++
	}
	return index
}

// passwordCommandPattern matches a command that takes a password, or a
// user:password, in a short flag (docker login -p x, mysql -px, sshpass -p x,
// redis-cli -a x, curl -u user:x), at the start of a command, an argument or
// after a path. Other commands' short flags are left alone: -p is a port to
// ssh and docker run.
var passwordCommandPattern = regexp.MustCompile("(?:^|[\\s;&|(`\"'/=])(docker|podman|nerdctl|buildah|skopeo|helm|oras|crane|sshpass|mysql|mariadb|mysqldump|mysqladmin|mysqlimport|mysqlshow|mysqlcheck|mariadb-dump|mariadb-admin|redis-cli|curl)[ \\t]")

// valueKind is how a password argument is masked.
type valueKind int

const (
	noValue valueKind = iota
	// wholeValue is a password.
	wholeValue
	// userValue is user:password, whose password is masked.
	userValue
)

// maskCommandPasswords masks the passwords passwordCommandPattern's commands
// take in a short flag, up to the end of each command.
func maskCommandPasswords(text string) string {
	var out strings.Builder
	last := 0
	quoting := quoteCursor{text: text}
	for position := 0; position < len(text); {
		match := passwordCommandPattern.FindStringSubmatchIndex(text[position:])
		if match == nil {
			break
		}
		command := text[position+match[2] : position+match[3]]
		// A command written inside a quoted string (echo "mysql -px") ends
		// with that string: its closing quote is not an argument's.
		enclosing := quoting.at(position + match[2])
		end := maskCommandArguments(text, position+match[1], command, enclosing, func(start, stop int, kind valueKind) {
			masked, ok := maskedArgument(text[start:stop], kind)
			if ok && start >= last {
				out.WriteString(text[last:start])
				out.WriteString(masked)
				last = stop
			}
		})
		// The command's end, or its name's for a name with no arguments (the
		// pattern consumed a blank after it).
		position = max(end, position+match[3])
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// maskCommandArguments reads command's arguments from position, up to the end
// of its shell segment (nextArgument) or of the quoted string it is written in
// (enclosing, its quote, or 0), and calls mask for each one that is a
// password, in order. It returns where the command ends.
func maskCommandArguments(text string, position int, command string, enclosing byte, mask func(start, stop int, kind valueKind)) int {
	login := false
	pending, skip := noValue, false
	for {
		start, stop, ok := nextArgument(text, position, enclosing)
		if !ok {
			return start
		}
		position = stop
		word := text[start:stop]
		if len(word) > 2 && (word[0] == '\'' || word[0] == '"') && word[1] == '-' {
			// A quoted flag ('-px'): its value ends before the closing quote.
			start++
			if word[len(word)-1] == word[0] {
				stop--
			}
			word = text[start:stop]
		}
		if skip {
			skip = false
			continue
		}
		if pending != noValue {
			if !strings.HasPrefix(word, "-") || word == "-" {
				mask(start, stop, pending)
			}
			pending = noValue
			continue
		}
		// attached masks a password written into its flag (-px, -uuser:x).
		attached := func(flagLength int, kind valueKind) {
			valueStart := start + flagLength
			if valueStart < stop && text[valueStart] == '=' {
				valueStart++
			}
			if valueStart < stop {
				mask(valueStart, stop, kind)
			}
		}
		short := len(word) > 2 && word[0] == '-' && word[1] != '-'
		switch command {
		case "docker", "podman", "nerdctl", "buildah", "skopeo", "helm", "oras", "crane":
			switch {
			case word == "login":
				login = true
			case !login:
			case word == "-p":
				pending = wholeValue
			case short && word[1] == 'p':
				attached(2, wholeValue)
			}
		case "sshpass":
			switch {
			case !strings.HasPrefix(word, "-"):
				// The command sshpass runs, whose -p is its own (ssh -p 22).
				return start
			case word == "-p":
				pending = wholeValue
			case word == "-f", word == "-d", word == "-P":
				skip = true
			case short && word[1] == 'p':
				attached(2, wholeValue)
			}
		case "redis-cli":
			switch {
			case word == "-a":
				pending = wholeValue
			case short && word[1] == 'a':
				attached(2, wholeValue)
			}
		case "curl":
			switch {
			case word == "-u", word == "-U", word == "--user", word == "--proxy-user":
				pending = userValue
			case strings.HasPrefix(word, "--user="):
				attached(len("--user"), userValue)
			case strings.HasPrefix(word, "--proxy-user="):
				attached(len("--proxy-user"), userValue)
			case short && (word[1] == 'u' || word[1] == 'U') && strings.Contains(word, ":"):
				attached(2, userValue)
			case short && isLetters(word[1:]) && (word[len(word)-1] == 'u' || word[len(word)-1] == 'U'):
				// Combined flags ending in -u (-fsSu user:x).
				pending = userValue
			}
		default: // The mysql family.
			switch {
			case word == "-p":
				pending = wholeValue
			case short && word[1] == 'p':
				attached(2, wholeValue)
			}
		}
	}
}

// nextArgument reads the shell word at or after position: its start and end,
// and false at the end of the command: the end of its shell segment (a line,
// or ;, &&, ||, |, $( or a closing parenthesis or backtick outside quotes) or
// of the quoted string the command is written in (enclosing, the quote of the
// string its name is in: echo "mysql -px"; echo "ssh -p 22" never reads the
// second echo as mysql's). A quoted part is read to its closing quote on the
// line, separators inside it included; one that is not closed there ends the
// word and the command.
func nextArgument(text string, position int, enclosing byte) (int, int, bool) {
	for position < len(text) && (text[position] == ' ' || text[position] == '\t') {
		position++
	}
	if position == len(text) || isCommandEnd(text, position) || (enclosing != 0 && text[position] == enclosing) {
		return position, position, false
	}
	end := position
	for end < len(text) && text[end] != ' ' && text[end] != '\t' && !isCommandEnd(text, end) {
		quote := text[end]
		if enclosing != 0 && quote == enclosing {
			break
		}
		if quote == '\'' || quote == '"' {
			closing := quoteEnd(text, end)
			if closing < 0 {
				break
			}
			end = closing
		}
		end++
	}
	if end == position {
		return position, position, false
	}
	return position, end, true
}

// quoteEnd is the index of the quote that closes the one at start on its line
// (a separator inside quotes is part of the argument: mysql -e "select 1;
// select 2"), past backslash escapes in a double-quoted one, or -1.
func quoteEnd(text string, start int) int {
	for index := start + 1; index < len(text); index++ {
		switch text[index] {
		case text[start]:
			return index
		case '\\':
			if text[start] == '"' {
				index++
			}
		case '\n', '\r':
			return -1
		}
	}
	return -1
}

// quoteCursor tells which quoted string, if any, a position of text is in,
// reading each line from its start with the shell's rules (a backslash
// escapes outside single quotes). Positions are looked up in order, so each
// byte is read once.
type quoteCursor struct {
	text     string
	position int
	quote    byte
}

// at returns the quote of the string position is in, or 0.
func (c *quoteCursor) at(position int) byte {
	if position < c.position {
		c.position, c.quote = 0, 0
	}
	for ; c.position < position; c.position++ {
		switch character := c.text[c.position]; {
		case character == '\n':
			c.quote = 0
		case character == '\\' && c.quote != '\'':
			c.position++
		case c.quote == 0 && (character == '"' || character == '\''):
			c.quote = character
		case character == c.quote:
			c.quote = 0
		}
	}
	return c.quote
}

func isCommandEnd(text string, index int) bool {
	switch text[index] {
	case '\n', '\r', ';', '|', '&', ')', '`':
		return true
	case '$':
		return index+1 < len(text) && text[index+1] == '('
	}
	return false
}

func isLetters(text string) bool {
	for index := 0; index < len(text); index++ {
		if c := text[index] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return text != ""
}

// maskedArgument is a password argument (its quotes kept) with the password
// masked: all of it, or what follows the user's colon. ok is false when it
// holds no password (a user without one).
func maskedArgument(argument string, kind valueKind) (string, bool) {
	quote := ""
	if argument != "" && (argument[0] == '\'' || argument[0] == '"') {
		quote = argument[:1]
	}
	inner := strings.TrimPrefix(argument, quote)
	closing := ""
	if quote != "" && strings.HasSuffix(inner, quote) {
		inner, closing = inner[:len(inner)-1], quote
	}
	user := ""
	if kind == userValue {
		colon := strings.IndexByte(inner, ':')
		if colon < 0 {
			return argument, false
		}
		user, inner = inner[:colon+1], inner[colon+1:]
	}
	if inner == "" || inner == redactedMarker {
		return argument, false
	}
	return quote + user + redactedMarker + closing, true
}

var (
	// kubernetesSecretKind is a manifest's kind line naming a Secret, in a
	// document or a list's item.
	kubernetesSecretKind = regexp.MustCompile(`^[ \t-]*["']?kind["']?[ \t]*:[ \t]*["']?Secret["']?[ \t]*\r?\n?$`)
	// kubernetesDataKey is a Secret's data or stringData key, whose indented
	// entries are its values; its group is the key's indentation.
	kubernetesDataKey = regexp.MustCompile(`^([ \t]*(?:-[ \t]+)?)["']?(?:data|stringData)["']?[ \t]*:[ \t]*(?:#.*)?\r?\n?$`)
)

// maskKubernetesSecretData masks the values of a Kubernetes Secret's data and
// stringData entries in YAML (kubectl get secret -o yaml), whatever their
// keys are named: each entry of such a block in a YAML document (between ---
// lines) that is a Secret.
func maskKubernetesSecretData(text string) string {
	if !strings.Contains(text, "Secret") || !strings.Contains(text, "ata") {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	changed := false
	for start := 0; start < len(lines); {
		end := start + 1
		for end < len(lines) && strings.TrimRight(lines[end], " \t\r\n") != "---" {
			end++
		}
		if maskSecretDocument(lines[start:end]) {
			changed = true
		}
		start = end
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "")
}

// maskSecretDocument masks the data entries of one YAML document's lines when
// it is a Secret, and reports whether it changed any.
func maskSecretDocument(lines []string) bool {
	secret := false
	for _, line := range lines {
		if strings.Contains(line, "Secret") && kubernetesSecretKind.MatchString(line) {
			secret = true
			break
		}
	}
	if !secret {
		return false
	}
	changed := false
	for index := 0; index < len(lines); index++ {
		if !strings.Contains(lines[index], "ata") {
			continue
		}
		match := kubernetesDataKey.FindStringSubmatch(lines[index])
		if match == nil {
			continue
		}
		dataIndent, entryIndent := len(match[1]), -1
		for next := index + 1; next < len(lines); next++ {
			body := strings.TrimRight(lines[next], "\r\n")
			if strings.TrimSpace(body) == "" {
				continue
			}
			indent := indentation(body)
			if indent <= dataIndent {
				break
			}
			if entryIndent < 0 {
				entryIndent = indent
			}
			masked := maskedDataLine(body, indent > entryIndent)
			if masked != body {
				lines[next] = masked + lines[next][len(body):]
				changed = true
			}
			index = next
		}
	}
	return changed
}

// maskedDataLine is a Secret data block's line with its value masked: an
// entry's value after its key, or all of a continuation line (a block
// scalar's).
func maskedDataLine(line string, continuation bool) string {
	indent := line[:indentation(line)]
	content := strings.TrimSpace(line)
	if continuation {
		if content == redactedMarker {
			return line
		}
		return indent + redactedMarker
	}
	colon := strings.Index(content, ": ")
	if colon < 0 {
		return line
	}
	value := strings.TrimSpace(content[colon+2:])
	if value == "" || value == redactedMarker || isBlockScalarHeader(value) {
		return line
	}
	return indent + content[:colon+2] + redactedMarker
}

var (
	// urlUserPattern matches a URL's user without a password
	// (https://user@host); urlUserToken says whether it is a token.
	urlUserPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/\s:@'"]+)@`)
	// urlSignaturePattern matches a signed URL's signature parameter (an
	// Azure SAS sig=, an S3 or GCS presigned X-Amz-Signature=), which is the
	// credential.
	urlSignaturePattern = regexp.MustCompile(`(?i)((?:^|[?&;\s"'])(?:sig|signature|x-amz-signature|x-goog-signature)=)[^&\s"'<>#]+`)
)

// maskURLTokens masks a token used as a URL's user (https://<token>@github.com)
// and a signed URL's signature. A URL's user:password is activity.Redact's.
func maskURLTokens(text string) string {
	if strings.Contains(text, "://") {
		matches := urlUserPattern.FindAllStringSubmatchIndex(text, -1)
		var out strings.Builder
		last := 0
		for _, match := range matches {
			if user := text[match[4]:match[5]]; urlUserToken(user) {
				out.WriteString(text[last:match[4]])
				out.WriteString(redactedMarker)
				last = match[5]
			}
		}
		if last > 0 {
			out.WriteString(text[last:])
			text = out.String()
		}
	}
	if !strings.Contains(text, "=") {
		return text
	}
	return urlSignaturePattern.ReplaceAllString(text, "${1}"+redactedMarker)
}

// urlUserToken reports whether a URL's user is a token rather than a name
// (git, deploy): token characters only, and long, with letters and digits or
// longer still.
func urlUserToken(user string) bool {
	letters, digits := false, false
	for index := 0; index < len(user); index++ {
		switch c := user[index]; {
		case c >= '0' && c <= '9':
			digits = true
		case c|0x20 >= 'a' && c|0x20 <= 'z':
			letters = true
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return len(user) >= 32 || (len(user) >= 16 && letters && digits)
}
