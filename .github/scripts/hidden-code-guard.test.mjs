import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { formatFindings, parseAddedLines, scan, scanDiff } from "./hidden-code-guard.mjs";

const fakePayload = ["global", ".o='x';var _", "$_abcd=(function(a,q){"].join("").padEnd(1200, "x");
const padded = (head) => head + " ".repeat(709) + fakePayload;
const diff = (file, text, line = 1) => `diff --git a/${file} b/${file}\n--- a/${file}\n+++ b/${file}\n@@ -${line} +${line} @@\n-old\n+${text}\n`;
const check = (file, text) => scan(parseAddedLines(diff(file, text)));

test("flags all three rules on a synthetic padded next config line", () => {
	const file = "frontend/src/docs/next.config.mjs";
	const findings = scan(parseAddedLines(diff(file, padded("export default withMDX(config);"), 51)));
	assert.deepEqual(findings.map(({ rule, file, line }) => ({ rule, file, line })),
		["R1", "R2", "R3"].map((rule) => ({ rule, file, line: 51 })));
	const output = formatFindings(findings);
	assert.match(output, /:51 padding: 709 spaces inside a line \(limit 150\)/);
	assert.match(output, /<709 spaces>/);
	assert.match(output, /skip-hidden-code-guard/);
	for (const excerpt of output.split("\n").filter((line) => line.startsWith("  "))) {
		assert.ok(excerpt.slice(2).length <= 120);
		assert.ok(excerpt.endsWith("..."));
	}
	const sanitized = formatFindings([{ ...findings[0], text: "a\u0000\u007fb\u202ec" }]);
	assert.match(sanitized, /\n  abc\n/);
});

test("flags all three rules on a synthetic padded mobile babel config line", () => {
	const file = "packages/mobile/babel.config.js";
	const findings = scan(parseAddedLines(diff(file, padded("};"), 12)));
	assert.deepEqual(findings.map(({ rule, file, line }) => ({ rule, file, line })),
		["R1", "R2", "R3"].map((rule) => ({ rule, file, line: 12 })));
});

test("allows terminal fixture lines with 115 inner spaces", () => {
	assert.deepEqual(check("backend/terminal_test.go", "visible" + " ".repeat(115) + "screen"), []);
});

test("allows markdown tables with 250 inner spaces", () => {
	for (const file of ["README.md", "docs/guide.mdx"]) {
		assert.deepEqual(check(file, "| left" + " ".repeat(250) + "| right |"), []);
	}
});

test("allows long prose and ordinary source lines", () => {
	for (const [file, length] of [["guide.md", 605], ["guide.mdx", 401], ["source.ts", 299]]) {
		assert.deepEqual(check(file, "x".repeat(length)), []);
	}
});

test("enforces the exact line length and padding boundaries", () => {
	for (const file of ["vite.config.js", "next.config.mjs", "babel.config.cjs", "jest.config.ts", "package.json", "packages/app/package.json", ".github/workflows/check.yml"]) {
		assert.deepEqual(check(file, "x".repeat(500)), []);
		assert.deepEqual(check(file, "x".repeat(501)).map(({ rule }) => rule), ["R2"]);
	}
	assert.deepEqual(check("source.ts", "a" + " ".repeat(149) + "b"), []);
	for (const whitespace of [" ".repeat(150), "\t".repeat(150), " \t".repeat(75)]) {
		assert.deepEqual(check("source.ts", `a${whitespace}b`).map(({ rule }) => rule), ["R1"]);
	}
});

test("allows large single line JSON data files", () => {
	assert.deepEqual(check("pricing/catalog/v1/providers/x.json", "x".repeat(16843)), []);
	assert.deepEqual(check(".github/scripts/contributor-project/data/blame-lines.json", "x".repeat(2000)), []);
});

test("ignores removed payloads and deleted files", () => {
	const removed = diff("next.config.mjs", "safe").replace("-old", `-${padded("visible;")}`);
	const deleted = `diff --git a/babel.config.js b/babel.config.js\n--- a/babel.config.js\n+++ /dev/null\n@@ -1 +0,0 @@\n-${padded("};")}\n`;
	assert.deepEqual(scan(parseAddedLines(removed + deleted)), []);
});

