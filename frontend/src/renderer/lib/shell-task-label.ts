/**
 * Turns the shell command a provider reports for a background task into
 * something a person can read: "PORT=3000 npm run dev > /tmp/dev.log 2>&1"
 * becomes a dev server on port 3000. A description that is not a command
 * (prose, or quoting this reader can't follow) is returned untouched.
 */
export type ShellTaskLabel = (
	| { kind: "devServer"; port?: string }
	| { kind: "tests" | "build" | "lint" | "install" }
	| { kind: "command"; text: string }
) & {
	/** The folder the last `cd` before the task moved into, to tell like tasks apart. */
	dir?: string;
};

const PACKAGE_RUNNERS = new Set(["npm", "pnpm", "yarn", "bun"]);
const WRAPPERS = new Set(["nohup", "exec", "env", "time", "npx", "bunx", "sudo"]);
// Wrapper options that take the next word as their value: "sudo -u bob", "npx -p vite".
const WRAPPER_VALUE_FLAGS: Record<string, string[]> = {
	sudo: ["-u", "-g", "--user", "--group"],
	npx: ["-p", "--package"],
	env: ["-u", "--unset"],
};
const SETUP = new Set(["cd", "export", "source", ".", "set", "pushd"]);
const FRAMEWORK_CLIS = new Set(["next", "nuxt", "astro", "remix"]);
const SERVER_TOOLS = new Set(["webpack-dev-server", "serve", "http-server", "uvicorn", "gunicorn"]);
const TEST_TOOLS = new Set(["vitest", "jest", "pytest", "playwright", "mocha"]);
const ENV_ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/;
// A redirection: "> f", ">> f", "2> f", "&> f", "< f", or one that names its target inline ("2>&1", ">/tmp/x").
const REDIRECT = /^(\d*|&)(>>?|<)(&?\d+|\S+)?$/;
const DIGITS = /^\d+$/;
const MAX_UNWRAP = 3;

type Word = { text: string; raw: string; quoted: boolean };
type Step = { words: Word[]; background: boolean; piped: boolean };

/**
 * Splits a command line into steps and words in one pass, so "&&", ";", "|" and
 * "&" inside quotes stay part of their word. Null when a quote never closes.
 */
function scan(input: string): Step[] | null {
	const steps: Step[] = [];
	let words: Word[] = [];
	let text = "";
	let start = -1;
	let quoted = false;
	let piped = false;
	const flush = (end: number) => {
		if (start >= 0) words.push({ text, raw: input.slice(start, end), quoted });
		text = "";
		start = -1;
		quoted = false;
	};
	const endStep = (end: number, background = false) => {
		flush(end);
		if (words.length) steps.push({ words, background, piped });
		words = [];
		piped = false;
	};
	for (let i = 0; i < input.length; i++) {
		const c = input[i];
		if (c === "'" || c === '"') {
			if (start < 0) start = i;
			let j = i + 1;
			let inner = "";
			for (; j < input.length && input[j] !== c; j++) {
				// Inside double quotes a backslash escapes a quote; a Windows path keeps its backslashes.
				if (c === '"' && input[j] === "\\" && input[j + 1] === '"') j++;
				inner += input[j];
			}
			if (j >= input.length) return null;
			text += inner;
			quoted = true;
			i = j;
			continue;
		}
		if (/\s/.test(c)) {
			flush(i);
			continue;
		}
		if (c === ";") {
			endStep(i);
			continue;
		}
		if (c === "&") {
			if (input[i + 1] === "&") {
				endStep(i);
				i++;
				continue;
			}
			// Part of a redirection: "2>&1", "&>".
			if (text.endsWith(">") || input[i + 1] === ">") {
				if (start < 0) start = i;
				text += c;
				continue;
			}
			// A lone "&" backgrounds the step before it.
			endStep(i, true);
			continue;
		}
		if (c === "|") {
			if (input[i + 1] === "|") {
				endStep(i);
				i++;
				continue;
			}
			endStep(i);
			piped = true;
			continue;
		}
		if (start < 0) start = i;
		text += c;
	}
	endStep(input.length);
	return steps;
}

/** "C:\\Windows\\system32\\cmd.exe" → "cmd". */
function programName(word: string): string {
	return (word.split(/[\\/]+/).pop() ?? word).toLowerCase().replace(/\.exe$/, "");
}

