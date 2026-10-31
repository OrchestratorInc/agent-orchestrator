import path from "node:path";

/**
 * How this install arrived, recorded once in ~/.ao/app-state.json and sent as
 * install_source on product telemetry. The daemon reads the same file and
 * normalizes against the same list (backend/internal/adapters/telemetry/
 * environment.go InstallSources); keep the two in step.
 *
 * - npm-bootstrap: `ao start` fetched and opened the app (--installed-via).
 * - dmg / brew-cask / exe / appimage / deb / rpm: the desktop installer,
 *   detected from the running bundle.
 * - dev: an unpackaged build.
 * - unknown: nothing recorded and nothing detectable.
 */
export const INSTALL_SOURCES = ["npm-bootstrap", "dmg", "brew-cask", "exe", "appimage", "deb", "rpm", "dev", "unknown"] as const;
export type InstallSource = (typeof INSTALL_SOURCES)[number];

/** Telemetry value for a recorded source: off-list text becomes "other". */
export function normalizeInstallSource(raw: string | undefined | null): InstallSource | "other" {
	const value = raw?.trim() ?? "";
	if (!value) return "unknown";
	return (INSTALL_SOURCES as readonly string[]).includes(value) ? (value as InstallSource) : "other";
}

export interface InstallProbe {
	platform: NodeJS.Platform;
	isPackaged: boolean;
	/** process.execPath of the running app. */
	execPath: string;
	env: Record<string, string | undefined>;
	exists: (file: string) => boolean;
}

// The Homebrew cask (AgentWrapper/homebrew-tap) and the deb/rpm package are
// both named after the package.json name.
const PACKAGE_NAME = "agent-orchestrator";

/**
 * Detects the installer format from the running bundle. Returns null when the
 * format cannot be told apart, so the caller keeps "unknown" instead of guessing.
 */
export function detectInstallSource(probe: InstallProbe): InstallSource | null {
	if (!probe.isPackaged) return "dev";
	switch (probe.platform) {
		case "darwin":
			// A cask install leaves its receipt in the Caskroom; the app itself
			// lands in /Applications exactly like a dmg drag-install.
			for (const prefix of ["/opt/homebrew", "/usr/local"]) {
				if (probe.exists(path.posix.join(prefix, "Caskroom", PACKAGE_NAME))) return "brew-cask";
			}
			return "dmg";
		case "win32":
			// Windows ships only the NSIS installer.
			return "exe";
		case "linux":
			// The AppImage runtime sets APPIMAGE to the image path.
			if (probe.env.APPIMAGE) return "appimage";
			if (probe.exists(`/var/lib/dpkg/info/${PACKAGE_NAME}.list`)) return "deb";
			// deb and rpm both install under /usr; without a dpkg record it is rpm.
			if (probe.execPath.startsWith("/usr/")) return "rpm";
			return null;
		default:
			return null;
	}
}

/**
 * The install source to record. A source captured on an earlier launch is
 * kept, except "unknown", which a detected installer format refines: builds
 * before detection recorded every non-npm install as unknown. An
 * --installed-via value wins on first creation.
 */
export function resolveInstallSource(existing: string | undefined, installedVia: string | undefined, detected: InstallSource | null): string {
	if (existing && existing !== "unknown") return existing;
	if (!existing && installedVia) return installedVia;
	return detected ?? existing ?? "unknown";
}

// Common CI providers. CI itself is checked separately so CI=false opts out.
const CI_ENV_VARS = [
	"GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL",
	"HUDSON_URL", "TEAMCITY_VERSION", "TF_BUILD", "BITBUCKET_BUILD_NUMBER",
	"CODEBUILD_BUILD_ID", "DRONE", "APPVEYOR", "SEMAPHORE", "BITRISE_IO", "CIRRUS_CI",
	"BUILDKITE_AGENT_ID", "WOODPECKER", "HARNESS_BUILD_ID",
];

export type CiSignal = "ci_env" | "container";

/**
 * Flags a CI runner or a container, mirroring the daemon's detectCI, so
 * dashboards can exclude one-shot fleets. ci_signal says which: a dev
 * container can be a real person.
 */
export function detectCiEnvironment(
	env: Record<string, string | undefined>,
	exists: (file: string) => boolean,
): { isCi: boolean; ciSignal?: CiSignal } {
	const ci = env.CI?.trim().toLowerCase();
	if (ci && ci !== "false" && ci !== "0") return { isCi: true, ciSignal: "ci_env" };
	if (CI_ENV_VARS.some((name) => env[name]?.trim())) return { isCi: true, ciSignal: "ci_env" };
	if (env.KUBERNETES_SERVICE_HOST || env.container || exists("/.dockerenv") || exists("/run/.containerenv")) {
		return { isCi: true, ciSignal: "container" };
	}
	return { isCi: false };
}

/** The recorded install time coarsened to its UTC day, or undefined. */
export function installDay(raw: string | undefined): string | undefined {
	if (!raw) return undefined;
	const t = new Date(raw);
	return Number.isNaN(t.getTime()) ? undefined : t.toISOString().slice(0, 10);
}

/** Only a version-shaped token (15.1.2, 10.0.22631), never free text. */
export function safeOsVersion(raw: string | undefined): string | undefined {
	const v = raw?.trim() ?? "";
	return /^[0-9][0-9A-Za-z.+_-]{0,63}$/.test(v) ? v : undefined;
}