test("tracks new line numbers across hunks and renamed files", () => {
	const patch = [
		"diff --git a/old.ts b/renamed.ts", "similarity index 90%", "rename from old.ts", "rename to renamed.ts",
		"--- a/old.ts", "+++ b/renamed.ts", "@@ -2,0 +3,2 @@", "+first", "+second",
		"@@ -8,2 +10,3 @@", "-old", "+third", " context", "+fourth", "\\ No newline at end of file",
		"diff --git a/old.config.js b/new.config.js", "--- a/old.config.js", "+++ b/new.config.js",
		"@@ -5 +6 @@", "-old", "+fifth", "@@ -20,0 +22 @@", "+++ b/content is not a header",
	].join("\n");
	assert.deepEqual(parseAddedLines(patch), [
		{ file: "renamed.ts", line: 3, text: "first" },
		{ file: "renamed.ts", line: 4, text: "second" },
		{ file: "renamed.ts", line: 10, text: "third" },
		{ file: "renamed.ts", line: 12, text: "fourth" },
		{ file: "new.config.js", line: 6, text: "fifth" },
		{ file: "new.config.js", line: 22, text: "++ b/content is not a header" },
	]);
	const quoted = diff("file.config.js", "x".repeat(501)).replace("+++ b/file.config.js", "+++ \"b/renamed\\t\\303\\251.config.js\"");
	assert.equal(scan(parseAddedLines(quoted))[0].file, "renamed\té.config.js");
	const spaced = diff("x", "x".repeat(501)).replace("+++ b/x", "+++ b/some dir/vite.config.js\t");
	assert.equal(scan(parseAddedLines(spaced))[0].file, "some dir/vite.config.js");
});

test("flags each family marker in ordinary TSX without padding", () => {
	for (const marker of [["global", ".o='x';"], ["var _", "$_abcd=0;"], ["String.fromCharCode", "(127)"]]) {
		assert.deepEqual(check("frontend/component.tsx", marker.join("")).map(({ rule }) => rule), ["R3"]);
	}
});

test("flags single line and block module replacements but allows requires and Go source", () => {
	for (const file of ["go.mod", "backend/go.mod", "go.work"]) {
		for (const text of ["replace a => ../local", "\ta => b v1.2.3"]) {
			assert.deepEqual(check(file, text).map(({ rule, message }) => ({ rule, message })),
				[{ rule: "R6", message: "go-replace: module replace added or changed" }]);
		}
		assert.deepEqual(check(file, "require modernc.org/sqlite v1.2.3"), []);
		assert.deepEqual(check(file, "\tgo.uber.org/zap v1.2.3"), []);
	}
	assert.deepEqual(check("backend/main.go", "// a => b"), []);
});

const encode = (value) => value === null || typeof value === "string" ? value : JSON.stringify(value);
const rules = (findings) => findings.map(({ rule }) => rule);
const compare = (base, head, file = "package.json") => scanDiff(diff(file, encode(head) ?? ""), {
	baseSha: "base", headSha: "head",
	load: (sha, path) => {
		assert.equal(path, file);
		assert.ok(sha === "base" || sha === "head");
		return encode(sha === "base" ? base : head);
	},
});

test("flags one-line, escaped and multiline postinstall scripts", () => {
	for (const head of ["{\"scripts\":{\"postinstall\":\"node x.js\"}}", String.raw`{"scripts":{"\u0070ostinstall":"node x.js"}}`, "{\"scripts\":{\"postinstall\":\n\"node x.js\"}}"]) {
		assert.deepEqual(rules(compare({}, head)), ["R4"]);
	}
});

test("flags all six install hooks and value edits, but allows unchanged hooks and removals", () => {
	const scripts = Object.fromEntries(["preinstall", "install", "postinstall", "prepare", "prepublish", "dependencies"].map((key) => [key, "node x.js"]));
	const findings = compare(null, { scripts });
	assert.deepEqual(rules(findings), ["R4"]);
	assert.equal(findings[0].details.length, 6);
	assert.match(findings[0].message, /6 install-time/);
	const base = { scripts: { postinstall: "node old.js" } };
	assert.deepEqual(rules(compare(base, { scripts: { postinstall: "node new.js" } })), ["R4"]);
	assert.deepEqual(compare(base, { ...base, name: "new-name" }), []);
	assert.deepEqual(compare(base, {}), []);
	assert.deepEqual(compare({}, { scripts: { build: "tsc", test: "node --test", lint: "eslint .", "install-deps": "npm install" } }), []);
});

