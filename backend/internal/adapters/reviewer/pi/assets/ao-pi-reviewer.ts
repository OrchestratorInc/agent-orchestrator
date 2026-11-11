import { Type } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

const workspace = mustEnv("AO_PI_REVIEW_WORKSPACE");
const promptRoot = mustEnv("AO_PI_REVIEW_PROMPT_ROOT");
const workerSession = mustValue("AO_PI_REVIEW_SESSION");
const manifestPointer = mustEnv("AO_PI_REVIEW_MANIFEST_POINTER");
const maxOutput = 100_000;

type ReviewTask = { runId: string; prUrl: string; targetSha: string };
type ReviewManifest = { version: number; workerSessionId: string; tasks: ReviewTask[] };

function mustEnv(name: string): string {
	return resolve(mustValue(name));
}

function mustValue(name: string): string {
	const value = process.env[name]?.trim();
	if (!value) throw new Error(`missing ${name}`);
	return value;
}

function result(text: string, isError = false) {
	return { content: [{ type: "text" as const, text: truncate(text) }], details: { isError } };
}

function truncate(text: string): string {
	return text.length > maxOutput ? `${text.slice(0, maxOutput)}\n[output truncated]` : text;
}

async function allowedReadPath(input: string): Promise<string> {
	const candidate = resolve(workspace, input);
	const actual = await realpath(candidate);
	for (const root of [await realpath(workspace), await realpath(promptRoot)]) {
		const rel = relative(root, actual);
		if (rel === "" || (!rel.startsWith(`..${sep}`) && rel !== ".." && !isAbsolute(rel))) return actual;
	}
	throw new Error("read path is outside the checkout and Orchestrator prompt directory");
}

function safeRef(value: string | undefined, fallback: string): string {
	const ref = value?.trim() || fallback;
	if (ref.startsWith("-") || !/^[A-Za-z0-9._/@{}~^+-]+$/.test(ref)) throw new Error("invalid git revision");
	return ref;
}

async function run(pi: ExtensionAPI, command: string, args: string[], signal: AbortSignal) {
	const output = await pi.exec(command, args, { cwd: workspace, signal, timeout: 30_000 });
	if (output.code !== 0) throw new Error(`${command} exited ${output.code}: ${output.stderr || output.stdout}`);
	return result(output.stdout || output.stderr || "ok");
}

function pathWithin(root: string, path: string): boolean {
	const rel = relative(root, path);
	return rel === "" || (!rel.startsWith(`..${sep}`) && rel !== ".." && !isAbsolute(rel));
}

async function activeManifest(): Promise<ReviewManifest> {
	const pointerPath = await realpath(manifestPointer);
	if (pointerPath !== manifestPointer) throw new Error("active manifest pointer must be a regular Orchestrator-owned path");
	const manifestPath = await realpath((await readFile(pointerPath, "utf8")).trim());
	const manifestRoot = await realpath(join(dirname(pointerPath), "manifests"));
	if (!pathWithin(manifestRoot, manifestPath)) throw new Error("active manifest is outside the Orchestrator manifest directory");
	const parsed = JSON.parse(await readFile(manifestPath, "utf8")) as ReviewManifest;
	if (parsed.version !== 1 || parsed.workerSessionId !== workerSession || !Array.isArray(parsed.tasks) || parsed.tasks.length === 0) {
		throw new Error("invalid active review manifest");
	}
	for (const task of parsed.tasks) {
		if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(task.runId) || !/^[0-9a-fA-F]{7,64}$/.test(task.targetSha)) {
			throw new Error("invalid task in active review manifest");
		}
		const url = new URL(task.prUrl);
		if (url.hostname !== "github.com" || !url.pathname.match(/^\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/pull\/\d+\/?$/)) {
			throw new Error("invalid pull request in active review manifest");
		}
	}
	return parsed;
}

async function authorizedTask(runId: string, prUrl: string, targetSha: string): Promise<ReviewTask> {
	const manifest = await activeManifest();
	const task = manifest.tasks.find((candidate) => candidate.runId === runId);
	if (!task || task.prUrl !== prUrl || task.targetSha !== targetSha) {
		throw new Error("run, pull request, and target commit are not an exact active Orchestrator review task");
	}
	return task;
}

