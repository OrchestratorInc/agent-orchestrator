/**
 * Turns the shell command a provider reports for a background task into
 * something a person can read: "PORT=3000 npm run dev > /tmp/dev.log 2>&1"
 * becomes a dev server on port 3000. A description that is not a command is
 * returned untouched.
 */
export type ShellTaskLabel =
	| { kind: "devServer"; port?: string }
	| { kind: "tests" | "build" | "lint" | "install" }
	| { kind: "command"; text: string };

const PACKAGE_RUNNERS = new Set(["npm", "pnpm", "yarn", "bun"]);
const WRAPPERS = new Set(["nohup", "exec", "env", "time", "npx", "bunx", "sudo"]);
const SETUP = new Set(["cd", "export", "source", ".", "set", "pushd"]);
const FRAMEWORK_CLIS = new Set(["next", "nuxt", "astro", "remix"]);
const SERVER_TOOLS = new Set(["vite", "webpack-dev-server", "serve", "http-server", "uvicorn", "gunicorn", "hugo", "jekyll"]);
const TEST_TOOLS = new Set(["vitest", "jest", "pytest", "playwright", "mocha"]);
const ENV_ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/;
// A redirection: "> f", ">> f", "2> f", "&> f", "< f", or one that names its target inline ("2>&1", ">/tmp/x").
const REDIRECT = /^(\d*|&)(>>?|<)(&?\d+|\S+)?$/;

function tokenize(segment: string): string[] {
	return segment.match(/"[^"]*"|'[^']*'|\S+/g) ?? [];
}

const unquote = (token: string) => token.replace(/^(["'])(.*)\1$/, "$2");

/** The tokens that name the program and its arguments, plus anything the environment says about a port. */
function commandOf(description: string): { tokens: string[]; port?: string; rewritten: boolean } | null {
	// Of a chain, the last real step runs the task; of a pipe, the first stage does.
	const steps = description.split(/&&|\|\||;/).map((step) => step.split(/(?<![|>&])\|(?!\|)/)[0]);
	let port: string | undefined;
	// Splitting a chain alone proves nothing: plain prose may hold a semicolon.
	let rewritten = false;
	let tokens: string[] = [];
	for (const step of steps) {
		const words = tokenize(step.trim());
		const kept: string[] = [];
		for (let i = 0; i < words.length; i++) {
			const word = words[i];
			if (!kept.length && ENV_ASSIGNMENT.test(word)) {
				const [name, value] = word.split(/=(.*)/s);
				if (name === "PORT") port = unquote(value);
				rewritten = true;
				continue;
			}
			if (!kept.length && WRAPPERS.has(word)) { rewritten = true; continue; }
			const redirect = REDIRECT.exec(word);
			if (redirect) {
				rewritten = true;
				if (!redirect[3]) i++; // the target is the next word
				continue;
			}
			if (word === "&") { rewritten = true; continue; }
			kept.push(word);
		}
		if (kept.length && !SETUP.has(kept[0])) tokens = kept;
	}
	return tokens.length ? { tokens, port, rewritten } : null;
}

function portFrom(tokens: string[]): string | undefined {
	for (let i = 0; i < tokens.length; i++) {
		const inline = /^--port=(\d+)$/.exec(tokens[i]);
		if (inline) return inline[1];
		if ((tokens[i] === "--port" || tokens[i] === "-p") && /^\d+$/.test(tokens[i + 1] ?? "")) return tokens[i + 1];
		const host = /(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)/.exec(tokens[i]);
		if (host) return host[1];
	}
	// "python -m http.server 8000", "php -S localhost:8000"
	const server = tokens.indexOf("http.server");
	if (server >= 0 && /^\d+$/.test(tokens[server + 1] ?? "")) return tokens[server + 1];
	return undefined;
}

function classify(tokens: string[]): Exclude<ShellTaskLabel["kind"], "command"> | null {
	const [program, first, second] = tokens.map((token) => token.toLowerCase());
	if (PACKAGE_RUNNERS.has(program)) {
		const script = first === "run" || first === "run-script" ? second : first;
		if (!script) return null;
		if (/^(dev|start|serve|preview)(:|$)/.test(script)) return "devServer";
		if (/^test(:|$)/.test(script) || script === "t") return "tests";
		if (/^build(:|$)/.test(script)) return "build";
		if (/^lint(:|$)/.test(script)) return "lint";
		if (["install", "i", "ci", "add"].includes(script)) return "install";
		return null;
	}
	if (FRAMEWORK_CLIS.has(program)) return first === "dev" || first === "start" ? "devServer" : null;
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

export function shellTaskLabel(description: string): ShellTaskLabel {
	const parsed = commandOf(description);
	if (!parsed) return { kind: "command", text: description };
	const kind = classify(parsed.tokens);
	if (kind === "devServer") {
		const port = parsed.port ?? portFrom(parsed.tokens);
		return port ? { kind, port } : { kind };
	}
	if (kind) return { kind };
	// Unknown programs keep their own words, without the environment, wrappers and log plumbing.
	return { kind: "command", text: parsed.rewritten ? parsed.tokens.join(" ") : description };
}
