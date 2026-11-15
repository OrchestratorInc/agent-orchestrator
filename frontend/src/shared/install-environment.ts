import path from "node:path";

export const INSTALL_SOURCES = ["npm-bootstrap", "dmg", "brew-cask", "exe", "appimage", "deb", "rpm", "dev", "unknown"] as const;
export type InstallSource = (typeof INSTALL_SOURCES)[number];

export function normalizeInstallSource(raw: string | undefined | null): InstallSource | "other" {
	const value = raw?.trim() ?? "";
	if (!value) return "unknown";
	return (INSTALL_SOURCES as readonly string[]).includes(value) ? (value as InstallSource) : "other";
}

export interface InstallProbe {
	platform: NodeJS.Platform;
	isPackaged: boolean;
	execPath: string;
	env: Record<string, string | undefined>;
	exists: (file: string) => boolean;
}

const PACKAGE_NAME = "agent-orchestrator";

export function detectInstallSource(probe: InstallProbe): InstallSource | null {
	if (!probe.isPackaged) return "dev";
	switch (probe.platform) {
		case "darwin":
			for (const prefix of ["/opt/homebrew", "/usr/local"]) {
				if (probe.exists(path.posix.join(prefix, "Caskroom", PACKAGE_NAME))) return "brew-cask";
			}
			return "dmg";
		case "win32":
			return "exe";
		case "linux":
			if (probe.env.APPIMAGE) return "appimage";
			if (probe.exists(`/var/lib/dpkg/info/${PACKAGE_NAME}.list`)) return "deb";
			if (probe.execPath.startsWith("/usr/")) return "rpm";
			return null;
		default:
			return null;
	}
}

export function resolveInstallSource(existing: string | undefined, installedVia: string | undefined, detected: InstallSource | null): string {
	if (existing && existing !== "unknown") return existing;
	if (!existing && installedVia) return installedVia;
	return detected ?? existing ?? "unknown";
}

const CI_ENV_VARS = [
	"GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL",
	"HUDSON_URL", "TEAMCITY_VERSION", "TF_BUILD", "BITBUCKET_BUILD_NUMBER",
	"CODEBUILD_BUILD_ID", "DRONE", "APPVEYOR", "SEMAPHORE", "BITRISE_IO", "CIRRUS_CI",
	"BUILDKITE_AGENT_ID", "WOODPECKER", "HARNESS_BUILD_ID",
];

export function detectCiEnvironment(
	env: Record<string, string | undefined>,
	exists: (file: string) => boolean,
): { isCi: boolean; isContainer: boolean } {
	const isContainer = Boolean(env.KUBERNETES_SERVICE_HOST || env.container || exists("/.dockerenv") || exists("/run/.containerenv"));
	const ci = env.CI?.trim().toLowerCase();
	return { isCi: Boolean((ci && ci !== "false" && ci !== "0") || CI_ENV_VARS.some((name) => env[name]?.trim())), isContainer };
}

export function installDay(raw: string | undefined): string | undefined {
	if (!raw) return undefined;
	const t = new Date(raw);
	return Number.isNaN(t.getTime()) ? undefined : t.toISOString().slice(0, 10);
}

export function safeOsVersion(raw: string | undefined): string | undefined {
	const v = raw?.trim() ?? "";
	return /^[0-9][0-9A-Za-z.+_-]{0,63}$/.test(v) ? v : undefined;
}