/** The command a shell interpreter was handed: `cmd /c "…"`, `bash -lc '…'`, `pwsh -Command …`. */
function unwrapInterpreter(words: Word[]): string | null {
	const program = programName(words[0]?.text ?? "");
	const rest = words.slice(1);
	let at = -1;
	if (program === "cmd") at = rest.findIndex((word) => /^\/[ck]$/i.test(word.text));
	else if (["bash", "sh", "zsh", "dash"].includes(program)) at = rest.findIndex((word) => /^-[a-z]*c[a-z]*$/.test(word.text));
	else if (program === "powershell" || program === "pwsh") at = rest.findIndex((word) => /^-(c|command)$/i.test(word.text));
	if (at < 0 || at + 1 >= rest.length) return null;
	const payload = rest.slice(at + 1);
	return payload.length === 1 ? payload[0].text : payload.map((word) => word.raw).join(" ");
}

/** Commands start with a lowercase program; "Serve the docs site" is prose that happens to start with a tool's name. */
function looksLikeProse(word: Word): boolean {
	return !word.quoted && /^[A-Z][a-z]+$/.test(word.text);
}

function readPort(value: string, current: string | undefined): string | undefined {
	return DIGITS.test(value) ? value : current;
}

const folderName = (target: string) => target.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || undefined;

type Candidate = { tokens: Word[]; background: boolean; dir?: string };

/** The steps that run something, each with its words minus the environment, wrappers and log plumbing. */
function candidatesOf(steps: Step[]): { candidates: Candidate[]; port?: string; rewritten: boolean } {
	let port: string | undefined;
	let dir: string | undefined;
	// Splitting a chain alone proves nothing: plain prose may hold a semicolon.
	let rewritten = false;
	const candidates: Candidate[] = [];
	for (const step of steps) {
		// Of a pipe, the first stage runs the task.
		if (step.piped) {
			rewritten = true;
			continue;
		}
		const kept: Word[] = [];
		let wrapper: string | null = null;
		for (let i = 0; i < step.words.length; i++) {
			const word = step.words[i];
			if (!kept.length) {
				// "$env:PORT=3000" (PowerShell) sets the environment in a step of its own.
				const powershell = /^\$env:([A-Za-z_][A-Za-z0-9_]*)=(.*)$/i.exec(word.text);
				if (powershell) {
					if (powershell[1].toUpperCase() === "PORT") port = readPort(powershell[2], port);
					rewritten = true;
					continue;
				}
				if (ENV_ASSIGNMENT.test(word.text)) {
					const [name, value] = word.text.split(/=(.*)/s);
					if (name.toUpperCase() === "PORT") port = readPort(value, port);
					rewritten = true;
					continue;
				}
				if (WRAPPERS.has(word.text)) {
					wrapper = word.text;
					rewritten = true;
					continue;
				}
				// A wrapper's own options: "npx --yes vite", "sudo -u bob npm start".
				if (wrapper && word.text.startsWith("-")) {
					if (WRAPPER_VALUE_FLAGS[wrapper]?.includes(word.text)) i++;
					continue;
				}
			}
			const redirect = word.quoted ? null : REDIRECT.exec(word.text);
			if (redirect) {
				rewritten = true;
				if (!redirect[3]) i++; // the target is the next word
				continue;
			}
			kept.push(word);
		}
		if (!kept.length) continue;
		const program = kept[0].text.toLowerCase();
		if (SETUP.has(program)) {
			rewritten = true;
			if (program === "cd" || program === "pushd") {
				const target = kept.slice(1).find((word) => !/^\/d$/i.test(word.text));
				if (target) dir = folderName(target.text);
			}
			// cmd's "set PORT=3000", "export PORT=3000".
			for (const word of kept.slice(1)) {
				const [name, value] = word.text.split(/=(.*)/s);
				if (value !== undefined && name.toUpperCase() === "PORT") port = readPort(value, port);
			}
			continue;
		}
		candidates.push({ tokens: kept, background: step.background, dir });
	}
	return { candidates, port, rewritten };
}

