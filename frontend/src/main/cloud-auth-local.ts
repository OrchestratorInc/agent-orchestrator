import { app, ipcMain } from "electron";
import type {
	CloudAccount,
	CloudOrganization,
} from "../shared/cloud-account";
import {
	invalidateAuthOperations,
	isLocalSession,
	publicAccount,
	type StoredSession,
	withAuthMutation,
	writeAuthStore,
} from "./cloud-auth";

const LOCAL_AUTH_PREFIX = "/api/cloud/v1/auth/local";
export const LOCAL_TOKEN_PREFIX = "ao_local_";

export interface LocalAuthGateOptions {
	isPackaged: boolean;
	devEnvEnabled: boolean;
	cpUrl: string;
}

export function isLoopbackCpUrl(cpUrl: string): boolean {
	let url: URL;
	try {
		url = new URL(cpUrl);
	} catch {
		return false;
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") return false;
	return url.hostname === "127.0.0.1" || url.hostname === "localhost";
}

export function localAuthAllowed(options: LocalAuthGateOptions): boolean {
	const devAllowed = !options.isPackaged || options.devEnvEnabled;
	return devAllowed && isLoopbackCpUrl(options.cpUrl);
}

export function localAuthAvailable(cpUrl: string): boolean {
	return localAuthAllowed({
		isPackaged: app.isPackaged,
		devEnvEnabled: process.env.AO_CLOUD_LOCAL_AUTH_DEV === "1",
		cpUrl,
	});
}

export class LocalAuthError extends Error {
	constructor(
		readonly status: number,
		message: string,
	) {
		super(message);
		this.name = "LocalAuthError";
	}
}

export interface LocalRegisterInput {
	cpUrl: string;
	email: string;
	displayName: string;
	password: string;
	orgSlug: string;
	orgName: string;
}

export interface LocalLoginInput {
	cpUrl: string;
	email: string;
	password: string;
}

export interface LocalAuthOptions {
	fetchImpl?: typeof fetch;
}

interface AuthResponse {
	token: string;
	expiresAt?: string;
	user: {
		id: string;
		email: string;
		displayName: string;
		authProvider?: string;
	};
	organizations?: CloudOrganization[];
}

function normalizeCpUrl(cpUrl: string): string {
	return new URL(cpUrl).origin;
}

function errorMessage(parsed: unknown, status: number): string {
	if (parsed && typeof parsed === "object") {
		const body = parsed as { message?: unknown; error?: unknown };
		if (typeof body.message === "string" && body.message !== "") return body.message;
		if (typeof body.error === "string" && body.error !== "") return body.error;
	}
	return `The control plane rejected the request (status ${status}).`;
}

function isAuthResponse(value: unknown): value is AuthResponse {
	if (!value || typeof value !== "object") return false;
	const body = value as Record<string, unknown>;
	if (typeof body.token !== "string" || body.token === "") return false;
	const user = body.user;
	if (!user || typeof user !== "object") return false;
	const u = user as Record<string, unknown>;
	return typeof u.id === "string" && typeof u.email === "string";
}

async function postLocalAuth(
	cpUrl: string,
	route: "register" | "login",
	body: unknown,
	fetchImpl: typeof fetch,
): Promise<AuthResponse> {
	if (!isLoopbackCpUrl(cpUrl)) {
		throw new LocalAuthError(0, "Local sign-in is only allowed against a loopback control plane.");
	}
	const url = `${normalizeCpUrl(cpUrl)}${LOCAL_AUTH_PREFIX}/${route}`;
	let response: Response;
	try {
		response = await fetchImpl(url, {
			method: "POST",
			headers: { "Content-Type": "application/json", Accept: "application/json" },
			body: JSON.stringify(body),
		});
	} catch (error) {
		throw new LocalAuthError(0, error instanceof Error ? error.message : String(error));
	}
	const text = await response.text();
	let parsed: unknown = null;
	if (text !== "") {
		try {
			parsed = JSON.parse(text);
		} catch {
			parsed = null;
		}
	}
	if (!response.ok) {
		throw new LocalAuthError(response.status, errorMessage(parsed, response.status));
	}
	if (!isAuthResponse(parsed)) {
		throw new LocalAuthError(response.status, "The control plane returned an unexpected response.");
	}
	return parsed;
}

function toStoredSession(cpUrl: string, response: AuthResponse): StoredSession {
	return {
		authProvider: "local",
		accessToken: response.token,
		cpBaseUrl: normalizeCpUrl(cpUrl),
		user: {
			id: response.user.id,
			email: response.user.email,
			displayName: response.user.displayName || response.user.email,
		},
		organizations: response.organizations ?? [],
		storedAt: new Date().toISOString(),
	};
}

async function persistLocalSession(
	dataDir: string,
	session: StoredSession,
): Promise<CloudAccount> {
	invalidateAuthOperations(dataDir);
	return withAuthMutation(dataDir, async () => {
		await writeAuthStore(dataDir, { session, pkce: null });
		return publicAccount(session);
	});
}

export async function registerLocal(
	dataDir: string,
	input: LocalRegisterInput,
	options: LocalAuthOptions = {},
): Promise<CloudAccount> {
	const fetchImpl = options.fetchImpl ?? fetch;
	const response = await postLocalAuth(
		input.cpUrl,
		"register",
		{
			email: input.email,
			displayName: input.displayName,
			password: input.password,
			orgSlug: input.orgSlug,
			orgName: input.orgName,
		},
		fetchImpl,
	);
	return persistLocalSession(dataDir, toStoredSession(input.cpUrl, response));
}

export async function loginLocal(
	dataDir: string,
	input: LocalLoginInput,
	options: LocalAuthOptions = {},
): Promise<CloudAccount> {
	const fetchImpl = options.fetchImpl ?? fetch;
	const response = await postLocalAuth(
		input.cpUrl,
		"login",
		{ email: input.email, password: input.password },
		fetchImpl,
	);
	return persistLocalSession(dataDir, toStoredSession(input.cpUrl, response));
}

export async function revokeLocalSession(
	session: StoredSession,
	options: LocalAuthOptions = {},
): Promise<void> {
	if (!isLocalSession(session) || !session.cpBaseUrl || !session.accessToken) return;
	if (!isLoopbackCpUrl(session.cpBaseUrl)) return;
	const fetchImpl = options.fetchImpl ?? fetch;
	try {
		await fetchImpl(`${normalizeCpUrl(session.cpBaseUrl)}${LOCAL_AUTH_PREFIX}/logout`, {
			method: "POST",
			headers: { Authorization: `Bearer ${session.accessToken}` },
		});
	} catch {
	}
}

export function installCloudLocalAuthIPC(
	getDataDir: () => string,
	notifyRenderers: (session: CloudAccount | null) => void,
): void {
	ipcMain.handle("cloud:localAuthAvailable", (_event, cpUrl: unknown) =>
		typeof cpUrl === "string" && localAuthAvailable(cpUrl),
	);

	ipcMain.handle("cloud:localRegister", async (_event, input: LocalRegisterInput) => {
		if (!localAuthAvailable(input.cpUrl)) {
			throw new Error("Local sign-in is not available for this build or control plane.");
		}
		const account = await registerLocal(getDataDir(), input);
		notifyRenderers(account);
		return account;
	});

	ipcMain.handle("cloud:localLogin", async (_event, input: LocalLoginInput) => {
		if (!localAuthAvailable(input.cpUrl)) {
			throw new Error("Local sign-in is not available for this build or control plane.");
		}
		const account = await loginLocal(getDataDir(), input);
		notifyRenderers(account);
		return account;
	});
}
