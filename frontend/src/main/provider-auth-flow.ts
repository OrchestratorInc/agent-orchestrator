import { type ChildProcess, spawn, type SpawnOptions } from "node:child_process";
import { constants as fsConstants } from "node:fs";
import { access, chmod, lstat, mkdtemp, mkdir, readdir, readFile, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import os from "node:os";
import path from "node:path";
import { shell } from "electron";
import crypto from "node:crypto";

const MAX_AUTH_DOCUMENT_BYTES = 64 << 10;

const CLAUDE_OAUTH_TOKEN_PATTERN = /sk-ant-oat[0-9A-Za-z_-]{10,}/;

export function extractClaudeOAuthToken(text: string): string | null {
	const match = text.match(CLAUDE_OAUTH_TOKEN_PATTERN);
	return match ? match[0] : null;
}

export async function readClaudeOAuthTokenFromDir(dir: string): Promise<string | null> {
	let entries: string[];
	try {
		entries = await readdir(dir);
	} catch {
		return null;
	}
	for (const entry of entries) {
		const full = path.join(dir, entry);
		try {
			const stat = await lstat(full);
			if (!stat.isFile() || stat.size === 0 || stat.size > MAX_AUTH_DOCUMENT_BYTES) continue;
			const token = extractClaudeOAuthToken(await readFile(full, "utf8"));
			if (token) return token;
		} catch {
		}
	}
	return null;
}

export async function findCodexAuthFile(codexHome: string): Promise<string | null> {
	const direct = path.join(codexHome, "auth.json");
	try {
		await access(direct, fsConstants.R_OK);
		return direct;
	} catch {
	}
	let entries: string[];
	try {
		entries = await readdir(codexHome);
	} catch {
		return null;
	}
	for (const entry of entries) {
		if (entry === "auth.json") return path.join(codexHome, entry);
		const nested = path.join(codexHome, entry, "auth.json");
		try {
			await access(nested, fsConstants.R_OK);
			return nested;
		} catch {
		}
	}
	return null;
}

function knownBinDirs(): string[] {
	const home = os.homedir();
	return [
		path.join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		path.join(home, ".npm-global", "bin"),
		path.join(home, ".bun", "bin"),
		path.join(home, ".volta", "bin"),
		"/opt/local/bin",
		"/usr/bin",
		"/bin",
	];
}

export async function firstExecutable(name: string, dirs: readonly string[]): Promise<string | null> {
	const names = process.platform === "win32" ? [`${name}.cmd`, `${name}.exe`, name] : [name];
	const seen = new Set<string>();
	for (const dir of dirs) {
		if (!dir || seen.has(dir)) continue;
		seen.add(dir);
		for (const candidateName of names) {
			const candidate = path.join(dir, candidateName);
			try {
				await access(candidate, fsConstants.X_OK);
				return candidate;
			} catch {
			}
		}
	}
	return null;
}

function resolveViaLoginShell(name: string): Promise<string | null> {
	if (process.platform === "win32") return Promise.resolve(null);
	const shell = process.env.SHELL || "/bin/zsh";
	return new Promise((resolve) => {
		let out = "";
		let settled = false;
		const child = spawn(shell, ["-lic", `command -v ${name} 2>/dev/null`], {
			stdio: ["ignore", "pipe", "ignore"],
		});
		const finish = (value: string | null) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			try {
				child.kill();
			} catch {
			}
			resolve(value);
		};
		const timer = setTimeout(() => finish(null), 4000);
		child.stdout.on("data", (chunk: Buffer) => {
			out += chunk.toString();
		});
		child.once("error", () => finish(null));
		child.once("exit", () => {
			const resolved = out
				.split("\n")
				.map((line) => line.trim())
				.filter(Boolean)
				.pop();
			finish(resolved && path.isAbsolute(resolved) ? resolved : null);
		});
	});
}

