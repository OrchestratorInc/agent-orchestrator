import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const scripts = dirname(fileURLToPath(import.meta.url));
const canonical = "github.com/router-for-me/CLIProxyAPI/v7";
const replacement = "github.com/Ayash-Bera/CLIProxyAPI/v7";
const revision = "f8e08347b8f7667bfaf26de2e59081dc346fc644";
const version = "v7.0.0-20260929230337-f8e08347b8f7";

function fixture(t, fault = "") {
	const root = mkdtempSync(join(tmpdir(), "accounts-dependency-"));
	t.after(() => rmSync(root, { recursive: true, force: true }));
	const scriptDir = join(root, "frontend", "scripts");
	const runner = join(root, "accounts-manager", "runner");
	const moduleDir = join(root, "module-cache", "engine");
	const bin = join(root, "bin");
	for (const dir of [scriptDir, runner, moduleDir, bin]) mkdirSync(dir, { recursive: true });
	for (const name of ["build-accounts-manager.mjs", "go-version.mjs", "accounts-manager-dependency.mjs"]) {
		if (existsSync(join(scripts, name))) copyFileSync(join(scripts, name), join(scriptDir, name));
	}
	writeFileSync(join(root, "frontend", "package.json"), '{"version":"0.0.0-test"}');
	writeFileSync(join(runner, "go.mod"), `module example.test/runner\n\ngo 1.26.0\n\nrequire ${canonical} v7.3.8\nreplace ${canonical} => ${replacement} ${version}\n`);
	const license = "MIT\nFixture copyright retained\n";
	const pin = { schemaVersion: 1, module: canonical, version: "v7.3.8", replacement: { module: replacement, version, commit: revision, sum: "h1:fixture-module", goModSum: "h1:fixture-mod" }, licenseSHA256: createHash("sha256").update(license).digest("hex") };
	writeFileSync(join(root, "accounts-manager", "dependency.json"), JSON.stringify(pin));
	writeFileSync(join(root, "accounts-manager", "UPSTREAM.md"), "Pinned public dependency\n");
	if (fault !== "missing-license") writeFileSync(join(moduleDir, "LICENSE"), fault === "tampered-license" ? "wrong license" : license);
	const tool = join(bin, "go");
	const fake = `#!${process.execPath}
const fs = require('node:fs');
const path = require('node:path');
const a = process.argv.slice(2), fault = ${JSON.stringify(fault)}, pin = ${JSON.stringify(pin)}, dir = ${JSON.stringify(moduleDir)};
fs.appendFileSync(${JSON.stringify(join(root, "calls.jsonl"))}, JSON.stringify({args:a,work:process.env.GOWORK})+'\\n');
if (a[0] === 'version' && a.length === 1) console.log('go version go1.27.1 linux/amd64');
else if (a[0] === 'list') console.log(JSON.stringify({Path:pin.module,Version:fault === 'wrong-required-version' ? 'v7.3.9' : pin.version,Replace:{Path:pin.replacement.module,Version:fault === 'wrong-pin' ? 'v7.0.0-wrong' : pin.replacement.version,Dir:dir}}));
else if (a[0] === 'mod' && a[1] === 'download') {
 if (fault === 'unavailable') { console.error('dependency unavailable'); process.exit(1); }
 console.log(JSON.stringify({Path:pin.replacement.module,Version:pin.replacement.version,Dir:dir,Sum:fault === 'wrong-sum' ? 'h1:wrong' : pin.replacement.sum,GoModSum:fault === 'wrong-mod-sum' ? 'h1:wrong' : pin.replacement.goModSum,Origin:fault === 'proxy-without-origin' ? undefined : {Hash:fault === 'wrong-revision' ? '0'.repeat(40) : pin.replacement.commit}}));
} else if (a[0] === 'mod' && a[1] === 'verify') {
 if (fault === 'tampered-module') { console.error('module has been modified'); process.exit(1); }
 console.log('all modules verified');
} else if (a[0] === 'build') {
 const output = a[a.indexOf('-o')+1]; fs.mkdirSync(path.dirname(output), {recursive:true});
 fs.writeFileSync(output, '#!${process.execPath}\\nconsole.log("ao-accounts-manager 0.0.0-test CLIProxyAPI v7.3.8");\\n', {mode:0o755});
} else if (a[0] === 'version' && a.includes('-m')) {
 const build = {Path:'github.com/aoagents/agent-orchestrator/accounts-manager/runner/cmd/ao-accounts-manager',Deps:[{Path:pin.module,Version:pin.version,Replace:{Path:pin.replacement.module,Version:fault === 'wrong-binary' ? 'v7.0.0-wrong' : pin.replacement.version,Sum:fault === 'wrong-binary-sum' ? 'h1:wrong' : pin.replacement.sum}}]};
 console.log(JSON.stringify(build));
} else { console.error('unexpected go arguments',a); process.exit(2); }
`;
	writeFileSync(tool, fake);
	chmodSync(tool, 0o755);
	return { root, pin, license, run: () => spawnSync(process.execPath, [join(scriptDir, "build-accounts-manager.mjs")], { encoding: "utf8", env: { PATH: bin, HOME: root, GOWORK: "ambient-workspace-must-not-win" } }) };
}

