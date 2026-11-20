// Renders the README's star history chart from the GitHub stargazers API.
// The workflow in .github/workflows/star-history.yml runs this daily and
// commits the SVGs to the `star-history` branch, which the README links to
// through raw.githubusercontent.com (5 minute cache, no third-party service).
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

const PAGE_SIZE = 100;
// GitHub stops listing stargazers after 400 pages. Past that the chart would
// silently flatten, so fail instead and let someone pick a new data source.
const MAX_LISTED_STARGAZERS = 40_000;
const CONCURRENCY = 8;
const DAY_MS = 86_400_000;

const WIDTH = 800;
const HEIGHT = 400;
const PLOT = { left: 64, right: 24, top: 56, bottom: 44 };
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

export const THEMES = {
	light: { text: "#1f2328", muted: "#59636e", grid: "#d1d9e0", line: "#0969da", fill: "#0969da" },
	dark: { text: "#f0f6fc", muted: "#9198a1", grid: "#3d444d", line: "#4493f8", fill: "#4493f8" },
};

const utcDay = (time) => Math.floor(time / DAY_MS) * DAY_MS;
const escapeXml = (value) =>
	String(value).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&apos;" })[c]);
const formatCount = (value) => value.toLocaleString("en-US");
const formatDate = (time) => {
	const date = new Date(time);
	return `${MONTHS[date.getUTCMonth()]} ${date.getUTCDate()}, ${date.getUTCFullYear()}`;
};
const round = (value) => Math.round(value * 10) / 10;

// One point per UTC day from the repository's creation to `now`, holding the
// cumulative star count at the end of that day.
export function dailySeries(starredAt, { createdAt, now }) {
	const times = starredAt.map((t) => Date.parse(t)).sort((a, b) => a - b);
	const first = utcDay(Math.min(Date.parse(createdAt), times[0] ?? Infinity));
	const last = utcDay(now);
	const series = [];
	let index = 0;
	for (let day = first; day <= last; day += DAY_MS) {
		while (index < times.length && times[index] < day + DAY_MS) index += 1;
		series.push({ day, stars: index });
	}
	return series;
}

// Round axis steps (1, 2, 2.5 or 5 times a power of ten) giving about five gridlines.
export function niceStep(max) {
	const raw = Math.max(max, 1) / 5;
	const power = 10 ** Math.floor(Math.log10(raw));
	// Stars are whole numbers, so never step by less than one.
	return Math.max(1, [1, 2, 2.5, 5, 10].find((m) => m * power >= raw) * power);
}

const formatTick = (value) => (value >= 1000 ? `${value / 1000}K` : String(value));