async function resolveProviderBinary(name: string): Promise<{ path: string; pathEnv: string } | null> {
	const inherited = (process.env.PATH ?? "").split(path.delimiter);
	let resolved = await firstExecutable(name, [...inherited, ...knownBinDirs()]);
	if (!resolved) resolved = await resolveViaLoginShell(name);
	if (!resolved) return null;
	const pathEnv = [path.dirname(resolved), ...knownBinDirs(), ...inherited]
		.filter(Boolean)
		.join(path.delimiter);
	return { path: resolved, pathEnv };
}

function spawnAgentBinary(binaryPath: string, args: readonly string[], options: SpawnOptions): ChildProcess {
	const useShell = process.platform === "win32";
	return spawn(useShell ? `"${binaryPath}"` : binaryPath, [...args], { ...options, shell: useShell });
}

export interface ProviderAuthCredential {
	provider: string;
	credentialType: string;
	secret: string;
	refreshToken?: string;
	expiresIn?: number;
	refreshTokenExpiresIn?: number;
}

export interface ProviderAuthFlow {
	provider: string;
	authenticate(dataDir: string, signal?: AbortSignal): Promise<ProviderAuthCredential>;
}

const codexAuthFlow: ProviderAuthFlow = {
	provider: "codex",
	async authenticate(dataDir: string, signal?: AbortSignal): Promise<ProviderAuthCredential> {
		await mkdir(dataDir, { recursive: true, mode: 0o700 });
		await chmod(dataDir, 0o700);
		const pending = await mkdtemp(path.join(dataDir, "codex-cloud-login-"));
		const codexHome = path.join(pending, "home");
		try {
			await mkdir(codexHome, { recursive: true, mode: 0o700 });
			await chmod(codexHome, 0o700);
			const binary = await resolveProviderBinary("codex");
			if (!binary) {
				throw new Error(
					'Codex is not installed or could not be found. Install the Codex CLI, or connect with the "API key" credential type instead.',
				);
			}
			const readCodexCredential = async (): Promise<string | null> => {
				const authPath = await findCodexAuthFile(codexHome).catch(() => null);
				if (!authPath) return null;
				let authFile: Buffer;
				try {
					authFile = await readFile(authPath);
				} catch {
					return null;
				}
				if (authFile.byteLength === 0 || authFile.byteLength > MAX_AUTH_DOCUMENT_BYTES) return null;
				const secret = authFile.toString("utf8");
				try {
					const document: unknown = JSON.parse(secret);
					if (typeof document !== "object" || document === null || Array.isArray(document)) return null;
				} catch {
					return null;
				}
				return secret;
			};

			const secret = await new Promise<string>((resolve, reject) => {
				const child = spawnAgentBinary(binary.path, ["-c", 'cli_auth_credentials_store="file"', "login"], {
					env: { ...process.env, PATH: binary.pathEnv, CODEX_HOME: codexHome },
					stdio: "ignore",
				});

				let settled = false;
				let timeout: NodeJS.Timeout;
				let poll: NodeJS.Timeout;
				const cleanup = () => {
					clearTimeout(timeout);
					clearInterval(poll);
					signal?.removeEventListener("abort", onAbort);
					try {
						child.kill();
					} catch {
					}
				};
				const succeed = (value: string) => {
					if (settled) return;
					settled = true;
					cleanup();
					resolve(value);
				};
				const fail = (err: Error) => {
					if (settled) return;
					settled = true;
					cleanup();
					reject(err);
				};

				poll = setInterval(() => {
					void readCodexCredential().then((value) => {
						if (value) succeed(value);
					});
				}, 1000);

				const onAbort = () => fail(new Error("Login was cancelled."));
				if (signal?.aborted) return onAbort();
				signal?.addEventListener("abort", onAbort);

				timeout = setTimeout(() => fail(new Error("Login timed out after 5 minutes.")), 5 * 60 * 1000);

				child.once("error", () =>
					fail(new Error('Codex could not start. Connect with the "API key" credential type instead.')),
				);
				child.once("exit", (code) => {
					void readCodexCredential().then((value) => {
						if (value) return succeed(value);
						fail(
							new Error(
								code === 0
									? 'Codex sign-in did not create a credential. Connect with the "API key" credential type instead.'
									: "Codex sign-in did not complete.",
							),
						);
					});
				});
			});
			return { provider: "codex", credentialType: "auth_json", secret };
		} finally {
			await rm(pending, { recursive: true, force: true });
		}
	},
};

