import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const registryHosts = new Set(["registry.npmjs.org", "registry.yarnpkg.com"]);

function parsePath(path) {
	if (!path.startsWith("\"")) {
		path = path.replace(/\t$/, "");
	} else {
		const escapes = { a: "\x07", b: "\b", f: "\f", n: "\n", r: "\r", t: "\t", v: "\v", "\"": "\"", "\\": "\\" };
		path = path.slice(1, -1).replace(/(?:\\[0-7]{3})+|\\([abfnrtv"\\])/g, (escape, char) =>
			char ? escapes[char] : Buffer.from(escape.match(/[0-7]{3}/g).map((code) => parseInt(code, 8))).toString("utf8"));
	}
	return path === "/dev/null" ? null : path.replace(/^b\//, "");
}

export function parseAddedLines(diffText, paths = new Map()) {
	const added = [];
	let file = null;
	let line = 0;
	let remaining = 0;
	for (const entry of diffText.split("\n")) {
		if (entry.startsWith("diff --git ")) {
			file = null;
			remaining = 0;
		} else if (remaining > 0) {
			if (entry.startsWith("+")) {
				if (file !== null) added.push({ file, line, text: entry.slice(1) });
				line++;
				remaining--;
			} else if (entry.startsWith(" ")) {
				line++;
				remaining--;
			}
		} else if (entry.startsWith("+++ ")) {
			const path = entry.slice(4);
			file = parsePath(path);
			if (file !== null) paths.set(path.startsWith("\"") ? path : file, path.startsWith("\""));
		} else {
			const hunk = entry.match(/^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/);
			if (hunk) {
				line = Number(hunk[1]);
				remaining = Number(hunk[2] ?? 1);
			}
		}
	}
	return added;
}

function whitespaceKind(run) {
	return run.includes("\t") ? (run.includes(" ") ? "spaces/tabs" : "tabs") : "spaces";
}

function iniValue(value) {
	value = value.trim();
	if (value.startsWith("'") && value.endsWith("'")) value = value.slice(1, -1);
	if (value.startsWith('"') && value.endsWith('"')) {
		try { return JSON.parse(value); } catch { return value; }
	}
	return value.replace(/\\([\\;#])|[;#].*$/g, (_, escaped) => escaped ?? "").trim();
}

function riskyNpmConfig(text) {
	if (/^\s*[;#\[]/.test(text)) return false;
	const setting = text.match(/^([^=]+)(?:=(.*))?$/);
	if (!setting) return false;
	const key = String(iniValue(setting[1])).replace(/\[\]$/, "").toLowerCase();
	return /^(?:@[^:]+:)?registry$|^(?:userconfig|globalconfig|node-options|node-gyp|script-shell|onload-script|init-module)$/.test(key)
		|| (key === "ignore-scripts" && iniValue(setting[2] ?? "true") === "false");
}

export function scan(lines) {
	const findings = [];
	for (const { file, line, text } of lines) {
		const add = (rule, message) => findings.push({ file, line, text, rule, message });
		const padding = text.match(/\S([ \t]{150,})\S/);
		if (padding && !/\.mdx?$/.test(file)) {
			add("R1", `padding: ${padding[1].length} ${whitespaceKind(padding[1])} inside a line (limit 150)`);
		}
		if (text.length > 500 && /(^|\/)package\.json$|\.config\.(js|mjs|cjs|ts)$|^\.github\/(?!.*\.json$)/.test(file)) {
			add("R2", `long-line: ${text.length} chars in a build/config file (limit 500)`);
		}
		if (/global\.o=|var _\$_[0-9a-f]{4}=|String\.fromCharCode\(127\)/.test(text)) {
			add("R3", "family-marker: known hidden-code payload marker");
		}
		if (/(^|\/)go\.(mod|work)$/.test(file) && text.includes("=>")) {
			add("R6", "go-replace: module replace added or changed");
		}
		if ((/(^|\/)\.npmrc$/.test(file) && riskyNpmConfig(text))
			|| (/(^|\/)\.yarnrc(?:\.yml)?$/.test(file)
				&& /^\s*(@[^:]+:)?registry\s*=|script-shell|onload-script|ignore-scripts\s*=\s*false|node-options/.test(text))) {
			add("R7", "install-config: package registry or script execution setting added or changed");
		}
	}
	return findings;
}

const tarball = /\.(?:tgz|tar(?:\.gz|\.xz)?)$/i;

function allowedSpec(value) {
	return (!/[/:]/.test(value) && !tarball.test(value)) || /^npm:(?:@[^/@:]+\/)?[^/@:]+@[^/:]*$/.test(value)
		|| value.startsWith("workspace:") || (/^(file|link):/.test(value) && !tarball.test(value));
}

function allowedLockSource(value, key, entry) {
	if (key === "from") return allowedSpec(value);
	if (key === "resolved" && entry?.link === true) {
		return !/[:?#\\]/.test(value) && !value.startsWith("//") && !tarball.test(value);
	}
	if (/^(?:[\w*+~^<>=| -][\w.*+~^<>=| -]*)?$/.test(value) && !tarball.test(value)) return true;
	try {
		const url = new URL(value);
		return url.protocol === "https:" && registryHosts.has(url.host)
			&& !url.username && !url.password && tarball.test(url.pathname);
	} catch {
		return false;
	}
}

function changedSpecs(head, base, path, bad) {
	for (const [key, value] of Object.entries(head ?? {})) {
		const name = `${path}.${key}`;
		if (typeof value === "string") {
			if (value !== base?.[key] && !allowedSpec(value)) bad.push(`${name} = ${JSON.stringify(value)}`);
		} else if (value && typeof value === "object") {
			changedSpecs(value, base?.[key], name, bad);
		}
	}
}

function changedLockSources(head, base, path, keys, bad) {
	for (const [name, entry] of Object.entries(head ?? {})) {
		for (const key of keys) {
			const value = entry?.[key];
			if (typeof value === "string" && value !== base?.[name]?.[key] && !allowedLockSource(value, key, entry)) {
				bad.push(`${path}.${name}.${key} = ${JSON.stringify(value)}`);
			}
		}
		if (keys.includes("version")) {
			changedLockSources(entry?.dependencies, base?.[name]?.dependencies, `${path}.${name}.dependencies`, keys, bad);
		}
	}
}

export function loadManifest(sha, path) {
	try {
		return execFileSync("git", ["show", `${sha}:${path}`], {
			encoding: "utf8", maxBuffer: 64 * 1024 * 1024, stdio: ["ignore", "pipe", "pipe"],
		});
	} catch (error) {
		if (error.status === 128 && /fatal: path .* (does not exist in|exists on disk, but not in) /.test(String(error.stderr))) return null;
		throw error;
	}
}

export function scanDiff(diffText, { baseSha = process.env.BASE_SHA, headSha = process.env.HEAD_SHA, load = loadManifest } = {}) {
	const paths = new Map();
	const findings = scan(parseAddedLines(diffText, paths));
	for (const [file, quoted] of paths) {
		const add = (rule, message, details = []) => findings.push({ file, line: 1, text: "", rule, message, details });
		if (quoted) {
			add("R4/R5", "odd-path: cannot verify a Git C-quoted path");
			continue;
		}
		if (!/(^|\/)(package\.json|package-lock\.json|npm-shrinkwrap\.json)$/.test(file)) continue;
		let base, head;
		let side = "head";
		try {
			const content = load(headSha, file);
			if (content === null) continue;
			head = JSON.parse(content);
			side = "base";
			base = JSON.parse(load(baseSha, file) ?? "{}");
		} catch {
			add("R4/R5", `cannot-verify: manifest at ${side} could not be loaded as JSON`);
			continue;
		}
		const hooks = [], sources = [];
		if (/(^|\/)package\.json$/.test(file)) {
			// Hooks that run on npm install or npm ci. Pack and publish hooks only run in a maintainer's release step.
			for (const key of ["preinstall", "install", "postinstall", "prepublish", "preprepare", "prepare", "postprepare", "dependencies"]) {
				if (Object.hasOwn(head?.scripts ?? {}, key) && JSON.stringify(head.scripts[key]) !== JSON.stringify(base?.scripts?.[key])) {
					hooks.push(`scripts.${key} = ${JSON.stringify(head.scripts[key])}`);
				}
			}
			for (const key of ["dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "overrides", "resolutions"]) {
				changedSpecs(head?.[key], base?.[key], key, sources);
			}
		} else {
			changedLockSources(head?.packages, base?.packages, "packages", ["resolved", "version"], sources);
			changedLockSources(head?.dependencies, base?.dependencies, "dependencies", ["resolved", "version", "from"], sources);
		}
		if (hooks.length) add("R4", `lifecycle-hook: ${hooks.length} install-time script value(s) added or changed`, hooks.slice(0, 20));
		if (sources.length) add("R5", `non-registry-source: ${sources.length} dependency source value(s) added or changed (up to 20 shown)`, sources.slice(0, 20));
	}
	return findings;
}

export function formatFindings(findings) {
	const output = findings.map(({ file, line, text, details, message }) => {
		const excerpts = (details ?? [text]).map((value) => {
			let excerpt = value.replace(/[ \t]{2,}/g, (run) => `<${run.length} ${whitespaceKind(run)}>`)
				.replace(/\p{C}/gu, "");
			if (excerpt.length > 120) excerpt = `${excerpt.slice(0, 117)}...`;
			return `\n  ${excerpt}`;
		});
		return `${file.replace(/\p{C}/gu, "")}:${line} ${message}${excerpts.join("")}`;
	});
	if (findings.length) {
		output.push("Fix: remove or rewrite the flagged changes. If a change is legitimate, ask a maintainer to add the `skip-hidden-code-guard` label and explain why in the PR.");
	}
	return output.join("\n");
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	const findings = scanDiff(readFileSync(0, "utf8"));
	if (findings.length) {
		console.log(formatFindings(findings));
		process.exitCode = 1;
	}
}
