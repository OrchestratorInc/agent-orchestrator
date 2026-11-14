import { readFileSync } from "node:fs";
import path from "node:path";
import { app } from "electron";
import type { UpdateSettings } from "./update-settings";

const GITHUB_API = "https://api.github.com";

const DEFAULT_REPO = { owner: "Untrivial-ai", repo: "agent-orchestrator" } as const;

let cachedRepo: { owner: string; repo: string } | undefined;
function resolveRepo(): { owner: string; repo: string } {
	if (cachedRepo) return cachedRepo;
	cachedRepo = { ...DEFAULT_REPO };
	try {
		if (app.isPackaged) {
			const yml = readFileSync(path.join(process.resourcesPath, "app-update.yml"), "utf8");
			const owner = /^owner:\s*(.+)$/m.exec(yml)?.[1]?.trim();
			const repo = /^repo:\s*(.+)$/m.exec(yml)?.[1]?.trim();
			if (owner && repo) cachedRepo = { owner, repo };
		}
	} catch {
	}
	return cachedRepo;
}

const FEATURE_BUILD_MARKER = "<!-- ao-feature-build:";

const MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;

export interface FeatureBuild {
	pr: number;
	title: string;
	base: string;
	sha: string;
	slug: string;
	buildId: string;
	publishedAt: string;
}

export function parseFeatureBuild(version: string): { pr: number } | null {
	const m = version.match(/-pr(\d+)\.\d{12}/);
	if (!m) return null;
	const pr = parseInt(m[1], 10);
	return Number.isFinite(pr) && pr > 0 ? { pr } : null;
}

export function getActiveFeatureBuild(): { pr: number } | null {
	return parseFeatureBuild(app.getVersion());
}

interface GitHubRelease {
	tag_name: string;
	name: string;
	prerelease: boolean;
	published_at: string;
	body: string | null;
}

interface MarkerPayload {
	pr: number;
	base: string;
	sha: string;
	slug: string;
}

function parseMarker(body: string): MarkerPayload | null {
	const idx = body.indexOf(FEATURE_BUILD_MARKER);
	if (idx === -1) return null;
	const start = idx + FEATURE_BUILD_MARKER.length;
	const end = body.indexOf("-->", start);
	if (end === -1) return null;
	try {
		const payload = JSON.parse(body.slice(start, end).trim()) as MarkerPayload;
		if (typeof payload.pr !== "number" || typeof payload.base !== "string") return null;
		return payload;
	} catch {
		return null;
	}
}

async function fetchJson<T>(url: string): Promise<T> {
	const res = await fetch(url, {
		headers: {
			Accept: "application/vnd.github+json",
			"X-GitHub-Api-Version": "2022-11-28",
			"User-Agent": `ao-desktop/${app.getVersion()}`,
		},
	});
	if (!res.ok) throw new Error(`GitHub API ${res.status}: ${url}`);
	return res.json() as Promise<T>;
}

async function isPrOpen(pr: number): Promise<boolean> {
	try {
		const { owner, repo } = resolveRepo();
		const data = await fetchJson<{ state: string }>(`${GITHUB_API}/repos/${owner}/${repo}/pulls/${pr}`);
		return data.state === "open";
	} catch {
		return true;
	}
}

async function collectFeatureBuilds(): Promise<FeatureBuild[]> {
	const { owner, repo } = resolveRepo();
	const releases = await fetchJson<GitHubRelease[]>(`${GITHUB_API}/repos/${owner}/${repo}/releases?per_page=100`);

	const now = Date.now();
	const cutoff = now - MAX_AGE_MS;

	interface Candidate extends FeatureBuild {
		publishedMs: number;
	}

	const candidates: Candidate[] = [];
	for (const rel of releases) {
		if (!rel.prerelease) continue;
		const publishedMs = new Date(rel.published_at).getTime();
		if (publishedMs < cutoff) continue;
		const body = rel.body ?? "";
		const marker = parseMarker(body);
		if (!marker) continue;
		candidates.push({
			pr: marker.pr,
			title: rel.name,
			base: marker.base,
			sha: marker.sha,
			slug: marker.slug,
			buildId: rel.tag_name,
			publishedAt: rel.published_at,
			publishedMs,
		});
	}

	if (candidates.length === 0) return [];

	const uniquePrs = [...new Set(candidates.map((c) => c.pr))];
	const openMap = new Map<number, boolean>();
	await Promise.all(
		uniquePrs.map(async (pr) => {
			openMap.set(pr, await isPrOpen(pr));
		}),
	);

	const bestByPr = new Map<number, Candidate>();
	for (const c of candidates) {
		if (!openMap.get(c.pr)) continue;
		const existing = bestByPr.get(c.pr);
		if (!existing || c.publishedMs > existing.publishedMs) {
			bestByPr.set(c.pr, c);
		}
	}

	const results = [...bestByPr.values()].sort((a, b) => b.publishedMs - a.publishedMs);

	return results.map(({ publishedMs: _ms, ...rest }) => rest);
}

export async function listFeatureBuilds(): Promise<FeatureBuild[]> {
	try {
		return await collectFeatureBuilds();
	} catch (err) {
		console.warn("[feature-builds] failed to list feature builds:", err);
		return [];
	}
}

export async function reconcileFeaturePin(
	settings: UpdateSettings,
): Promise<{ settings: UpdateSettings; cleared: boolean }> {
	if (!settings.feature) return { settings, cleared: false };
	let builds: FeatureBuild[];
	try {
		builds = await collectFeatureBuilds();
	} catch {
		return { settings, cleared: false };
	}
	if (builds.some((b) => b.pr === settings.feature!.pr)) return { settings, cleared: false };
	return { settings: { ...settings, feature: null }, cleared: true };
}