export function renderSvg(series, { repo, theme }) {
	const colors = THEMES[theme];
	const plotWidth = WIDTH - PLOT.left - PLOT.right;
	const plotHeight = HEIGHT - PLOT.top - PLOT.bottom;
	const firstDay = series[0].day;
	const lastDay = series.at(-1).day;
	const total = series.at(-1).stars;
	const step = niceStep(total);
	const yMax = Math.ceil(Math.max(total, 1) / step) * step;
	const x = (day) => PLOT.left + (lastDay === firstDay ? plotWidth : ((day - firstDay) / (lastDay - firstDay)) * plotWidth);
	const y = (stars) => PLOT.top + plotHeight - (stars / yMax) * plotHeight;
	const bottom = PLOT.top + plotHeight;

	const grid = [];
	for (let value = 0; value <= yMax; value += step) {
		grid.push(
			`<line x1="${PLOT.left}" x2="${WIDTH - PLOT.right}" y1="${round(y(value))}" y2="${round(y(value))}" stroke="${colors.grid}" stroke-width="1"${value === 0 ? "" : ' stroke-dasharray="2 4"'}/>`,
			`<text x="${PLOT.left - 10}" y="${round(y(value))}" fill="${colors.muted}" font-size="12" text-anchor="end" dominant-baseline="middle">${formatTick(value)}</text>`,
		);
	}

	// Label the first of each month; name the year on January and on the first label.
	const start = new Date(firstDay);
	const months = [];
	for (let month = Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + 1, 1); month <= lastDay; ) {
		months.push(month);
		const date = new Date(month);
		month = Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 1);
	}
	const labelEvery = Math.ceil(months.length / 12);
	const ticks = months
		.filter((_, index) => (months.length - 1 - index) % labelEvery === 0)
		.map((month, index) => {
			const date = new Date(month);
			const label = MONTHS[date.getUTCMonth()] + (index === 0 || date.getUTCMonth() === 0 ? ` ${date.getUTCFullYear()}` : "");
			return [
				`<line x1="${round(x(month))}" x2="${round(x(month))}" y1="${bottom}" y2="${bottom + 5}" stroke="${colors.grid}" stroke-width="1"/>`,
				`<text x="${round(x(month))}" y="${bottom + 20}" fill="${colors.muted}" font-size="12" text-anchor="middle">${label}</text>`,
			].join("");
		});

	const points = series.map((point) => `${round(x(point.day))},${round(y(point.stars))}`);
	const line = `M${points.join("L")}`;
	const area = `${line}L${round(x(lastDay))},${bottom}L${round(x(firstDay))},${bottom}Z`;
	const endX = round(x(lastDay));
	const endY = round(y(total));
	const summary = `${formatCount(total)} stars · ${formatDate(lastDay)}`;

	return [
		`<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}" role="img" aria-label="${escapeXml(`Star history for ${repo}: ${summary}`)}" font-family="-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif">`,
		`<title>${escapeXml(`Star history for ${repo}: ${summary}`)}</title>`,
		`<text x="${PLOT.left}" y="28" fill="${colors.text}" font-size="16" font-weight="600">${escapeXml(repo)}</text>`,
		`<text x="${WIDTH - PLOT.right}" y="28" fill="${colors.muted}" font-size="14" text-anchor="end">${escapeXml(summary)}</text>`,
		...grid,
		...ticks,
		`<path d="${area}" fill="${colors.fill}" fill-opacity="0.12"/>`,
		`<path d="${line}" fill="none" stroke="${colors.line}" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`,
		`<circle cx="${endX}" cy="${endY}" r="4" fill="${colors.line}"/>`,
		"</svg>",
		"",
	].join("\n");
}

async function request(url, token) {
	const response = await fetch(url, {
		headers: {
			Accept: "application/vnd.github.star+json",
			Authorization: `Bearer ${token}`,
			"X-GitHub-Api-Version": "2022-11-28",
			"User-Agent": "star-history-chart",
		},
	});
	if (!response.ok) throw new Error(`GET ${url} failed: ${response.status} ${await response.text()}`);
	return response.json();
}

export async function fetchStargazers(repo, token) {
	const info = await request(`https://api.github.com/repos/${repo}`, token);
	if (info.stargazers_count > MAX_LISTED_STARGAZERS) {
		throw new Error(`${repo} has ${info.stargazers_count} stars; GitHub lists only the first ${MAX_LISTED_STARGAZERS}.`);
	}
	const pages = Math.ceil(info.stargazers_count / PAGE_SIZE) + 1;
	// Stars that land mid-fetch shift entries across pages; key by login to drop repeats.
	const starredAt = new Map();
	for (let first = 1; first <= pages; first += CONCURRENCY) {
		const batch = [];
		for (let page = first; page < first + CONCURRENCY && page <= pages; page += 1) {
			batch.push(request(`https://api.github.com/repos/${repo}/stargazers?per_page=${PAGE_SIZE}&page=${page}`, token));
		}
		for (const stargazers of await Promise.all(batch)) {
			for (const stargazer of stargazers) starredAt.set(stargazer.user.login, stargazer.starred_at);
		}
	}
	return { createdAt: info.created_at, starredAt: [...starredAt.values()] };
}

export async function run({ repo, token, outDir, now = Date.now() }) {
	const { createdAt, starredAt } = await fetchStargazers(repo, token);
	const series = dailySeries(starredAt, { createdAt, now });
	await mkdir(outDir, { recursive: true });
	await writeFile(join(outDir, "star-history.svg"), renderSvg(series, { repo, theme: "light" }));
	await writeFile(join(outDir, "star-history-dark.svg"), renderSvg(series, { repo, theme: "dark" }));
	return series.at(-1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const [outDir = "star-history"] = process.argv.slice(2);
	const { GITHUB_REPOSITORY: repo, GITHUB_TOKEN: token } = process.env;
	if (!repo || !token) throw new Error("GITHUB_REPOSITORY and GITHUB_TOKEN are required.");
	const last = await run({ repo, token, outDir });
	console.log(`${repo}: ${last.stars} stars on ${new Date(last.day).toISOString().slice(0, 10)}`);
}