test("flags every npm install and ci hook without flagging ordinary or release scripts", () => {
	for (const key of ["preinstall", "install", "postinstall", "prepublish", "preprepare", "prepare", "postprepare", "dependencies"]) {
		const head = { scripts: { [key]: "node hook.js" } };
		assert.deepEqual(rules(compare({}, head)), ["R4"], key);
		assert.deepEqual(rules(compare({ scripts: { [key]: "node old.js" } }, head)), ["R4"], key);
		assert.deepEqual(compare(head, head), [], key);
	}
	for (const key of ["build", "test", "lint", "prebuild", "posttest", "install-deps", "prepack", "postpack", "prepublishOnly", "publish", "postpublish"]) {
		assert.deepEqual(compare({}, { scripts: { [key]: "node task.js" } }), [], key);
	}
});

test("reports unparseable or unavailable manifests and skips absent heads", () => {
	for (const file of ["package.json", "package-lock.json", "npm-shrinkwrap.json"]) {
		assert.match(compare({}, "{", file)[0].message, /cannot-verify.*head/);
		assert.match(compare("{", {}, file)[0].message, /cannot-verify.*base/);
		assert.deepEqual(compare({}, null, file), []);
	}
	assert.match(scanDiff(diff("package.json", "{}"), { load: () => { throw new Error("unavailable"); } })[0].message, /cannot-verify/);
});