function portFrom(tokens: string[]): string | undefined {
	for (let i = 0; i < tokens.length; i++) {
		const inline = /^--port=(\d+)$/.exec(tokens[i]);
		if (inline) return inline[1];
		if ((tokens[i] === "--port" || tokens[i] === "-p") && DIGITS.test(tokens[i + 1] ?? "")) return tokens[i + 1];
		const host = /(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)/.exec(tokens[i]);
		if (host) return host[1];
	}
	// "python -m http.server 8000", "php -S localhost:8000"
	const server = tokens.indexOf("http.server");
	if (server >= 0 && DIGITS.test(tokens[server + 1] ?? "")) return tokens[server + 1];
	return undefined;
}

type Kind = "devServer" | "tests" | "build" | "lint" | "install";

function classify(tokens: string[]): Kind | null {
	const [program, first, second] = tokens.map((token) => token.toLowerCase());
	// No subcommand, or only options: the tool's default.
	const bare = !first || first.startsWith("-");
	if (PACKAGE_RUNNERS.has(program)) {
		const script = first === "run" || first === "run-script" ? second : first;
		if (!script) return null;
		if (/^(dev|start|serve|preview)(:|$)/.test(script)) return /prod/.test(script) ? null : "devServer";
		if (/^test(:|$)/.test(script) || script === "t") return "tests";
		if (/^build(:|$)/.test(script)) return "build";
		if (/^lint(:|$)/.test(script)) return "lint";
		if (["install", "i", "ci", "add"].includes(script)) return "install";
		return null;
	}
	if (FRAMEWORK_CLIS.has(program)) return first === "dev" || first === "start" ? "devServer" : null;
	if (program === "vite") {
		if (bare || ["dev", "serve", "preview"].includes(first)) return "devServer";
		return first === "build" ? "build" : null;
	}
	if (program === "hugo") {
		if (first === "server" || first === "serve") return "devServer";
		return bare || first === "build" ? "build" : null;
	}
	if (program === "jekyll") {
		if (["serve", "server", "s"].includes(first)) return "devServer";
		return first === "build" || first === "b" ? "build" : null;
	}
	if (SERVER_TOOLS.has(program)) return "devServer";
	if (TEST_TOOLS.has(program)) return "tests";
	if ((program === "python" || program === "python3") && first === "-m" && second === "http.server") return "devServer";
	if (program === "php" && first === "-s") return "devServer";
	if ((program === "rails" || program === "bin/rails") && (first === "s" || first === "server")) return "devServer";
	if (program === "flask" && first === "run") return "devServer";
	if ((program === "go" || program === "cargo") && first === "test") return "tests";
	if ((program === "go" || program === "cargo") && first === "build") return "build";
	return null;
}

function label(description: string, depth: number): ShellTaskLabel {
	const asWritten: ShellTaskLabel = { kind: "command", text: description };
	const steps = scan(description);
	if (!steps?.length) return asWritten;
	if (steps.some((step) => looksLikeProse(step.words[0]))) return asWritten;
	if (steps.length === 1 && depth < MAX_UNWRAP) {
		const payload = unwrapInterpreter(steps[0].words);
		if (payload?.trim()) return label(payload.trim(), depth + 1);
	}
	const { candidates, port, rewritten } = candidatesOf(steps);
	// A backgrounded step ("npm run dev > log & echo $!") is the task, not the one-shot after it.
	const background = candidates.findIndex((candidate) => candidate.background);
	const chosen = background >= 0 ? candidates[background] : candidates[candidates.length - 1];
	if (!chosen) return asWritten;
	// Two long-running steps ("npm run dev & npm run test:watch"): no one name fits both.
	if (background >= 0 && candidates.slice(background + 1).some((candidate) => classify(candidate.tokens.map((word) => word.text)))) {
		return asWritten;
	}
	const tokens = chosen.tokens.map((word) => word.text);
	const where = chosen.dir ? { dir: chosen.dir } : {};
	const kind = classify(tokens);
	if (kind === "devServer") {
		const found = port ?? portFrom(tokens);
		return found ? { kind, port: found, ...where } : { kind, ...where };
	}
	if (kind) return { kind, ...where };
	// Unknown programs keep their own words, without the environment, wrappers and log plumbing.
	const unwrapped = depth > 0 || rewritten || background >= 0;
	return { kind: "command", text: unwrapped ? chosen.tokens.map((word) => word.raw).join(" ") : description, ...where };
}

export function shellTaskLabel(description: string): ShellTaskLabel {
	return label(description.trim(), 0);
}