function base64Url(buf: Buffer): string {
	return buf.toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

const CLAUDE_OAUTH_CLIENT_ID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e";
const CLAUDE_OAUTH_AUTHORIZE_URL = "https://claude.com/cai/oauth/authorize";
const CLAUDE_OAUTH_TOKEN_URL = "https://platform.claude.com/v1/oauth/token";
const CLAUDE_OAUTH_SCOPE = "user:inference";
const CLAUDE_OAUTH_TOKEN_TTL_SECONDS = 31_536_000;

const CLAUDE_CALLBACK_HTML = (title: string, body: string): string =>
	`<!doctype html><meta charset="utf-8"><title>${title}</title>` +
	`<body style="font:15px -apple-system,system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#111">` +
	`<h1 style="font-size:1.25rem">${title}</h1><p style="color:#555">${body}</p></body>`;

export function buildClaudeAuthorizeUrl(redirectUri: string, codeChallenge: string, state: string): string {
	const params = new URLSearchParams({
		code: "true",
		client_id: CLAUDE_OAUTH_CLIENT_ID,
		response_type: "code",
		redirect_uri: redirectUri,
		scope: CLAUDE_OAUTH_SCOPE,
		code_challenge: codeChallenge,
		code_challenge_method: "S256",
		state,
	});
	return `${CLAUDE_OAUTH_AUTHORIZE_URL}?${params.toString()}`;
}

const claudeAuthFlow: ProviderAuthFlow = {
	provider: "claude-code",
	async authenticate(_dataDir: string, signal?: AbortSignal): Promise<ProviderAuthCredential> {
		const codeVerifier = base64Url(crypto.randomBytes(32));
		const codeChallenge = base64Url(crypto.createHash("sha256").update(codeVerifier).digest());
		const state = crypto.randomBytes(32).toString("hex");
		let server: Server | null = null;

		return new Promise<ProviderAuthCredential>((resolve, reject) => {
			let port = 0;

			const timeout = setTimeout(() => {
				server?.close();
				cleanup();
				reject(new Error("Claude sign-in timed out after 5 minutes."));
			}, 5 * 60 * 1000);

			const cleanup = () => {
				clearTimeout(timeout);
				signal?.removeEventListener("abort", onAbort);
			};
			const onAbort = () => {
				server?.close();
				cleanup();
				reject(new Error("Login was cancelled."));
			};
			if (signal?.aborted) return onAbort();
			signal?.addEventListener("abort", onAbort, { once: true });

			const failCallback = (res: import("node:http").ServerResponse, err: Error) => {
				cleanup();
				server?.close();
				res.writeHead(400, { "Content-Type": "text/html; charset=utf-8" });
				res.end(CLAUDE_CALLBACK_HTML("Sign-in failed", "Return to Orchestrator and try signing in again."));
				reject(err);
			};

			server = createServer((req, res) => {
				const url = new URL(req.url ?? "/", "http://127.0.0.1");
				if (url.pathname !== "/callback") {
					res.writeHead(404, { "Content-Type": "text/plain" });
					res.end("Not found");
					return;
				}
				const errorParam = url.searchParams.get("error");
				if (errorParam) {
					failCallback(res, new Error(url.searchParams.get("error_description") || `Claude sign-in failed: ${errorParam}`));
					return;
				}
				const code = url.searchParams.get("code");
				const returnedState = url.searchParams.get("state");
				if (!code || returnedState !== state) {
					failCallback(res, new Error("Claude sign-in callback is invalid."));
					return;
				}
				const redirectUri = `http://localhost:${port}/callback`;
				void (async () => {
					try {
						const tokenRes = await fetch(CLAUDE_OAUTH_TOKEN_URL, {
							method: "POST",
							redirect: "error",
							headers: { "Content-Type": "application/json", Accept: "application/json" },
							body: JSON.stringify({
								grant_type: "authorization_code",
								code,
								redirect_uri: redirectUri,
								client_id: CLAUDE_OAUTH_CLIENT_ID,
								code_verifier: codeVerifier,
								state,
								expires_in: CLAUDE_OAUTH_TOKEN_TTL_SECONDS,
							}),
						});
						if (!tokenRes.ok) throw new Error(`Claude token exchange failed (HTTP ${tokenRes.status}).`);
						const tokenBody = (await tokenRes.json()) as Record<string, unknown>;
						const accessToken = tokenBody.access_token;
						if (typeof accessToken !== "string" || !extractClaudeOAuthToken(accessToken)) {
							throw new Error("Claude did not return a valid setup token.");
						}
						cleanup();
						server?.close();
						res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
						res.end(
							CLAUDE_CALLBACK_HTML(
								"Signed in to Orchestrator",
								"You can close this tab and return to Orchestrator.",
							),
						);
						resolve({ provider: "claude-code", credentialType: "oauth_token", secret: accessToken });
					} catch (err) {
						failCallback(res, err instanceof Error ? err : new Error(String(err)));
					}
				})();
			});

			server.listen(0, "127.0.0.1", () => {
				const addr = server!.address();
				if (typeof addr === "string" || addr === null) {
					cleanup();
					server?.close();
					reject(new Error("Failed to start local callback server."));
					return;
				}
				port = addr.port;
				void shell.openExternal(buildClaudeAuthorizeUrl(`http://localhost:${port}/callback`, codeChallenge, state));
			});

			server.on("error", (err) => {
				cleanup();
				server?.close();
				reject(err);
			});
		});
	},
};

const GITHUB_OAUTH_SCOPES = "repo read:org repo_hook";

const GITHUB_CALLBACK_HTML = (title: string, body: string): string =>
	`<!doctype html><meta charset="utf-8"><title>${title}</title>` +
	`<body style="font:15px -apple-system,system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#111">` +
	`<h1 style="font-size:1.25rem">${title}</h1><p style="color:#555">${body}</p></body>`;

const githubAuthFlow: ProviderAuthFlow = {
	provider: "github",
	async authenticate(_dataDir: string, signal?: AbortSignal): Promise<ProviderAuthCredential> {
		const clientId = process.env.AO_GITHUB_OAUTH_CLIENT_ID?.trim();
		if (!clientId) {
			throw new Error(
				"GitHub OAuth is not configured. Set AO_GITHUB_OAUTH_CLIENT_ID in your environment, or use a Personal Access Token instead.",
			);
		}
		const state = crypto.randomBytes(16).toString("hex");
		let server: Server | null = null;

		return new Promise<ProviderAuthCredential>((resolve, reject) => {
			const timeout = setTimeout(() => {
				server?.close();
				reject(new Error("GitHub sign-in timed out after 5 minutes."));
			}, 5 * 60 * 1000);

			const cleanup = () => {
				clearTimeout(timeout);
				signal?.removeEventListener("abort", onAbort);
			};

			const onAbort = () => {
				server?.close();
				cleanup();
				reject(new Error("GitHub sign-in was cancelled."));
			};
			if (signal?.aborted) return onAbort();
			signal?.addEventListener("abort", onAbort, { once: true });

			server = createServer((req, res) => {
				const url = new URL(req.url ?? "/", "http://127.0.0.1");
				if (url.pathname !== "/callback") {
					res.writeHead(404, { "Content-Type": "text/plain" });
					res.end("Not found");
					return;
				}
				const errorParam = url.searchParams.get("error");
				if (errorParam) {
					cleanup();
					server?.close();
					reject(new Error(url.searchParams.get("error_description") || `GitHub sign-in failed: ${errorParam}`));
					res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
					res.end(GITHUB_CALLBACK_HTML("Sign-in failed", "Return to Orchestrator and try signing in again."));
					return;
				}

				const code = url.searchParams.get("code");
				const returnedState = url.searchParams.get("state");
				if (!code || returnedState !== state) {
					cleanup();
					server?.close();
					reject(new Error("GitHub sign-in callback is invalid."));
					res.writeHead(400, { "Content-Type": "text/html; charset=utf-8" });
					res.end(GITHUB_CALLBACK_HTML("Sign-in failed", "Return to Orchestrator and try signing in again."));
					return;
				}

				void (async () => {
					try {
						const clientSecret = process.env.AO_GITHUB_OAUTH_CLIENT_SECRET?.trim();
						if (!clientSecret) throw new Error("GitHub OAuth client secret is not configured.");

						const tokenRes = await fetch("https://github.com/login/oauth/access_token", {
							method: "POST",
							headers: {
								Accept: "application/json",
								"Content-Type": "application/json",
								"User-Agent": "Agent-Orchestrator",
							},
							body: JSON.stringify({
								client_id: clientId,
								client_secret: clientSecret,
								code,
							}),
						});
						if (!tokenRes.ok) throw new Error(`GitHub token exchange failed (HTTP ${tokenRes.status}).`);
						const tokenBody = (await tokenRes.json()) as Record<string, unknown>;
						if (typeof tokenBody.error === "string") {
							throw new Error((tokenBody.error_description as string) || `GitHub OAuth error: ${tokenBody.error}`);
						}
						const accessToken = tokenBody.access_token;
						if (typeof accessToken !== "string" || !accessToken) {
							throw new Error("GitHub did not return an access token.");
						}
						const refreshToken = typeof tokenBody.refresh_token === "string" ? tokenBody.refresh_token : undefined;
						const expiresIn = typeof tokenBody.expires_in === "number" ? tokenBody.expires_in : undefined;
						const refreshTokenExpiresIn =
							typeof tokenBody.refresh_token_expires_in === "number" ? tokenBody.refresh_token_expires_in : undefined;

						const userRes = await fetch("https://api.github.com/user", {
							headers: { Authorization: `Bearer ${accessToken}`, "User-Agent": "Agent-Orchestrator" },
						});
						if (!userRes.ok) throw new Error("GitHub token verification failed.");
						const user = (await userRes.json()) as { login?: string };
						const login = user.login || "unknown";

						cleanup();
						server?.close();
						res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
						res.end(GITHUB_CALLBACK_HTML(
							"Signed in to Orchestrator",
							`Authenticated as <strong>${login}</strong>. You can close this tab and return to Orchestrator.`,
						));
						resolve({ provider: "github", credentialType: "access_token", secret: accessToken, refreshToken, expiresIn, refreshTokenExpiresIn });
					} catch (err) {
						cleanup();
						server?.close();
						reject(err instanceof Error ? err : new Error(String(err)));
						res.writeHead(400, { "Content-Type": "text/html; charset=utf-8" });
						res.end(GITHUB_CALLBACK_HTML("Sign-in failed", "Return to Orchestrator and try signing in again."));
					}
				})();
			});

			server.listen(0, "127.0.0.1", () => {
				const addr = server!.address();
				if (typeof addr === "string" || addr === null) {
					cleanup();
					server?.close();
					reject(new Error("Failed to start local callback server."));
					return;
				}
				const port = addr.port;
				const redirectUri = `http://127.0.0.1:${port}/callback`;
				const authUrl =
					`https://github.com/login/oauth/authorize` +
					`?client_id=${encodeURIComponent(clientId)}` +
					`&redirect_uri=${encodeURIComponent(redirectUri)}` +
					`&scope=${encodeURIComponent(GITHUB_OAUTH_SCOPES)}` +
					`&state=${encodeURIComponent(state)}` +
					`&prompt=consent`;
				void shell.openExternal(authUrl);
			});

			server.on("error", (err) => {
				cleanup();
				server?.close();
				reject(err);
			});
		});
	},
};

const flows = new Map<string, ProviderAuthFlow>([
	[codexAuthFlow.provider, codexAuthFlow],
	[claudeAuthFlow.provider, claudeAuthFlow],
	[githubAuthFlow.provider, githubAuthFlow],
]);

export function providerAuthFlow(provider: string): ProviderAuthFlow {
	const flow = flows.get(provider);
	if (!flow) throw new Error(`No browser authentication flow is available for ${provider}.`);
	return flow;
}
