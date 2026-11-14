import path from "node:path";
import { MakerBase, type MakerOptions } from "@electron-forge/maker-base";
import type { ForgePlatform } from "@electron-forge/shared-types";

export type MakerNSISConfig = {
	appId?: string;
	productName?: string;
	executableName?: string;
	icon?: string;
	nsis?: Record<string, unknown>;
	win?: Record<string, unknown>;
};

function envSigningOptions(): Record<string, unknown> | undefined {
	const certFile = process.env.WIN_CSC_LINK;
	const certPassword = process.env.WIN_CSC_KEY_PASSWORD;
	if (certFile) {
		const signtoolOptions: Record<string, unknown> = {
			certificateFile: certFile,
		};
		if (certPassword) {
			signtoolOptions.certificatePassword = certPassword;
		}
		if (process.env.WIN_SIGNING_HASH_ALGORITHMS) {
			signtoolOptions.signingHashAlgorithms = process.env.WIN_SIGNING_HASH_ALGORITHMS.split(",")
				.map((algo) => algo.trim())
				.filter(Boolean);
		}
		return { signtoolOptions };
	}
	if (process.env.WIN_CERT_SUBJECT_NAME) {
		return { signtoolOptions: { certificateSubjectName: process.env.WIN_CERT_SUBJECT_NAME } };
	}
	if (process.env.AZURE_PUBLISHER_NAME) {
		const azureSignOptions: Record<string, unknown> = {
			publisherName: process.env.AZURE_PUBLISHER_NAME,
		};
		for (const [env, key] of [
			["AZURE_TENANT_ID", "tenantId"],
			["AZURE_CLIENT_ID", "clientId"],
			["AZURE_CLIENT_SECRET", "clientSecret"],
			["AZURE_SUBSCRIPTION_ID", "subscriptionId"],
			["AZURE_RESOURCE_GROUP_NAME", "resourceGroupName"],
			["AZURE_ACCOUNT_NAME", "accountName"],
			["AZURE_CODE_SIGNING_ACCOUNT_NAME", "codeSigningAccountName"],
		] as const) {
			if (process.env[env]) azureSignOptions[key] = process.env[env];
		}
		return { azureSignOptions };
	}
	return undefined;
}

export default class MakerNSIS extends MakerBase<MakerNSISConfig> {
	name = "nsis";
	defaultPlatforms: ForgePlatform[] = ["win32"];

	isSupportedOnCurrentPlatform(): boolean {
		return true;
	}

	async make({ dir, targetArch, appName }: MakerOptions): Promise<string[]> {
		const { buildForge } = await import("app-builder-lib");
		const cfg = this.config ?? {};
		const output = path.join(path.dirname(path.resolve(dir)), "make");
		const win: Record<string, unknown> = { ...(cfg.win ?? {}) };
		if (cfg.icon) win.icon = cfg.icon;
		if (cfg.executableName) win.executableName = cfg.executableName;
		const signing = envSigningOptions();
		if (signing) {
			for (const [key, value] of Object.entries(signing)) {
				win[key] =
					value !== null && typeof value === "object" && !Array.isArray(value)
						? { ...((win[key] as Record<string, unknown> | undefined) ?? {}), ...value }
						: value;
			}
			win.forceCodeSigning = true;
		}
		return buildForge(
			{ dir },
			{
				win: [`nsis:${targetArch}`],
				config: {
					appId: cfg.appId,
					productName: cfg.productName ?? appName,
					directories: { output },
					publish: null,
					...(Object.keys(win).length ? { win } : {}),
					nsis: {
						oneClick: false,
						perMachine: false,
						allowToChangeInstallationDirectory: true,
						createDesktopShortcut: true,
						createStartMenuShortcut: true,
						...cfg.nsis,
					},
				},
			},
		);
	}
}

export { MakerNSIS };