test("flags disallowed specs in every dependency section and nested overrides, while ignoring metadata", () => {
	for (const key of ["dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "overrides", "resolutions"]) {
		for (const value of ["attacker/payload#sha", "github:a/b", "git@github.com:a/b.git", "git+ssh://git@h/r", "https://x/y", "http://x/y", "catalog:", "file:evil.tgz", "link:evil.tar", "file:evil.tar.gz", "file:evil.tar.xz"]) {
			assert.deepEqual(rules(compare({}, { [key]: { x: value } })), ["R5"], `${key}: ${value}`);
		}
	}
	assert.match(compare({}, { overrides: { a: { b: "https://evil.example/x" } } })[0].details[0], /overrides\.a\.b/);
	assert.deepEqual(compare({}, { repository: { url: "git+https://github.com/aoagents/agent-orchestrator.git" }, homepage: "https://x/y", bugs: { url: "https://x/y" } }), []);
});

test("allows ranges, tags, aliases and local links, and ignores reordered or removed specs", () => {
	const values = ["^1.2.3", ">=1 <2", "latest", "*", "", "$react", "npm:foo@^1", "npm:@s/foo@1", "workspace:*", "file:../pkg", "link:../pkg"];
	assert.deepEqual(compare({}, { dependencies: Object.fromEntries(values.map((value, i) => [`x${i}`, value])) }), []);
	const base = { dependencies: { a: "github:a/b", b: "https://evil.example/x" } };
	assert.deepEqual(compare(base, { dependencies: { b: base.dependencies.b, a: base.dependencies.a } }), []);
	assert.deepEqual(compare(base, { dependencies: {} }), []);
});

test("decodes escaped lockfile URLs and flags foreign URLs, HTTP, SCP sources and relative tarballs", () => {
	const escaped = String.raw`{"lockfileVersion":3,"packages":{"node_modules/x":{"resolved":"https:\/\/evil.example\/x.tgz"}}}`;
	assert.deepEqual(rules(compare({}, escaped, "package-lock.json")), ["R5"]);
	for (const file of ["package-lock.json", "npm-shrinkwrap.json"]) {
		for (const resolved of ["https://evil.example/x.tgz", "http://registry.npmjs.org/x.tgz", "git@github.com:a/b.git", "./evil.tar.gz"]) {
			assert.deepEqual(rules(compare({}, { lockfileVersion: 2, packages: { x: { resolved } } }, file)), ["R5"]);
		}
	}
});

test("checks recursive shrinkwrap v1 resolved, version and from values", () => {
	for (const [key, value] of [["resolved", "https://evil.example/x.tgz"], ["version", "git+ssh://git@github.com/a/b.git"], ["from", "github:a/b"]]) {
		const head = { lockfileVersion: 1, dependencies: { parent: { dependencies: { x: { [key]: value } } } } };
		const findings = compare({}, head, "npm-shrinkwrap.json");
		assert.deepEqual(rules(findings), ["R5"]);
		assert.match(findings[0].details[0], /dependencies\.parent\.dependencies\.x\./);
	}
});

test("allows official HTTPS lock URLs, plain versions and workspace links", () => {
	const packages = { npm: { resolved: "https://registry.npmjs.org/x/-/x.tgz" }, yarn: { resolved: "https://registry.yarnpkg.com/x/-/x.tgz" }, mobile: { link: true, resolved: "packages/mobile" } };
	assert.deepEqual(compare({}, { lockfileVersion: 3, packages }, "package-lock.json"), []);
	assert.deepEqual(compare({}, { dependencies: { x: { version: "1.2.3", from: "x@^1", resolved: packages.npm.resolved } } }, "npm-shrinkwrap.json"), []);
});

test("allows only registry tarballs, plain registry specs and explicit local lockfile links", () => {
	const rejected = [
		"github:attacker/payload#main", "gitlab:attacker/payload", "bitbucket:attacker/payload", "gist:abcdef",
		"git+ssh://git@github.com/a/b.git", "git+https://github.com/a/b.git", "git+file:../payload",
		"attacker/payload", "attacker/payload#main", "file:../payload", ".", "..", ".payload", "https://registry.npmjs.org/payload.js",
		"https://registry.npmjs.org.evil.example/x.tgz", "https://evil@registry.npmjs.org/x.tgz",
	];
	const allowed = ["1.2.3", "1.2.3-beta.1+build", "^1.2.3", ">=1 <2", "1.2.x || ~2", "1 - 2", "latest", "next-1", "*", "https://registry.npmjs.org/x/-/x.tgz", "https://registry.yarnpkg.com/x/-/x.tgz"];
	for (const file of ["package-lock.json", "npm-shrinkwrap.json"]) {
		for (const section of ["packages", "dependencies"]) {
			for (const key of ["resolved", "version"]) {
				for (const value of rejected) {
					const base = { [section]: { x: { version: "1.2.3", integrity: "sha512-old" } } };
					assert.deepEqual(rules(compare(base, { [section]: { x: { version: "1.2.3", [key]: value } } }, file)), ["R5"], `${file} ${section}.${key}: ${value}`);
				}
				for (const value of allowed) assert.deepEqual(compare({}, { [section]: { x: { [key]: value } } }, file), [], value);
			}
		}
		for (const resolved of ["../packages/product-ui", "native/image-paste"]) {
			assert.deepEqual(compare({}, { packages: { x: { link: true, resolved } } }, file), [], resolved);
		}
	}
});

test("flags local tarballs with uppercase or mixed-case extensions", () => {
	for (const extension of ["TGZ", "tGz", "TAR", "TaR.Gz", "TAR.XZ"]) {
		for (const value of [`file:payload.${extension}`, `link:payload.${extension}`, `payload.${extension}`]) {
			assert.deepEqual(rules(compare({}, { dependencies: { x: value } })), ["R5"], value);
			for (const file of ["package-lock.json", "npm-shrinkwrap.json"]) {
				for (const key of ["resolved", "version", "from"]) {
					assert.deepEqual(rules(compare({}, { dependencies: { x: { [key]: value } } }, file)), ["R5"], `${file} ${key}: ${value}`);
				}
			}
		}
	}
});

test("ignores existing node-gyp Git sources during sibling changes and entry removals", () => {
	for (const section of ["packages", "dependencies"]) {
		const base = { [section]: { "node-gyp": { resolved: "git+ssh://git@github.com/electron/node-gyp.git#sha", version: "git+ssh://git@github.com/electron/node-gyp.git#sha" }, sibling: { version: "1.0.0" } } };
		const head = { [section]: { ...base[section], sibling: { version: "2.0.0" } } };
		assert.deepEqual(compare(base, head, "package-lock.json"), []);
		assert.deepEqual(compare(base, { [section]: {} }, "package-lock.json"), []);
	}
});

test("groups 50 bad lock entries into one finding with 20 capped and sanitized excerpts", () => {
	const packages = Object.fromEntries(Array.from({ length: 50 }, (_, i) => [`x${i}\u0000\u202e${"x".repeat(140)}`, { resolved: "https://evil.example/x.tgz" }]));
	const findings = compare({}, { packages }, "package-lock.json");
	assert.deepEqual(rules(findings), ["R5"]);
	assert.match(findings[0].message, /50 dependency source/);
	assert.equal(findings[0].details.length, 20);
	const output = formatFindings(findings);
	const excerpts = output.split("\n").filter((line) => line.startsWith("  "));
	assert.equal(excerpts.length, 20);
	assert.ok(excerpts.every((line) => line.length <= 122));
	assert.doesNotMatch(output, /[\u0000\u202e]/u);
});

test("flags sensitive npm and yarn settings while allowing ordinary settings", () => {
	for (const file of [".npmrc", "packages/mobile/.npmrc", ".yarnrc", ".yarnrc.yml"]) {
		for (const text of ["registry=https://registry.npmjs.org", " @scope:registry = https://evil.example", "script-shell=/tmp/x", "onload-script=x", "ignore-scripts = false", "node-options=--require=x"]) {
			assert.deepEqual(rules(check(file, text)), ["R7"]);
		}
		for (const text of ["legacy-peer-deps=true", "ignore-scripts=true"]) assert.deepEqual(check(file, text), []);
	}
});

test("flags npm config indirection and code-loading keys, while ignoring comments and harmless values", () => {
	for (const file of [".npmrc", "frontend/.npmrc"]) {
		for (const key of ["userconfig", "globalconfig", "node-options", "node-gyp", "script-shell", "onload-script", "init-module"]) {
			assert.deepEqual(rules(check(file, `${key}=./extra.ini`)), ["R7"], key);
			assert.deepEqual(check(file, `# ${key}=./extra.ini`), [], key);
			assert.deepEqual(check(file, `; ${key}=./extra.ini`), [], key);
		}
		assert.deepEqual(check(file, "message=node-options"), []);
	}
});

test("normalizes quoted npm INI keys, JSON escapes, whitespace, case and array suffixes", () => {
	for (const key of ['"registry"', "'registry'", '  "registry"  ', '"ReGiStRy"', String.raw`"\u0072egistry"`, '"@scope:registry"', '"userconfig"', "'globalconfig'", '"node-options"', '"script-shell"', '"registry[]"']) {
		assert.deepEqual(rules(check(".npmrc", `${key}=https://evil.example`)), ["R7"], key);
	}
	for (const value of ['"false"', "'false'", "false # enable scripts"]) {
		assert.deepEqual(rules(check(".npmrc", `"ignore-scripts"=${value}`)), ["R7"], value);
	}
	assert.deepEqual(check(".npmrc", '"ignore-scripts"="true"'), []);
	assert.deepEqual(check(".npmrc", '"legacy-peer-deps"="true"'), []);
});

test("reports Git C-quoted headers without loading them or treating hunk content as headers", () => {
	const patch = diff("package.json", "{}").replace("+++ b/package.json", "+++ \"b/odd\\tpackage.json\"");
	const findings = scanDiff(patch, { load: () => assert.fail("must not load an odd path") });
	assert.match(findings[0].message, /odd-path/);
	assert.deepEqual(scanDiff(diff("source.ts", "++ b/package.json"), { load: () => assert.fail("not a header") }), []);
});

test("runs the real script on two Git commits without executing an install hook", () => {
	const dir = mkdtempSync(join(tmpdir(), "hidden-code-guard-"));
	const git = (...args) => execFileSync("git", args, { cwd: dir, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
	try {
		git("init", "-q");
		git("config", "user.name", "Guard test");
		git("config", "user.email", "guard@example.invalid");
		git("config", "commit.gpgsign", "false");
		writeFileSync(join(dir, "package.json"), "{}");
		writeFileSync(join(dir, ".npmrc"), "");
		git("add", "."); git("commit", "-qm", "base");
		const base = git("rev-parse", "HEAD");
		writeFileSync(join(dir, "package.json"), JSON.stringify({ scripts: { postinstall: "touch executed" }, dependencies: { x: "github:a/b" } }));
		writeFileSync(join(dir, ".npmrc"), "registry=https://evil.example\n");
		git("add", "."); git("commit", "-qm", "head");
		const head = git("rev-parse", "HEAD");
		const patch = git("diff", "--text", "--no-renames", "--unified=0", base, head);
		const result = spawnSync(process.execPath, [fileURLToPath(new URL("./hidden-code-guard.mjs", import.meta.url))], { cwd: dir, input: patch, encoding: "utf8", env: { ...process.env, BASE_SHA: base, HEAD_SHA: head } });
		assert.equal(result.status, 1, result.stderr);
		assert.equal(result.stdout, [
			".npmrc:1 install-config: package registry or script execution setting added or changed", "  registry=https://evil.example",
			"package.json:1 lifecycle-hook: 1 install-time script value(s) added or changed", "  scripts.postinstall = \"touch executed\"",
			"package.json:1 non-registry-source: 1 dependency source value(s) added or changed (up to 20 shown)", "  dependencies.x = \"github:a/b\"",
			"Fix: remove or rewrite the flagged changes. If a change is legitimate, ask a maintainer to add the `skip-hidden-code-guard` label and explain why in the PR.\n",
		].join("\n"));
		assert.equal(existsSync(join(dir, "executed")), false);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});
