import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";

function go(args, runnerRoot) {
	const result = spawnSync("go", args, {
		cwd: runnerRoot, encoding: "utf8", windowsHide: true,
		env: { ...process.env, GOWORK: "off" },
	});
	if (result.error || result.status !== 0) throw new Error(`Accounts Manager dependency check failed: ${result.error?.message || result.stderr || result.stdout}`);
	return result.stdout;
}

export function resolveAccountsManagerDependency(repoRoot) {
	const runnerRoot = join(repoRoot, "accounts-manager", "runner");
	const recordPath = join(repoRoot, "accounts-manager", "dependency.json");
	const pin = JSON.parse(readFileSync(recordPath, "utf8"));
	if (pin.schemaVersion !== 1 || typeof pin.module !== "string" || typeof pin.version !== "string" ||
		!/^github\.com\/[A-Za-z0-9_-]+\/CLIProxyAPI\/v7$/.test(pin.replacement?.module) ||
		!/^[a-f0-9]{40}$/.test(pin.replacement?.commit) ||
		!/^v7\.\d+\.\d+-\d{14}-[a-f0-9]{12}$/.test(pin.replacement?.version) ||
		!pin.replacement.version.endsWith(pin.replacement.commit.slice(0, 12)) ||
		!pin.replacement.sum?.startsWith("h1:") || !pin.replacement.goModSum?.startsWith("h1:") ||
		!/^[a-f0-9]{64}$/.test(pin.licenseSHA256)) {
		throw new Error("Invalid Accounts Manager dependency record");
	}
	const selected = JSON.parse(go(["list", "-mod=readonly", "-m", "-json", pin.module], runnerRoot));
	if (selected.Path !== pin.module || selected.Version !== pin.version ||
		selected.Replace?.Path !== pin.replacement.module || selected.Replace?.Version !== pin.replacement.version) {
		throw new Error("Accounts Manager go.mod does not match the pinned dependency");
	}
	const downloaded = JSON.parse(go(["mod", "download", "-json", `${pin.replacement.module}@${pin.replacement.version}`], runnerRoot));
	if (downloaded.Error || downloaded.Path !== pin.replacement.module || downloaded.Version !== pin.replacement.version ||
		downloaded.Sum !== pin.replacement.sum || downloaded.GoModSum !== pin.replacement.goModSum ||
		(downloaded.Origin?.Hash && downloaded.Origin.Hash !== pin.replacement.commit) || !downloaded.Dir) {
		throw new Error("Accounts Manager dependency checksum or revision mismatch");
	}
	go(["mod", "verify"], runnerRoot);
	const licensePath = join(downloaded.Dir, "LICENSE");
	if (createHash("sha256").update(readFileSync(licensePath)).digest("hex") !== pin.licenseSHA256) {
		throw new Error("Accounts Manager dependency license mismatch");
	}
	return { pin, recordPath, licensePath };
}

export function verifyAccountsManagerBinary(binaryPath, repoRoot, pin) {
	const build = JSON.parse(go(["version", "-m", "-json", binaryPath], join(repoRoot, "accounts-manager", "runner")));
	const matches = build.Deps?.filter(dependency => dependency.Path === pin.module) ?? [];
	if (matches.length !== 1 || matches[0].Version !== pin.version ||
		matches[0].Replace?.Path !== pin.replacement.module || matches[0].Replace?.Version !== pin.replacement.version ||
		matches[0].Replace?.Sum !== pin.replacement.sum) {
		throw new Error("Accounts Manager binary does not contain the pinned dependency");
	}
}
