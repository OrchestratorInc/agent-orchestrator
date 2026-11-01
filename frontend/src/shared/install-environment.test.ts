// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	detectCiEnvironment,
	detectInstallSource,
	installDay,
	normalizeInstallSource,
	safeOsVersion,
	type InstallProbe,
} from "./install-environment";

const probe = (over: Partial<InstallProbe>): InstallProbe => ({
	platform: "darwin",
	isPackaged: true,
	execPath: "/Applications/Agent Orchestrator.app/Contents/MacOS/agent-orchestrator",
	env: {},
	exists: () => false,
	...over,
});

describe("detectInstallSource", () => {
	it("reports dev for an unpackaged build", () => {
		expect(detectInstallSource(probe({ isPackaged: false }))).toBe("dev");
	});
	it("tells a Homebrew cask from a dmg install", () => {
		expect(detectInstallSource(probe({}))).toBe("dmg");
		expect(detectInstallSource(probe({ exists: (f) => f === "/opt/homebrew/Caskroom/agent-orchestrator" }))).toBe("brew-cask");
		expect(detectInstallSource(probe({ exists: (f) => f === "/usr/local/Caskroom/agent-orchestrator" }))).toBe("brew-cask");
	});
	it("reports exe on Windows", () => {
		expect(detectInstallSource(probe({ platform: "win32", execPath: "C:\\Users\\x\\AppData\\Local\\Programs\\ao\\agent-orchestrator.exe" }))).toBe("exe");
	});
	it("tells AppImage, deb and rpm apart on Linux", () => {
		const linux = { platform: "linux" as const, execPath: "/usr/lib/agent-orchestrator/agent-orchestrator" };
		expect(detectInstallSource(probe({ ...linux, env: { APPIMAGE: "/home/x/AO.AppImage" } }))).toBe("appimage");
		expect(detectInstallSource(probe({ ...linux, exists: (f) => f === "/var/lib/dpkg/info/agent-orchestrator.list" }))).toBe("deb");
		expect(detectInstallSource(probe(linux))).toBe("rpm");
		expect(detectInstallSource(probe({ platform: "linux", execPath: "/home/x/ao/agent-orchestrator" }))).toBeNull();
	});
});

describe("normalizeInstallSource", () => {
	it("keeps known values and never forwards free text", () => {
		expect(normalizeInstallSource("npm-bootstrap")).toBe("npm-bootstrap");
		expect(normalizeInstallSource("unknown")).toBe("unknown");
		expect(normalizeInstallSource(undefined)).toBe("unknown");
		expect(normalizeInstallSource("/Users/me/Downloads")).toBe("other");
	});
});

describe("detectCiEnvironment", () => {
	const none = () => false;
	it("flags CI env vars and containers, and honors CI=false", () => {
		expect(detectCiEnvironment({}, none)).toEqual({ isCi: false });
		expect(detectCiEnvironment({ CI: "true" }, none)).toEqual({ isCi: true, ciSignal: "ci_env" });
		expect(detectCiEnvironment({ CI: "false" }, none)).toEqual({ isCi: false });
		expect(detectCiEnvironment({ GITHUB_ACTIONS: "true" }, none)).toEqual({ isCi: true, ciSignal: "ci_env" });
		expect(detectCiEnvironment({}, (f) => f === "/.dockerenv")).toEqual({ isCi: true, ciSignal: "container" });
		expect(detectCiEnvironment({ KUBERNETES_SERVICE_HOST: "10.0.0.1" }, none)).toEqual({ isCi: true, ciSignal: "container" });
	});
});

describe("installDay / safeOsVersion", () => {
	it("coarsens the install time to a UTC day", () => {
		expect(installDay("2026-10-20T23:59:59.123Z")).toBe("2026-10-20");
		expect(installDay("garbage")).toBeUndefined();
		expect(installDay(undefined)).toBeUndefined();
	});
	it("admits only version-shaped OS versions", () => {
		expect(safeOsVersion("15.1.2")).toBe("15.1.2");
		expect(safeOsVersion("my mac")).toBeUndefined();
	});
});