test("packages the pinned dependency with no engine sibling", { skip: process.platform === "win32" }, t => {
	const f = fixture(t);
	const result = f.run();
	assert.equal(result.status, 0, result.stderr || result.stdout);
	assert.equal(existsSync(join(f.root, "accounts-manager", "engine")), false);
	const output = join(f.root, "frontend", "accounts-manager");
	assert.equal(readFileSync(join(output, "CLIProxyAPI-LICENSE"), "utf8"), f.license);
	assert.deepEqual(JSON.parse(readFileSync(join(output, "dependency.json"), "utf8")), f.pin);
	const calls = readFileSync(join(f.root, "calls.jsonl"), "utf8").trim().split("\n").map(JSON.parse);
	for (const call of calls.filter(call => call.args[0] !== "version")) assert.equal(call.work, "off");
	assert(calls.some(call => call.args[0] === "mod" && call.args[1] === "verify"));
	assert(calls.some(call => call.args[0] === "build" && call.args.includes("-mod=readonly")));
});

for (const [fault, reason] of Object.entries({
	"wrong-pin": "does not match the pinned dependency",
	"wrong-required-version": "does not match the pinned dependency",
	unavailable: "dependency unavailable",
	"wrong-sum": "checksum or revision mismatch",
	"wrong-mod-sum": "checksum or revision mismatch",
	"wrong-revision": "checksum or revision mismatch",
	"missing-license": "ENOENT",
	"tampered-license": "license mismatch",
	"tampered-module": "module has been modified",
	"wrong-binary": "binary does not contain the pinned dependency",
	"wrong-binary-sum": "binary does not contain the pinned dependency",
})) {
	test(`refuses ${fault} without recording a successful package`, { skip: process.platform === "win32" }, t => {
		const f = fixture(t, fault);
		const result = f.run();
		assert.notEqual(result.status, 0, "unsafe package was accepted");
		assert(result.stderr.includes(reason), result.stderr);
		assert.equal(existsSync(join(f.root, "frontend", "accounts-manager", "dependency.json")), false);
		if (!fault.startsWith("wrong-binary")) {
			const calls = readFileSync(join(f.root, "calls.jsonl"), "utf8").trim().split("\n").map(JSON.parse);
			assert.equal(calls.some(call => call.args[0] === "build"), false);
		}
	});
}

test("accepts checksum-proven proxy downloads without optional VCS metadata", { skip: process.platform === "win32" }, t => {
	const result = fixture(t, "proxy-without-origin").run();
	assert.equal(result.status, 0, result.stderr || result.stdout);
});