export default function (pi: ExtensionAPI) {
	pi.registerTool({
		name: "ao_read",
		label: "Read",
		description: "Read a UTF-8 file from the review checkout or the Orchestrator-owned review prompt directory. Cannot write files.",
		parameters: Type.Object({ path: Type.String(), offset: Type.Optional(Type.Integer({ minimum: 1 })), limit: Type.Optional(Type.Integer({ minimum: 1, maximum: 4000 })) }),
		async execute(_id, params) {
			try {
				const text = await readFile(await allowedReadPath(params.path), "utf8");
				const lines = text.split("\n");
				const start = (params.offset ?? 1) - 1;
				return result(lines.slice(start, start + (params.limit ?? 2000)).join("\n"));
			} catch (error) { return result(String(error), true); }
		},
	});

	pi.registerTool({
		name: "ao_search",
		label: "Search",
		description: "Search checkout text files for a literal string without invoking a shell.",
		parameters: Type.Object({ query: Type.String({ minLength: 1 }), path: Type.Optional(Type.String()) }),
		async execute(_id, params) {
			try {
				const root = await allowedReadPath(params.path ?? ".");
				const matches: string[] = [];
				async function walk(dir: string): Promise<void> {
					for (const entry of await readdir(dir, { withFileTypes: true })) {
						if (matches.length >= 1000 || entry.name === ".git" || entry.name === "node_modules") continue;
						const path = join(dir, entry.name);
						if (entry.isDirectory()) await walk(path);
						else if (entry.isFile()) {
							try {
								const text = await readFile(path, "utf8");
								text.split("\n").forEach((line, index) => { if (line.includes(params.query) && matches.length < 1000) matches.push(`${relative(workspace, path)}:${index + 1}:${line}`); });
							} catch { /* skip binary/unreadable files */ }
						}
					}
				}
				const stat = await readdir(root).then(() => true, () => false);
				if (stat) await walk(root); else {
					const text = await readFile(root, "utf8");
					text.split("\n").forEach((line, index) => { if (line.includes(params.query)) matches.push(`${relative(workspace, root)}:${index + 1}:${line}`); });
				}
				return result(matches.join("\n") || "no matches");
			} catch (error) { return result(String(error), true); }
		},
	});

	pi.registerTool({
		name: "git_inspect",
		label: "Git inspect",
		description: "Run one fixed read-only git inspection operation. No shell, hooks, external diffs, textconv, commits, or pushes are available.",
		parameters: Type.Object({ operation: Type.Union([Type.Literal("status"), Type.Literal("diff"), Type.Literal("log"), Type.Literal("show")]), base: Type.Optional(Type.String()), head: Type.Optional(Type.String()), path: Type.Optional(Type.String()), maxCount: Type.Optional(Type.Integer({ minimum: 1, maximum: 200 })) }),
		async execute(_id, params, signal) {
			try {
				const prefix = ["--no-pager", "--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external="];
				let args: string[];
				if (params.operation === "status") args = ["status", "--short", "--branch"];
				else if (params.operation === "log") args = ["log", "--no-ext-diff", "--no-textconv", "--oneline", "--decorate", `--max-count=${params.maxCount ?? 50}`, `${safeRef(params.base, "HEAD~20")}..${safeRef(params.head, "HEAD")}`];
				else if (params.operation === "show") args = ["show", "--no-ext-diff", "--no-textconv", "--format=fuller", "--stat", "--patch", safeRef(params.head, "HEAD")];
				else args = ["diff", "--no-ext-diff", "--no-textconv", "--stat", "--patch", `${safeRef(params.base, "HEAD~1")}...${safeRef(params.head, "HEAD")}`];
				if (params.path) args.push("--", params.path);
				return await run(pi, "git", [...prefix, ...args], signal);
			} catch (error) { return result(String(error), true); }
		},
	});

	pi.registerTool({
		name: "github_post_review",
		label: "Post GitHub review",
		description: "Post one COMMENT review, including optional inline findings, to the exact run, pull request, and target commit authorized by Orchestrator's active manifest. Returns the GitHub review id.",
		parameters: Type.Object({ runId: Type.String(), prUrl: Type.String(), targetSha: Type.String(), body: Type.String(), comments: Type.Optional(Type.Array(Type.Object({ path: Type.String(), line: Type.Integer({ minimum: 1 }), body: Type.String() }))) }),
		async execute(_id, params, signal) {
			let dir = "";
			try {
				const task = await authorizedTask(params.runId, params.prUrl, params.targetSha);
				const url = new URL(params.prUrl);
				const match = url.hostname === "github.com" && url.pathname.match(/^\/([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)\/pull\/(\d+)\/?$/);
				if (!match) throw new Error("unsupported GitHub pull request URL");
				for (const comment of params.comments ?? []) {
					const normalized = comment.path.replaceAll("\\", "/");
					if (normalized.startsWith("/") || normalized.split("/").includes("..")) throw new Error("invalid inline comment path");
				}
				dir = await mkdtemp(join(promptRoot, "github-review-"));
				const input = join(dir, "review.json");
				await writeFile(input, JSON.stringify({ event: "COMMENT", body: params.body, commit_id: task.targetSha, ...(params.comments?.length ? { comments: params.comments } : {}) }), { mode: 0o600 });
				return await run(pi, "gh", ["api", "--method", "POST", `repos/${match[1]}/${match[2]}/pulls/${match[3]}/reviews`, "--input", input, "--jq", ".id"], signal);
			} catch (error) { return result(String(error), true); }
			finally { if (dir) await rm(dir, { recursive: true, force: true }); }
		},
	});

	pi.registerTool({
		name: "ao_review_submit",
		label: "Submit Orchestrator review",
		description: "Submit structured results for run ids in Orchestrator's exact active review manifest. The worker session is fixed by Orchestrator.",
		parameters: Type.Object({ reviews: Type.Array(Type.Object({ runId: Type.String(), verdict: Type.Union([Type.Literal("approved"), Type.Literal("changes_requested")]), githubReviewId: Type.Optional(Type.String()), body: Type.String() }), { minItems: 1 }) }),
		async execute(_id, params, signal) {
			let dir = "";
			try {
				const manifest = await activeManifest();
				for (const review of params.reviews) if (!manifest.tasks.some((task) => task.runId === review.runId)) throw new Error(`run ${review.runId} is not in the active Orchestrator review manifest`);
				dir = await mkdtemp(join(promptRoot, "ao-submit-"));
				const input = join(dir, "reviews.json");
				await writeFile(input, JSON.stringify({ reviews: params.reviews }), { mode: 0o600 });
				return await run(pi, "ao", ["review", "submit", "--session", workerSession, "--reviews", input], signal);
			} catch (error) { return result(String(error), true); }
			finally { if (dir) await rm(dir, { recursive: true, force: true }); }
		},
	});
}
