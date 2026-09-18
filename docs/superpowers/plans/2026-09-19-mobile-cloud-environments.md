# Mobile Cloud Environments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `packages/mobile` a second environment, so the user picks Local (today's paired daemon) or Cloud (the control plane in `cloud/`) and sees that environment's projects, sessions, and chat.

**Architecture:** Screens and `lib/store.tsx` stop calling daemon functions directly and depend on a `SessionSource` port. `local.ts` implements it over today's `ServerConfig` functions with no behavior change; `cloud.ts` implements it over `@aoagents/cloud-client` with a real bearer token from `expo-secure-store`. Desktop's IPC proxy layer has no mobile counterpart and is not ported. Cloud liveness is cursor polling, never SSE.

**Tech Stack:** Expo SDK 57, React Native 0.86, TypeScript, vitest, `@aoagents/cloud-client`, `expo-auth-session`, `expo-secure-store`, AsyncStorage.

**Spec:** `docs/superpowers/specs/2026-09-19-mobile-cloud-environments-design.md`

## Global Constraints

- **Never use `streamEvents`** from `@aoagents/cloud-client`. It calls `response.body.getReader()` and `TextDecoder` (`packages/cloud-client/src/client.ts:460`), which React Native does not support reliably; `expo/fetch` streaming also hits a JNI global-reference ceiling. Use cursor polling of `/chat-events?after=` instead.
- **Bearer tokens never touch AsyncStorage.** They live in `expo-secure-store` only, matching the rule `lib/config.ts` already applies to the daemon connection password.
- **The local path must not change behavior.** Every existing test in `packages/mobile` stays green, unmodified, through Tasks 1–3.
- **Cloud API prefix** is `/api/cloud/v1`; org-scoped paths are `/api/cloud/v1/orgs/{orgId}/...`.
- All work happens in `packages/mobile` unless a task says otherwise. Commands run from `packages/mobile`: `npm run typecheck`, `npm test`.
- Tests are vitest, colocated as `<name>.test.ts` next to the module. Mock native modules at the top of the file before importing the module under test (see `lib/chat/eventCursor.test.ts` for the established pattern).
- Use tab indentation, matching the surrounding files.

---

### Task 1: The `SessionSource` port

**Files:**
- Create: `lib/environment/types.ts`
- Create: `lib/environment/fake.ts`
- Test: `lib/environment/fake.test.ts`

**Interfaces:**
- Consumes: `ProjectInfo`, `DashboardSession`, `SpawnOptions` from `lib/api.ts`; `ConversationPage`, `SendMessageInput`, `SendMessageResult` from `lib/chat/api.ts`; `ConversationEvent` from `lib/chat/sse.ts`.
- Produces: `SessionSource`, `EnvironmentKind`, `createFakeSessionSource(overrides?)`.

- [ ] **Step 1: Write the failing test**

```ts
// lib/environment/fake.test.ts
import { describe, expect, it } from "vitest";
import { createFakeSessionSource } from "./fake";

describe("createFakeSessionSource", () => {
	it("answers with empty collections by default", async () => {
		const source = createFakeSessionSource();
		expect(await source.listProjects()).toEqual([]);
		expect(await source.listSessions()).toEqual([]);
	});

	it("lets a test override one method and keep the rest", async () => {
		const source = createFakeSessionSource({
			listProjects: async () => [{ id: "p1", name: "web" }],
		});
		expect(await source.listProjects()).toEqual([{ id: "p1", name: "web" }]);
		expect(await source.listSessions()).toEqual([]);
	});

	it("reports its environment kind", () => {
		expect(createFakeSessionSource().kind).toBe("local");
		expect(createFakeSessionSource({ kind: "cloud" }).kind).toBe("cloud");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/environment/fake.test.ts`
Expected: FAIL — cannot resolve `./fake`.

- [ ] **Step 3: Write the interface**

```ts
// lib/environment/types.ts
import type { DashboardSession, ProjectInfo, SpawnOptions } from "../api";
import type { ConversationPage, SendMessageInput, SendMessageResult } from "../chat/api";
import type { ConversationEvent } from "../chat/sse";

/** Which environment a source speaks for. */
export type EnvironmentKind = "local" | "cloud";

/**
 * The data operations every environment implements.
 *
 * Screens and the store depend on this rather than on daemon functions, so a
 * second environment does not mean threading a second config type through
 * every call site. Deliberately scoped to pass 1: terminal, workspace files,
 * and PRs are not here yet.
 */
export interface SessionSource {
	readonly kind: EnvironmentKind;
	listProjects(): Promise<ProjectInfo[]>;
	listSessions(): Promise<DashboardSession[]>;
	createSession(options: SpawnOptions): Promise<{ id: string }>;
	deleteSession(id: string): Promise<void>;
	getConversationPage(id: string, beforeSequence?: number): Promise<ConversationPage>;
	sendMessage(id: string, input: SendMessageInput): Promise<SendMessageResult>;
	cancelTurn(id: string, turnId: string): Promise<void>;
	subscribeEvents(id: string, listener: (event: ConversationEvent) => void): () => void;
}
```

- [ ] **Step 4: Write the fake**

```ts
// lib/environment/fake.ts
import type { SessionSource } from "./types";

/**
 * A SessionSource that answers with empties unless a test overrides a method.
 * Screen and store tests use this so they exercise both environments without
 * a daemon or a control plane.
 */
export function createFakeSessionSource(overrides: Partial<SessionSource> = {}): SessionSource {
	return {
		kind: "local",
		listProjects: async () => [],
		listSessions: async () => [],
		createSession: async () => ({ id: "fake-session" }),
		deleteSession: async () => {},
		getConversationPage: async () => {
			throw new Error("createFakeSessionSource: getConversationPage not overridden");
		},
		sendMessage: async () => ({ duplicate: false }),
		cancelTurn: async () => {},
		subscribeEvents: () => () => {},
		...overrides,
	};
}
```

- [ ] **Step 5: Run tests and typecheck**

Run: `npx vitest run lib/environment/fake.test.ts && npm run typecheck`
Expected: PASS, no type errors.

- [ ] **Step 6: Commit**

```bash
git add lib/environment/types.ts lib/environment/fake.ts lib/environment/fake.test.ts
git commit -m "feat(mobile): add the SessionSource port and its test fake"
```

---

### Task 2: The local adapter

**Files:**
- Create: `lib/environment/local.ts`
- Test: `lib/environment/local.test.ts`

**Interfaces:**
- Consumes: `SessionSource` from Task 1; `getProjects`, `getSessions`, `delegateTask`, `killSession` from `lib/api.ts`; `getConversationPage`, `sendConversationMessage`, `cancelQueuedConversationTurn` from `lib/chat/api.ts`; `subscribeConversationEvents` from `lib/chat/conversationEvents.ts`.
- Produces: `createLocalSessionSource(cfg: ServerConfig): SessionSource`.

- [ ] **Step 1: Write the failing test**

```ts
// lib/environment/local.test.ts
import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

const getProjects = vi.fn();
const getSessions = vi.fn();
const killSession = vi.fn();
const delegateTask = vi.fn();
vi.mock("../api", async (importOriginal) => ({
	...(await importOriginal<typeof import("../api")>()),
	getProjects: (...args: unknown[]) => getProjects(...args),
	getSessions: (...args: unknown[]) => getSessions(...args),
	killSession: (...args: unknown[]) => killSession(...args),
	delegateTask: (...args: unknown[]) => delegateTask(...args),
}));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { createLocalSessionSource } from "./local";

const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };

describe("createLocalSessionSource", () => {
	it("identifies as the local environment", () => {
		expect(createLocalSessionSource(cfg).kind).toBe("local");
	});

	it("forwards listProjects to the daemon with the active config", async () => {
		getProjects.mockResolvedValue([{ id: "p1", name: "web" }]);
		const result = await createLocalSessionSource(cfg).listProjects();
		expect(getProjects).toHaveBeenCalledWith(cfg);
		expect(result).toEqual([{ id: "p1", name: "web" }]);
	});

	// getSessions returns the daemon's full board payload; the port only
	// promises the session list, so the adapter unwraps it here rather than
	// widening the interface for one environment's extra fields.
	it("unwraps the daemon's sessions response", async () => {
		getSessions.mockResolvedValue({
			sessions: [{ id: "s1" }], orchestrators: [], orchestratorId: null, stats: {}, projects: [],
		});
		expect(await createLocalSessionSource(cfg).listSessions()).toEqual([{ id: "s1" }]);
	});

	it("spawns through delegateTask and returns the new session id", async () => {
		delegateTask.mockResolvedValue({ id: "s9" });
		const result = await createLocalSessionSource(cfg).createSession({
			projectId: "p1", prompt: "fix the build", mode: "chat",
		});
		expect(result).toEqual({ id: "s9" });
		expect(delegateTask).toHaveBeenCalledWith(cfg, expect.objectContaining({
			projectId: "p1", brief: "fix the build", mode: "chat",
		}));
	});

	it("deletes a session by killing it", async () => {
		await createLocalSessionSource(cfg).deleteSession("s1");
		expect(killSession).toHaveBeenCalledWith(cfg, "s1");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/environment/local.test.ts`
Expected: FAIL — cannot resolve `./local`.

- [ ] **Step 3: Write the adapter**

```ts
// lib/environment/local.ts
import { delegateTask, getProjects, getSessions, killSession } from "../api";
import {
	cancelQueuedConversationTurn,
	getConversationPage,
	sendConversationMessage,
} from "../chat/api";
import { subscribeConversationEvents } from "../chat/conversationEvents";
import type { ServerConfig } from "../config";
import type { SessionSource } from "./types";

/**
 * The paired-daemon environment, expressed as a SessionSource.
 *
 * A pure adapter: every method forwards to the function that already
 * implements it, with the active ServerConfig bound. No daemon behavior
 * changes here — this exists so screens can stop importing daemon functions
 * directly.
 */
export function createLocalSessionSource(cfg: ServerConfig): SessionSource {
	return {
		kind: "local",
		listProjects: () => getProjects(cfg),
		listSessions: async () => (await getSessions(cfg)).sessions,
		createSession: async (options) => {
			const session = await delegateTask(cfg, {
				projectId: options.projectId ?? "",
				brief: options.prompt ?? "",
				agent: options.harness,
				model: options.model,
				mode: options.mode ?? "chat",
				attachments: options.attachments,
			});
			return { id: session.id };
		},
		deleteSession: (id) => killSession(cfg, id),
		getConversationPage: (id, beforeSequence) => getConversationPage(cfg, id, beforeSequence),
		sendMessage: (id, input) => sendConversationMessage(cfg, id, input),
		cancelTurn: (id, turnId) => cancelQueuedConversationTurn(cfg, id, turnId),
		subscribeEvents: (id, listener) => subscribeConversationEvents(id, listener),
	};
}
```

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/environment/local.test.ts && npm run typecheck`
Expected: PASS, no type errors.

- [ ] **Step 5: Run the whole suite to prove nothing regressed**

Run: `npm test`
Expected: PASS — the same set of tests that passed before Task 1.

- [ ] **Step 6: Commit**

```bash
git add lib/environment/local.ts lib/environment/local.test.ts
git commit -m "feat(mobile): express the paired daemon as a SessionSource"
```

---

### Task 3: Consume the port from the store

**Files:**
- Modify: `lib/store.tsx`
- Test: `lib/environment/store-wiring.test.ts` (create)

**Interfaces:**
- Consumes: `createLocalSessionSource` from Task 2.
- Produces: `useSessionSource(): SessionSource` exported from `lib/store.tsx`.

This task is a refactor with no user-visible change. The store keeps every existing behavior — polling, racing, project retention — and only changes *how* it reaches its data.

- [ ] **Step 1: Write the failing test**

```ts
// lib/environment/store-wiring.test.ts
import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { sessionSourceForConfig } from "./resolve";

describe("sessionSourceForConfig", () => {
	it("returns a local source for a configured daemon", () => {
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		expect(sessionSourceForConfig(cfg)?.kind).toBe("local");
	});

	it("returns undefined when no daemon is configured", () => {
		expect(sessionSourceForConfig(null)).toBeUndefined();
	});

	// The store rebuilds on every render; a new source object per render would
	// retrigger every effect keyed on it.
	it("returns the same instance for an unchanged config", () => {
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		expect(sessionSourceForConfig(cfg)).toBe(sessionSourceForConfig(cfg));
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/environment/store-wiring.test.ts`
Expected: FAIL — cannot resolve `./resolve`.

- [ ] **Step 3: Write the resolver**

```ts
// lib/environment/resolve.ts
import { isConfigured, sameServerConfigKey, type ServerConfig } from "../config";
import { createLocalSessionSource } from "./local";
import type { SessionSource } from "./types";

// Memoised on the config's identity so the store hands the same instance to
// every consumer between config changes.
let cachedKey: string | undefined;
let cachedSource: SessionSource | undefined;

/** The source for the currently configured daemon, or undefined when unpaired. */
export function sessionSourceForConfig(cfg: ServerConfig | null): SessionSource | undefined {
	if (!cfg || !isConfigured(cfg)) {
		cachedKey = undefined;
		cachedSource = undefined;
		return undefined;
	}
	const key = sameServerConfigKey(cfg);
	if (key !== cachedKey || cachedSource === undefined) {
		cachedKey = key;
		cachedSource = createLocalSessionSource(cfg);
	}
	return cachedSource;
}
```

If `sameServerConfigKey` does not exist in `lib/config.ts`, add it there as
`export function sameServerConfigKey(cfg: ServerConfig): string { return `${normalizeServerHost(cfg.host)}:${cfg.httpPort}:${cfg.secure ? "s" : ""}`; }`
and export it — `lib/sameConfig.ts` compares configs but does not produce a key.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run lib/environment/store-wiring.test.ts`
Expected: PASS.

- [ ] **Step 5: Expose it from the store**

In `lib/store.tsx`, add to the provider's value and to the exported hooks:

```tsx
import { sessionSourceForConfig } from "./environment/resolve";
import type { SessionSource } from "./environment/types";

// inside the provider, alongside the existing derived values:
const sessionSource = useMemo(() => sessionSourceForConfig(config), [config]);

// add `sessionSource` to the AppState type as `sessionSource: SessionSource | undefined;`
// and to the context value object.

/** The active environment's data source. Undefined until one is configured. */
export function useSessionSource(): SessionSource | undefined {
	return useContext(AppContext).sessionSource;
}
```

Do **not** rewrite the store's existing polling to go through the source in this task. The source is exposed and unused; Task 10 switches readers over once a cloud source exists to switch to.

- [ ] **Step 6: Run the whole suite and typecheck**

Run: `npm test && npm run typecheck`
Expected: PASS — no existing test changed.

- [ ] **Step 7: Commit**

```bash
git add lib/environment/resolve.ts lib/environment/store-wiring.test.ts lib/store.tsx lib/config.ts
git commit -m "feat(mobile): expose the active environment's source from the store"
```

---

### Task 4: Prove `@aoagents/cloud-client` resolves on device

**Files:**
- Modify: `packages/mobile/package.json`
- Modify: `packages/mobile/metro.config.js`
- Create: `lib/cloud/smoke.ts`

**Interfaces:**
- Produces: a working import of `createCloudClient` inside the Expo bundle.

This is a risk-first task. Metro resolution of a workspace TypeScript package has broken this app before (the single-React pin in `metro.config.js`). Prove it now, not after five tasks are built on it.

- [ ] **Step 1: Add the dependency**

```bash
cd packages/mobile
npm install --save ../cloud-client
```

Per this repo's rule, install per-package from `packages/mobile` — the root has no workspaces.

- [ ] **Step 2: Teach Metro to follow the package source**

In `metro.config.js`, keep the existing single-React pin untouched and add the sibling package to `watchFolders`:

```js
const path = require("node:path");

// The cloud client is consumed from source in the monorepo, so Metro has to
// watch a folder outside the app root. The React pin below must stay as-is.
config.watchFolders = [...(config.watchFolders ?? []), path.resolve(__dirname, "../cloud-client")];
```

- [ ] **Step 3: Write the smoke module**

```ts
// lib/cloud/smoke.ts
import { createCloudClient } from "@aoagents/cloud-client";

/**
 * Proves the cloud client resolves and constructs inside the Expo bundle.
 * Called once from the dev build; carries no credentials and makes no request.
 */
export function cloudClientResolves(): boolean {
	const client = createCloudClient({
		baseUrl: "https://example.invalid",
		getToken: async () => "unused",
	});
	return typeof client.getCurrentAccount === "function";
}
```

- [ ] **Step 4: Verify on a device or simulator**

Run: `npx expo run:ios` (or `run:android`), then from the dev build call `cloudClientResolves()` — temporarily log it from `app/_layout.tsx`'s mount effect.
Expected: logs `true`, with no red box and no "Unable to resolve module" error.

If `@aoagents/cloud-client` fails to resolve because it ships `dist` rather than source, build it first: `npm --prefix ../cloud-client run build`, and record in the commit message that the mobile build depends on that artifact.

- [ ] **Step 5: Remove the temporary log and commit**

```bash
git add packages/mobile/package.json packages/mobile/package-lock.json packages/mobile/metro.config.js packages/mobile/lib/cloud/smoke.ts
git commit -m "chore(mobile): consume the cloud client from the Expo bundle"
```

---

### Task 5: Bring the contract up to the server

**Files:**
- Modify: `contracts/cloud/openapi.yaml`
- Modify: `packages/cloud-client/src/schema.ts` (generated)
- Modify: `packages/cloud-client/src/client.ts`
- Test: `packages/cloud-client/test/client.test.ts`

**Interfaces:**
- Produces: `CloudClient.wakeSessions(orgId, options)`, `CloudClient.resumeSession(orgId, sessionId, options)`, `CloudClient.restoreSession(orgId, sessionId, options)`.

The server has these routes (`cloud/internal/httpapi/server.go`); the contract does not describe them, so the generated client cannot call them. Task 13 needs `resume`.

- [ ] **Step 1: Write the failing client test**

```ts
// packages/cloud-client/test/client.test.ts — add to the existing file
it("resumes a paused session", async () => {
	const calls: Array<{ url: string; method: string }> = [];
	const client = createCloudClient({
		baseUrl: "https://cp.test",
		getToken: async () => "t",
		fetch: async (url, init) => {
			calls.push({ url: String(url), method: init?.method ?? "GET" });
			return new Response(null, { status: 204 });
		},
	});
	await client.resumeSession("org1", "sess1");
	expect(calls[0]).toEqual({
		url: "https://cp.test/api/cloud/v1/orgs/org1/sessions/sess1/resume",
		method: "POST",
	});
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd packages/cloud-client && npx vitest run test/client.test.ts`
Expected: FAIL — `client.resumeSession is not a function`.

- [ ] **Step 3: Describe the routes in the contract**

Add to `contracts/cloud/openapi.yaml`, matching the style of the neighbouring session paths:

```yaml
  /api/cloud/v1/orgs/{orgId}/sessions/wake:
    post:
      operationId: wakePausedSessions
      summary: Wake every paused sandbox in the organization.
      parameters:
        - $ref: "#/components/parameters/OrgId"
      responses:
        "204": { description: Wake requested. }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /api/cloud/v1/orgs/{orgId}/sessions/{sessionId}/resume:
    post:
      operationId: resumeSession
      summary: Resume one paused session's sandbox.
      parameters:
        - $ref: "#/components/parameters/OrgId"
        - $ref: "#/components/parameters/SessionId"
      responses:
        "204": { description: Resume requested. }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "404": { $ref: "#/components/responses/NotFound" }
  /api/cloud/v1/orgs/{orgId}/sessions/{sessionId}/restore:
    post:
      operationId: restoreSession
      summary: Restore the agent inside a running sandbox.
      parameters:
        - $ref: "#/components/parameters/OrgId"
        - $ref: "#/components/parameters/SessionId"
      responses:
        "204": { description: Restore requested. }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "404": { $ref: "#/components/responses/NotFound" }
```

Confirm the parameter and response component names against the existing file before committing — reuse whatever `/sessions/{sessionId}` already references rather than inventing new ones. Confirm each response status against the handlers in `cloud/internal/httpapi/session_restore_handlers.go` and `resource_handlers.go`.

- [ ] **Step 4: Regenerate and add the methods**

```bash
cd packages/cloud-client && npm run generate
```

Then add to `client.ts`, next to `deleteSession`:

```ts
	wakeSessions(orgId: string, options: RequestOptions = {}): Promise<void> {
		return this.voidRequest("POST", this.orgPath(orgId, "/sessions/wake"), options);
	}

	resumeSession(orgId: string, sessionId: string, options: RequestOptions = {}): Promise<void> {
		return this.voidRequest(
			"POST",
			this.orgPath(orgId, `/sessions/${encodeURIComponent(sessionId)}/resume`),
			options,
		);
	}

	restoreSession(orgId: string, sessionId: string, options: RequestOptions = {}): Promise<void> {
		return this.voidRequest(
			"POST",
			this.orgPath(orgId, `/sessions/${encodeURIComponent(sessionId)}/restore`),
			options,
		);
	}
```

Use whatever private helper `deleteSession` already uses for a no-content request; if none exists, follow `deleteSession`'s body exactly and discard the response.

- [ ] **Step 5: Run the tests**

Run: `cd packages/cloud-client && npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit the spec and generated types together**

```bash
git add contracts/cloud/openapi.yaml packages/cloud-client/src/schema.ts packages/cloud-client/src/client.ts packages/cloud-client/test/client.test.ts
git commit -m "feat(cloud-client): describe session wake, resume, and restore"
```

---

### Task 6: Cloud token storage

**Files:**
- Create: `lib/cloud/tokens.ts`
- Test: `lib/cloud/tokens.test.ts`

**Interfaces:**
- Produces: `CloudTokens`, `readTokens()`, `writeTokens(t)`, `clearTokens()`, `isExpired(t, now)`.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/tokens.test.ts
import { describe, expect, it, vi, beforeEach } from "vitest";

const store = new Map<string, string>();
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (k: string) => store.get(k) ?? null),
	setItemAsync: vi.fn(async (k: string, v: string) => { store.set(k, v); }),
	deleteItemAsync: vi.fn(async (k: string) => { store.delete(k); }),
}));

import { clearTokens, isExpired, readTokens, writeTokens } from "./tokens";

beforeEach(() => store.clear());

describe("cloud tokens", () => {
	it("round-trips through the device keystore", async () => {
		await writeTokens({ accessToken: "a", refreshToken: "r", expiresAt: 1000 });
		expect(await readTokens()).toEqual({ accessToken: "a", refreshToken: "r", expiresAt: 1000 });
	});

	it("returns null when nothing is stored", async () => {
		expect(await readTokens()).toBeNull();
	});

	it("returns null rather than throwing on unreadable contents", async () => {
		store.set("ao.cloud.tokens", "{not json");
		expect(await readTokens()).toBeNull();
	});

	it("clears every key it wrote", async () => {
		await writeTokens({ accessToken: "a", expiresAt: 1000 });
		await clearTokens();
		expect(await readTokens()).toBeNull();
	});

	// A token that expires during the request is worse than one refreshed a
	// little early, so expiry is judged against a skew window.
	it("treats a token inside the skew window as expired", () => {
		expect(isExpired({ accessToken: "a", expiresAt: 1_000_000 }, 1_000_000 - 10_000)).toBe(true);
		expect(isExpired({ accessToken: "a", expiresAt: 1_000_000 }, 1_000_000 - 120_000)).toBe(false);
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/tokens.test.ts`
Expected: FAIL — cannot resolve `./tokens`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/tokens.ts
import * as SecureStore from "expo-secure-store";

/** Refresh this long before nominal expiry so a token cannot die mid-request. */
export const EXPIRY_SKEW_MS = 60_000;

const KEY = "ao.cloud.tokens";

export type CloudTokens = {
	accessToken: string;
	/** WorkOS sessions rotate a refresh token; local opaque-token sessions have none. */
	refreshToken?: string;
	/** Epoch milliseconds. */
	expiresAt: number;
};

/**
 * Cloud bearer tokens live only in the device keystore, never in AsyncStorage —
 * the same rule lib/config.ts applies to the daemon connection password.
 */
export async function readTokens(): Promise<CloudTokens | null> {
	try {
		const raw = await SecureStore.getItemAsync(KEY);
		if (!raw) return null;
		const parsed = JSON.parse(raw) as Partial<CloudTokens>;
		if (typeof parsed.accessToken !== "string" || typeof parsed.expiresAt !== "number") return null;
		return {
			accessToken: parsed.accessToken,
			refreshToken: typeof parsed.refreshToken === "string" ? parsed.refreshToken : undefined,
			expiresAt: parsed.expiresAt,
		};
	} catch {
		return null;
	}
}

export async function writeTokens(tokens: CloudTokens): Promise<void> {
	await SecureStore.setItemAsync(KEY, JSON.stringify(tokens));
}

export async function clearTokens(): Promise<void> {
	await SecureStore.deleteItemAsync(KEY);
}

export function isExpired(tokens: CloudTokens, now: number): boolean {
	return tokens.expiresAt - EXPIRY_SKEW_MS <= now;
}
```

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/tokens.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/cloud/tokens.ts lib/cloud/tokens.test.ts
git commit -m "feat(mobile): keep cloud tokens in the device keystore"
```

---

### Task 7: The token provider with single-flight refresh

**Files:**
- Create: `lib/cloud/session.ts`
- Test: `lib/cloud/session.test.ts`

**Interfaces:**
- Consumes: `CloudTokens`, `isExpired` from Task 6.
- Produces: `createTokenProvider({ read, write, clear, refresh, now })` returning `{ getToken(): Promise<string | null>, signOut(): Promise<void> }`.

Ported from `frontend/src/main/cloud-auth.ts`: concurrent callers must share one refresh, and sign-out must invalidate refreshes already in flight.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/session.test.ts
import { describe, expect, it, vi } from "vitest";
import { createTokenProvider } from "./session";
import type { CloudTokens } from "./tokens";

const fresh: CloudTokens = { accessToken: "fresh", refreshToken: "r", expiresAt: 10_000_000 };
const stale: CloudTokens = { accessToken: "stale", refreshToken: "r", expiresAt: 0 };

function harness(initial: CloudTokens | null, refresh: () => Promise<CloudTokens>) {
	let stored = initial;
	const refreshSpy = vi.fn(refresh);
	const provider = createTokenProvider({
		read: async () => stored,
		write: async (t) => { stored = t; },
		clear: async () => { stored = null; },
		refresh: refreshSpy,
		now: () => 0,
	});
	return { provider, refreshSpy, stored: () => stored };
}

describe("createTokenProvider", () => {
	it("returns a live token without refreshing", async () => {
		const { provider, refreshSpy } = harness(fresh, async () => fresh);
		expect(await provider.getToken()).toBe("fresh");
		expect(refreshSpy).not.toHaveBeenCalled();
	});

	it("returns null when signed out", async () => {
		const { provider } = harness(null, async () => fresh);
		expect(await provider.getToken()).toBeNull();
	});

	it("refreshes an expired token and stores the result", async () => {
		const { provider, stored } = harness(stale, async () => fresh);
		expect(await provider.getToken()).toBe("fresh");
		expect(stored()).toEqual(fresh);
	});

	// Every screen asks for a token at once on resume. One refresh, not eight.
	it("shares one refresh between concurrent callers", async () => {
		const { provider, refreshSpy } = harness(stale, async () => fresh);
		const results = await Promise.all([provider.getToken(), provider.getToken(), provider.getToken()]);
		expect(results).toEqual(["fresh", "fresh", "fresh"]);
		expect(refreshSpy).toHaveBeenCalledTimes(1);
	});

	it("signs out and reports null when the refresh is rejected", async () => {
		const { provider, stored } = harness(stale, async () => { throw new Error("invalid_grant"); });
		expect(await provider.getToken()).toBeNull();
		expect(stored()).toBeNull();
	});

	// A refresh that lands after sign-out must not resurrect the session.
	it("discards a refresh that completes after sign-out", async () => {
		let release: (t: CloudTokens) => void = () => {};
		const { provider, stored } = harness(stale, () => new Promise((r) => { release = r; }));
		const pending = provider.getToken();
		await provider.signOut();
		release(fresh);
		expect(await pending).toBeNull();
		expect(stored()).toBeNull();
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/session.test.ts`
Expected: FAIL — cannot resolve `./session`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/session.ts
import { isExpired, type CloudTokens } from "./tokens";

export type TokenProviderDeps = {
	read(): Promise<CloudTokens | null>;
	write(tokens: CloudTokens): Promise<void>;
	clear(): Promise<void>;
	refresh(refreshToken: string): Promise<CloudTokens>;
	now(): number;
};

export interface TokenProvider {
	/** The bearer token to send, or null when there is no usable session. */
	getToken(): Promise<string | null>;
	signOut(): Promise<void>;
}

/**
 * Refresh-aware access to the stored cloud session.
 *
 * Mirrors frontend/src/main/cloud-auth.ts: concurrent callers share one
 * refresh, and a generation counter means a refresh that resolves after
 * sign-out is discarded rather than resurrecting the session.
 */
export function createTokenProvider(deps: TokenProviderDeps): TokenProvider {
	let inFlight: Promise<CloudTokens | null> | null = null;
	let generation = 0;

	async function refreshOnce(tokens: CloudTokens, startedAt: number): Promise<CloudTokens | null> {
		if (!tokens.refreshToken) {
			await deps.clear();
			return null;
		}
		try {
			const next = await deps.refresh(tokens.refreshToken);
			// Sign-out happened while this was in flight; drop the result.
			if (startedAt !== generation) return null;
			await deps.write(next);
			return next;
		} catch {
			if (startedAt === generation) await deps.clear();
			return null;
		}
	}

	return {
		async getToken() {
			const tokens = await deps.read();
			if (!tokens) return null;
			if (!isExpired(tokens, deps.now())) return tokens.accessToken;
			if (inFlight === null) {
				const startedAt = generation;
				inFlight = refreshOnce(tokens, startedAt).finally(() => { inFlight = null; });
			}
			return (await inFlight)?.accessToken ?? null;
		},
		async signOut() {
			generation += 1;
			inFlight = null;
			await deps.clear();
		},
	};
}
```

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/session.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/cloud/session.ts lib/cloud/session.test.ts
git commit -m "feat(mobile): refresh-aware cloud token provider"
```

---

### Task 8: Sign-in

**Files:**
- Create: `lib/cloud/signIn.ts`
- Create: `lib/cloud/config.ts`
- Test: `lib/cloud/signIn.test.ts`

**Interfaces:**
- Consumes: `CloudTokens` from Task 6.
- Produces: `signInWithLocalAuth(baseUrl, email, password)`, `registerWithLocalAuth(...)`, `buildWorkOSAuthUrl(...)`, `exchangeWorkOSCode(...)`, `CLOUD_BASE_URL`.

Local email/password comes first: it is how this work is developed against `npm run cloud:local`, and it needs no WorkOS account. WorkOS PKCE follows in the same module.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/signIn.test.ts
import { describe, expect, it, vi } from "vitest";
import { buildWorkOSAuthUrl, signInWithLocalAuth } from "./signIn";

describe("signInWithLocalAuth", () => {
	it("posts credentials and maps the response to stored tokens", async () => {
		const fetchImpl = vi.fn(async () => new Response(
			JSON.stringify({ accessToken: "tok", expiresIn: 3600 }),
			{ status: 200, headers: { "content-type": "application/json" } },
		));
		const tokens = await signInWithLocalAuth(
			"http://127.0.0.1:8081", "dev@example.com", "correct-horse-battery",
			{ fetchImpl, now: () => 1_000_000 },
		);
		expect(fetchImpl).toHaveBeenCalledWith(
			"http://127.0.0.1:8081/api/cloud/v1/auth/local/login",
			expect.objectContaining({ method: "POST" }),
		);
		expect(tokens).toEqual({ accessToken: "tok", expiresAt: 1_000_000 + 3_600_000 });
	});

	it("throws a readable error when the control plane rejects the credentials", async () => {
		const fetchImpl = async () => new Response(
			JSON.stringify({ message: "Invalid email or password." }),
			{ status: 401, headers: { "content-type": "application/json" } },
		);
		await expect(
			signInWithLocalAuth("http://127.0.0.1:8081", "a@b.c", "nope", { fetchImpl, now: () => 0 }),
		).rejects.toThrow("Invalid email or password.");
	});
});

describe("buildWorkOSAuthUrl", () => {
	it("builds a PKCE authorization URL", () => {
		const url = new URL(buildWorkOSAuthUrl({
			clientId: "client_123",
			redirectUri: "aomobile://callback",
			codeChallenge: "chal",
			state: "st",
		}));
		expect(url.origin + url.pathname).toBe("https://api.workos.com/user_management/authorize");
		expect(url.searchParams.get("client_id")).toBe("client_123");
		expect(url.searchParams.get("redirect_uri")).toBe("aomobile://callback");
		expect(url.searchParams.get("code_challenge")).toBe("chal");
		expect(url.searchParams.get("code_challenge_method")).toBe("S256");
		expect(url.searchParams.get("response_type")).toBe("code");
		expect(url.searchParams.get("state")).toBe("st");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/signIn.test.ts`
Expected: FAIL — cannot resolve `./signIn`.

- [ ] **Step 3: Write the config module**

```ts
// lib/cloud/config.ts
/**
 * The AuthKit client id is public configuration — it appears in every sign-in
 * URL — so a baked default keeps sign-in working with no build-time setup,
 * exactly as the desktop app does (frontend/src/main/cloud-auth.ts).
 */
export const WORKOS_CLIENT_ID =
	process.env.EXPO_PUBLIC_WORKOS_CLIENT_ID?.trim() || "client_01KZ3VRKC374HS91XGRDPT3671";

/** Control plane the app signs into. Overridable for local Docker development. */
export const CLOUD_BASE_URL =
	process.env.EXPO_PUBLIC_AO_CLOUD_URL?.trim() || "https://staging-api.aoagents.dev";

export const CLOUD_API_PREFIX = "/api/cloud/v1";
export const WORKOS_REDIRECT_URI = "aomobile://callback";
```

- [ ] **Step 4: Implement sign-in**

```ts
// lib/cloud/signIn.ts
import { CLOUD_API_PREFIX } from "./config";
import type { CloudTokens } from "./tokens";

type Deps = { fetchImpl?: typeof fetch; now?: () => number };

async function errorMessage(response: Response, fallback: string): Promise<string> {
	try {
		const body = (await response.json()) as { message?: unknown };
		if (typeof body?.message === "string" && body.message !== "") return body.message;
	} catch {
		// Non-JSON body: keep the fallback.
	}
	return fallback;
}

async function localAuth(
	path: "login" | "register",
	baseUrl: string,
	email: string,
	password: string,
	{ fetchImpl = fetch, now = Date.now }: Deps,
): Promise<CloudTokens> {
	const response = await fetchImpl(`${baseUrl}${CLOUD_API_PREFIX}/auth/local/${path}`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ email, password }),
	});
	if (!response.ok) {
		throw new Error(await errorMessage(response, `Sign-in failed (${response.status}).`));
	}
	const body = (await response.json()) as { accessToken: string; expiresIn: number };
	return { accessToken: body.accessToken, expiresAt: now() + body.expiresIn * 1000 };
}

/** Dev-only email/password sign-in against a loopback control plane. */
export function signInWithLocalAuth(
	baseUrl: string, email: string, password: string, deps: Deps = {},
): Promise<CloudTokens> {
	return localAuth("login", baseUrl, email, password, deps);
}

export function registerWithLocalAuth(
	baseUrl: string, email: string, password: string, deps: Deps = {},
): Promise<CloudTokens> {
	return localAuth("register", baseUrl, email, password, deps);
}

export function buildWorkOSAuthUrl(input: {
	clientId: string; redirectUri: string; codeChallenge: string; state: string;
}): string {
	const url = new URL("https://api.workos.com/user_management/authorize");
	url.searchParams.set("client_id", input.clientId);
	url.searchParams.set("redirect_uri", input.redirectUri);
	url.searchParams.set("response_type", "code");
	url.searchParams.set("provider", "authkit");
	url.searchParams.set("code_challenge", input.codeChallenge);
	url.searchParams.set("code_challenge_method", "S256");
	url.searchParams.set("state", input.state);
	return url.toString();
}

/** Exchanges a WorkOS authorization code for tokens. PKCE: no client secret. */
export async function exchangeWorkOSCode(
	input: { clientId: string; code: string; codeVerifier: string },
	{ fetchImpl = fetch, now = Date.now }: Deps = {},
): Promise<CloudTokens> {
	const response = await fetchImpl("https://api.workos.com/user_management/authenticate", {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({
			client_id: input.clientId,
			grant_type: "authorization_code",
			code: input.code,
			code_verifier: input.codeVerifier,
		}),
	});
	if (!response.ok) {
		throw new Error(await errorMessage(response, `Sign-in failed (${response.status}).`));
	}
	const body = (await response.json()) as {
		access_token: string; refresh_token?: string; expires_in?: number;
	};
	return {
		accessToken: body.access_token,
		refreshToken: body.refresh_token,
		expiresAt: now() + (body.expires_in ?? 3600) * 1000,
	};
}
```

- [ ] **Step 5: Run tests and typecheck**

Run: `npx vitest run lib/cloud/signIn.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Verify against the real local control plane**

Start the stack from the repo root: `npm run cloud:local`. From the dev build, call `registerWithLocalAuth("http://<mac-lan-ip>:8081", …)` once and confirm a token comes back. Use the Mac's LAN address, not `127.0.0.1` — that is the phone's own loopback.

- [ ] **Step 7: Commit**

```bash
git add lib/cloud/config.ts lib/cloud/signIn.ts lib/cloud/signIn.test.ts
git commit -m "feat(mobile): cloud sign-in for local auth and WorkOS PKCE"
```

---

### Task 9: Map cloud DTOs onto mobile's board types

**Files:**
- Create: `lib/cloud/mapping.ts`
- Test: `lib/cloud/mapping.test.ts`

**Interfaces:**
- Consumes: `Project`, `Session` types from `@aoagents/cloud-client`.
- Produces: `toProjectInfo(project)`, `toDashboardSession(session)`, `kanbanColumnForStatus(status)`.

The cloud `Session` (`contracts/cloud/openapi.yaml`) carries `status`, `activityState`, `runtimeConnected`, `isTerminated` but **no** `kanbanColumn` — the daemon derives that server-side and cloud does not. Mobile derives it here so the board groups cloud sessions the same way.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/mapping.test.ts
import { describe, expect, it } from "vitest";
import type { Project, Session } from "@aoagents/cloud-client";
import { kanbanColumnForStatus, toDashboardSession, toProjectInfo } from "./mapping";

const project: Project = {
	id: "p1", orgId: "o1", displayName: "web",
	repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
	config: {}, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
};

const session: Session = {
	id: "s1", orgId: "o1", projectId: "p1", kind: "worker", harness: "claude-code",
	displayName: "fix the build", branch: "ao/fix-build", mode: "trusted",
	deniedCommands: [], activityState: "active", status: "working",
	runtimeConnected: true, isTerminated: false,
	createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-02T00:00:00Z",
};

describe("toProjectInfo", () => {
	it("names the project by its display name", () => {
		expect(toProjectInfo(project)).toEqual({ id: "p1", name: "web", kind: "single_repo" });
	});
});

describe("toDashboardSession", () => {
	it("carries the fields the board reads", () => {
		const mapped = toDashboardSession(session);
		expect(mapped.id).toBe("s1");
		expect(mapped.projectId).toBe("p1");
		expect(mapped.harness).toBe("claude-code");
		expect(mapped.displayName).toBe("fix the build");
		expect(mapped.branch).toBe("ao/fix-build");
		expect(mapped.status).toBe("working");
		expect(mapped.createdAt).toBe("2026-09-01T00:00:00Z");
		expect(mapped.lastActivityAt).toBe("2026-09-02T00:00:00Z");
	});

	// Mobile's mode vocabulary is the controller (chat/tui), not cloud's trust
	// level (read-only/standard/trusted). Cloud sessions are always Chat.
	it("reports Chat regardless of the cloud trust mode", () => {
		expect(toDashboardSession(session).mode).toBe("chat");
		expect(toDashboardSession({ ...session, mode: "read-only" }).mode).toBe("chat");
	});

	// Cloud has no terminal mux handle; leaving it set would offer a terminal
	// this pass cannot open.
	it("leaves the terminal handle unset", () => {
		expect(toDashboardSession(session).terminalHandleId).toBeUndefined();
	});

	it("derives the board column cloud does not send", () => {
		expect(toDashboardSession(session).kanbanColumn).toBe("building");
	});
});

describe("kanbanColumnForStatus", () => {
	it("places each cloud status on the board", () => {
		expect(kanbanColumnForStatus("working")).toBe("building");
		expect(kanbanColumnForStatus("needs_input")).toBe("building");
		expect(kanbanColumnForStatus("idle")).toBe("building");
		expect(kanbanColumnForStatus("ci_failed")).toBe("validating");
		expect(kanbanColumnForStatus("changes_requested")).toBe("validating");
		expect(kanbanColumnForStatus("pr_open")).toBe("needs_review");
		expect(kanbanColumnForStatus("draft")).toBe("needs_review");
		expect(kanbanColumnForStatus("review_pending")).toBe("needs_review");
		expect(kanbanColumnForStatus("approved")).toBe("ready");
		expect(kanbanColumnForStatus("mergeable")).toBe("ready");
		expect(kanbanColumnForStatus("merged")).toBe("archive");
		expect(kanbanColumnForStatus("exited")).toBe("archive");
		expect(kanbanColumnForStatus("terminated")).toBe("archive");
	});

	it("falls back to building for an unknown status", () => {
		expect(kanbanColumnForStatus("something_new")).toBe("building");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/mapping.test.ts`
Expected: FAIL — cannot resolve `./mapping`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/mapping.ts
import type { Project, Session } from "@aoagents/cloud-client";
import type { DashboardSession, KanbanColumn, ProjectInfo } from "../api";

/**
 * Where a cloud session sits on mobile's board.
 *
 * The daemon derives kanbanColumn server-side and sends it; the cloud contract
 * has no such field, so mobile derives it from the status enum
 * (contracts/cloud/openapi.yaml, SessionStatus) to keep both environments
 * grouping the same way.
 */
export function kanbanColumnForStatus(status: string): KanbanColumn {
	switch (status) {
		case "ci_failed":
		case "changes_requested":
			return "validating";
		case "pr_open":
		case "draft":
		case "review_pending":
			return "needs_review";
		case "approved":
		case "mergeable":
			return "ready";
		case "merged":
		case "exited":
		case "terminated":
			return "archive";
		default:
			return "building";
	}
}

export function toProjectInfo(project: Project): ProjectInfo {
	return { id: project.id, name: project.displayName, kind: "single_repo" };
}

export function toDashboardSession(session: Session): DashboardSession {
	return {
		id: session.id,
		projectId: session.projectId,
		status: session.status,
		kanbanColumn: kanbanColumnForStatus(session.status),
		displayStatus: null,
		// Derived from facts mobile does not have for cloud yet; the board's own
		// fallback is better than a wrong colour.
		attentionLevel: null,
		activity: session.activityState,
		harness: session.harness,
		// Cloud's `mode` is a trust level (read-only/standard/trusted), not
		// mobile's controller (chat/tui). Cloud sessions are always Chat.
		mode: "chat",
		branch: session.branch || null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		displayName: session.displayName,
		summary: null,
		createdAt: session.createdAt,
		lastActivityAt: session.updatedAt,
		isTerminated: session.isTerminated,
	};
}
```

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/mapping.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/cloud/mapping.ts lib/cloud/mapping.test.ts
git commit -m "feat(mobile): map cloud projects and sessions onto the board"
```

---

### Task 10: The cloud source's read paths

**Files:**
- Create: `lib/cloud/client.ts`
- Create: `lib/cloud/source.ts`
- Test: `lib/cloud/source.test.ts`

**Interfaces:**
- Consumes: `SessionSource` (Task 1), `TokenProvider` (Task 7), `toProjectInfo`/`toDashboardSession` (Task 9).
- Produces: `createCloudSessionSource({ client, orgId })`, `createMobileCloudClient({ baseUrl, tokens })`.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/source.test.ts
import { describe, expect, it, vi } from "vitest";
import { createCloudSessionSource } from "./source";

function clientStub(overrides: Record<string, unknown> = {}) {
	return {
		listProjects: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		listSessions: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		createSession: vi.fn(async () => ({ id: "s-new" })),
		deleteSession: vi.fn(async () => {}),
		...overrides,
	};
}

describe("createCloudSessionSource", () => {
	it("identifies as the cloud environment", () => {
		expect(createCloudSessionSource({ client: clientStub() as never, orgId: "o1" }).kind).toBe("cloud");
	});

	it("lists projects for the bound org and maps them", async () => {
		const client = clientStub({
			listProjects: vi.fn(async () => ({
				items: [{
					id: "p1", orgId: "o1", displayName: "web",
					repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
					config: {}, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
				}],
				page: { hasMore: false },
			})),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.listProjects()).toEqual([{ id: "p1", name: "web", kind: "single_repo" }]);
		expect(client.listProjects).toHaveBeenCalledWith("o1", expect.anything());
	});

	// A phone must not stop at page one and silently hide half the board.
	it("follows pagination cursors to the end", async () => {
		const pages = [
			{ items: [{ id: "a" }], page: { hasMore: true, nextCursor: "c2" } },
			{ items: [{ id: "b" }], page: { hasMore: false } },
		];
		let call = 0;
		const client = clientStub({ listSessions: vi.fn(async () => pages[call++]) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const sessions = await source.listSessions();
		expect(sessions.map((s) => s.id)).toEqual(["a", "b"]);
		expect(client.listSessions).toHaveBeenCalledTimes(2);
	});

	it("spawns a worker session in the bound org", async () => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.createSession({ projectId: "p1", prompt: "fix it", harness: "codex" }))
			.toEqual({ id: "s-new" });
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({
			projectId: "p1", kind: "worker", harness: "codex", prompt: "fix it",
		}), expect.objectContaining({ idempotencyKey: expect.any(String) }));
	});
});
```

The pagination test maps raw `{ id }` objects; make `toDashboardSession` tolerate the partial shapes by asserting only on `id` in that case, or give the stub full sessions. Prefer full sessions if the mapper rejects partials.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/source.test.ts`
Expected: FAIL — cannot resolve `./source`.

- [ ] **Step 3: Write the client binding**

```ts
// lib/cloud/client.ts
import { createCloudClient, type CloudClient } from "@aoagents/cloud-client";
import type { TokenProvider } from "./session";

/**
 * The cloud client, bound to the device's token provider.
 *
 * Unlike the desktop renderer there is no proxy and no placeholder token:
 * React Native has no CORS restriction and no privileged main process, so the
 * real bearer goes on the request here.
 */
export function createMobileCloudClient(input: {
	baseUrl: string;
	tokens: TokenProvider;
}): CloudClient {
	return createCloudClient({
		baseUrl: input.baseUrl,
		getToken: async () => (await input.tokens.getToken()) ?? "",
	});
}
```

- [ ] **Step 4: Implement the source**

```ts
// lib/cloud/source.ts
import type { CloudClient } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo } from "../api";
import type { SessionSource } from "../environment/types";
import { toDashboardSession, toProjectInfo } from "./mapping";

/** Page through a cursor-paginated cloud list until it ends. */
async function collect<T>(
	fetchPage: (cursor?: string) => Promise<{ items: T[]; page: { hasMore?: boolean; nextCursor?: string } }>,
): Promise<T[]> {
	const all: T[] = [];
	let cursor: string | undefined;
	for (;;) {
		const page = await fetchPage(cursor);
		all.push(...page.items);
		if (page.page?.hasMore !== true || !page.page.nextCursor) return all;
		cursor = page.page.nextCursor;
	}
}

export function createCloudSessionSource(input: {
	client: CloudClient;
	orgId: string;
}): SessionSource {
	const { client, orgId } = input;
	return {
		kind: "cloud",
		listProjects: async (): Promise<ProjectInfo[]> =>
			(await collect((cursor) => client.listProjects(orgId, { cursor }))).map(toProjectInfo),
		listSessions: async (): Promise<DashboardSession[]> =>
			(await collect((cursor) => client.listSessions(orgId, { cursor }))).map(toDashboardSession),
		createSession: async (options) => {
			const session = await client.createSession(
				orgId,
				{
					projectId: options.projectId ?? "",
					kind: "worker",
					harness: options.harness ?? "claude-code",
					displayName: (options.prompt ?? "New session").slice(0, 80),
					prompt: options.prompt ?? "",
				},
				// Idempotent: a retry on a flaky phone network must not spawn twice.
				{ idempotencyKey: `spawn-${Date.now()}-${Math.random().toString(36).slice(2)}` },
			);
			return { id: session.id };
		},
		deleteSession: async (id) => { await client.deleteSession(orgId, id); },
		getConversationPage: async () => {
			throw new Error("Cloud conversation pages arrive in Task 12.");
		},
		sendMessage: async () => {
			throw new Error("Cloud message sending arrives in Task 13.");
		},
		cancelTurn: async () => {
			throw new Error("Cloud turn cancellation arrives in Task 13.");
		},
		subscribeEvents: () => () => {},
	};
}
```

Confirm `listProjects`/`listSessions`/`createSession` signatures against `packages/cloud-client/src/client.ts` before writing; adjust the option objects to match exactly.

- [ ] **Step 5: Run tests and typecheck**

Run: `npx vitest run lib/cloud/source.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add lib/cloud/client.ts lib/cloud/source.ts lib/cloud/source.test.ts
git commit -m "feat(mobile): read cloud projects and sessions through the port"
```

---

### Task 11: Org resolution

**Files:**
- Create: `lib/cloud/org.ts`
- Test: `lib/cloud/org.test.ts`

**Interfaces:**
- Produces: `resolveOrg(client)`, `orgDisplayNameForAccount(user)`.

Copies `frontend/src/renderer/hooks/useCloudOrg.ts`: first org from `/me`, else create one. No org switcher in this pass.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/org.test.ts
import { describe, expect, it, vi } from "vitest";
import { orgDisplayNameForAccount, resolveOrg } from "./org";

describe("orgDisplayNameForAccount", () => {
	it("prefers the display name", () => {
		expect(orgDisplayNameForAccount({ displayName: "Ada L", email: "ada@x.com" })).toBe("Ada L");
	});

	it("falls back to the email local part", () => {
		expect(orgDisplayNameForAccount({ displayName: "", email: "ada@x.com" })).toBe("ada");
	});

	it("falls back again when there is nothing to name it after", () => {
		expect(orgDisplayNameForAccount({ displayName: "", email: "" })).toBe("Workspace");
	});

	// The control plane caps the name at 80 characters.
	it("truncates to the control plane's limit", () => {
		expect(orgDisplayNameForAccount({ displayName: "x".repeat(200), email: "" })).toHaveLength(80);
	});
});

describe("resolveOrg", () => {
	it("uses the first organization when one exists", async () => {
		const client = {
			getCurrentAccount: vi.fn(async () => ({
				user: { id: "u1", displayName: "Ada", email: "ada@x.com" },
				organizations: [{ id: "o1", slug: "ada", displayName: "Ada", role: "owner" }],
			})),
			createOrganization: vi.fn(),
		};
		expect((await resolveOrg(client as never)).id).toBe("o1");
		expect(client.createOrganization).not.toHaveBeenCalled();
	});

	it("creates one for an account with no organizations", async () => {
		const client = {
			getCurrentAccount: vi.fn(async () => ({
				user: { id: "u1", displayName: "Ada", email: "ada@x.com" },
				organizations: [],
			})),
			createOrganization: vi.fn(async () => ({
				organization: { id: "o-new", slug: "ada", displayName: "Ada", role: "owner" },
			})),
		};
		expect((await resolveOrg(client as never)).id).toBe("o-new");
		expect(client.createOrganization).toHaveBeenCalledWith({ displayName: "Ada" });
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/org.test.ts`
Expected: FAIL — cannot resolve `./org`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/org.ts
import type { CloudClient, OrganizationMembership } from "@aoagents/cloud-client";

/** The control plane caps an organization's display name at 80 characters. */
const MAX_ORG_NAME = 80;

export function orgDisplayNameForAccount(user: { displayName: string; email: string }): string {
	const name = user.displayName.trim() || user.email.split("@")[0]?.trim() || "";
	return (name === "" ? "Workspace" : name).slice(0, MAX_ORG_NAME);
}

/**
 * The organization every cloud call is scoped to: the first from /me, or a
 * freshly created one. First-org is the same deliberate v0 simplification the
 * desktop makes (useCloudOrg.ts) — there is no org switcher yet.
 */
export async function resolveOrg(client: CloudClient): Promise<OrganizationMembership> {
	const account = await client.getCurrentAccount();
	const first = account.organizations[0];
	if (first !== undefined) return first;
	const created = await client.createOrganization({
		displayName: orgDisplayNameForAccount(account.user),
	});
	return created.organization;
}
```

If `createOrganization` is absent from `CloudClient`, add it in `packages/cloud-client` following Task 5's pattern — the route is `POST /api/cloud/v1/orgs` (`cloud/internal/httpapi/server.go:328`) — and commit the contract and generated types together.

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/org.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/cloud/org.ts lib/cloud/org.test.ts
git commit -m "feat(mobile): resolve the cloud organization for every call"
```

---

### Task 12: Cloud chat by cursor polling

**Files:**
- Create: `lib/cloud/events.ts`
- Modify: `lib/cloud/source.ts`
- Test: `lib/cloud/events.test.ts`

**Interfaces:**
- Consumes: `ClientEvent` from `@aoagents/cloud-client`.
- Produces: `toConversationItems(events)`, `pollCloudEvents({ client, orgId, sessionId, after, onEvents, signal, intervalMs })`.

Never `streamEvents` (see Global Constraints). The control plane's `chat-events` replay is cursor-based, which is the same shape `lib/chat/eventCursor.ts` already handles for the daemon.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/events.test.ts
import { describe, expect, it, vi } from "vitest";
import type { ClientEvent } from "@aoagents/cloud-client";
import { toConversationItems } from "./events";

const userEvent: ClientEvent = {
	sessionId: "s1", sequence: 1, type: "chat.user_message",
	payload: { text: "hello" }, createdAt: "2026-09-01T00:00:00Z",
} as ClientEvent;

const deltaOne: ClientEvent = {
	sessionId: "s1", sequence: 2, type: "chat.assistant_delta",
	payload: { text: "Hi " }, createdAt: "2026-09-01T00:00:01Z",
} as ClientEvent;

const deltaTwo: ClientEvent = {
	sessionId: "s1", sequence: 3, type: "chat.assistant_delta",
	payload: { text: "there" }, createdAt: "2026-09-01T00:00:02Z",
} as ClientEvent;

describe("toConversationItems", () => {
	it("maps a user message", () => {
		const [item] = toConversationItems([userEvent]);
		expect(item).toMatchObject({
			kind: "message", role: "user", origin: "human", text: "hello", sequence: 1, streaming: false,
		});
	});

	// Deltas are fragments of one reply; rendering each as its own bubble is
	// the classic streaming-chat bug.
	it("coalesces consecutive assistant deltas into one message", () => {
		const items = toConversationItems([userEvent, deltaOne, deltaTwo]);
		expect(items).toHaveLength(2);
		expect(items[1]).toMatchObject({ role: "assistant", text: "Hi there", streaming: true });
	});

	it("marks the assistant message settled once its turn completes", () => {
		const completed = {
			sessionId: "s1", sequence: 4, type: "chat.turn_completed",
			payload: {}, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, completed]);
		expect(items[0]).toMatchObject({ role: "assistant", streaming: false });
	});

	it("ignores event types it has no rendering for", () => {
		const interrupt = {
			sessionId: "s1", sequence: 5, type: "chat.interrupt_requested",
			payload: {}, createdAt: "2026-09-01T00:00:04Z",
		} as ClientEvent;
		expect(toConversationItems([interrupt])).toEqual([]);
	});
});
```

Confirm each `type` literal against `contracts/cloud/openapi.yaml` (`UserMessageEvent.type` is `chat.user_message`) before writing the implementation; correct the test's literals if they differ.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/events.test.ts`
Expected: FAIL — cannot resolve `./events`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/events.ts
import type { ClientEvent, CloudClient } from "@aoagents/cloud-client";
import type { ConversationItem, ConversationMessage } from "../chat/types";

/**
 * Cloud transcript events as mobile's conversation items.
 *
 * Assistant deltas are fragments of one reply, so consecutive deltas coalesce
 * into a single message that stays `streaming` until its turn completes.
 */
export function toConversationItems(events: ClientEvent[]): ConversationItem[] {
	const items: ConversationItem[] = [];
	let open: ConversationMessage | undefined;

	for (const event of events) {
		const payload = event.payload as { text?: string };
		switch (event.type) {
			case "chat.user_message":
				open = undefined;
				items.push({
					kind: "message", id: `${event.sessionId}:${event.sequence}`,
					sequence: event.sequence, revision: 0, role: "user", origin: "human",
					text: payload.text ?? "", streaming: false, createdAt: event.createdAt,
				});
				break;
			case "chat.assistant_delta":
				if (open) {
					open.text += payload.text ?? "";
					break;
				}
				open = {
					kind: "message", id: `${event.sessionId}:${event.sequence}`,
					sequence: event.sequence, revision: 0, role: "assistant", origin: "provider",
					text: payload.text ?? "", streaming: true, createdAt: event.createdAt,
				};
				items.push(open);
				break;
			case "chat.turn_completed":
			case "chat.turn_interrupted":
			case "chat.turn_aborted":
				if (open) open.streaming = false;
				open = undefined;
				break;
			default:
				// turn_started, interrupt_requested: no rendering of their own.
				break;
		}
	}
	return items;
}

export type PollOptions = {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	after: number;
	onEvents(events: ClientEvent[], cursor: number): void;
	signal: AbortSignal;
	intervalMs?: number;
};

/**
 * Polls the cloud transcript from a cursor until aborted.
 *
 * Polling rather than SSE is deliberate: React Native has no EventSource and
 * expo/fetch streaming aborts on long-lived responses. The control plane's
 * replay is cursor-based, so this loses nothing but latency.
 */
export async function pollCloudEvents(options: PollOptions): Promise<void> {
	const interval = options.intervalMs ?? 2000;
	let cursor = options.after;
	while (!options.signal.aborted) {
		try {
			const page = await options.client.replayEvents(options.orgId, options.sessionId, {
				after: cursor, signal: options.signal,
			});
			if (page.items.length > 0) {
				cursor = page.items[page.items.length - 1].sequence;
				options.onEvents(page.items, cursor);
			}
		} catch (error) {
			if (options.signal.aborted) return;
			// A transient failure must not kill the loop; the next tick retries.
		}
		await new Promise((resolve) => setTimeout(resolve, interval));
	}
}
```

Confirm `replayEvents`'s signature and its page shape against `packages/cloud-client/src/client.ts:414` before writing.

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/events.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Wire it into the source**

Replace `getConversationPage` and `subscribeEvents` in `lib/cloud/source.ts`. `getConversationPage` builds a `ConversationSnapshot` from a replay: `conversationId` and `sessionId` are the session id, `harness` from the session, `mode: "chat"`, `controller: { state: "ready" }`, `latestSequence`/`oldestSequence` from the returned events, `hasMoreBefore: false`, `turns: []`, `items` from `toConversationItems`, `settings: {}`. `subscribeEvents` starts `pollCloudEvents` with an `AbortController` and returns a disposer that aborts it.

- [ ] **Step 6: Run the full suite and typecheck**

Run: `npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/cloud/events.ts lib/cloud/events.test.ts lib/cloud/source.ts
git commit -m "feat(mobile): read cloud transcripts by polling the event cursor"
```

---

### Task 13: Sending and cancelling on cloud

**Files:**
- Modify: `lib/cloud/source.ts`
- Test: `lib/cloud/source.test.ts` (extend)

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/source.test.ts — add
it("sends a message with the client's id as the idempotency key", async () => {
	const sendMessage = vi.fn(async () => ({ turnId: "t1" }));
	const source = createCloudSessionSource({
		client: clientStub({ sendMessage }) as never, orgId: "o1",
	});
	const result = await source.sendMessage("s1", { text: "hi", clientMessageId: "cm-1" });
	expect(sendMessage).toHaveBeenCalledWith(
		"o1", "s1", { text: "hi" }, expect.objectContaining({ idempotencyKey: "cm-1" }),
	);
	expect(result).toEqual({ turnId: "t1", duplicate: false });
});

it("cancels a turn", async () => {
	const cancelTurn = vi.fn(async () => {});
	const source = createCloudSessionSource({
		client: clientStub({ cancelTurn }) as never, orgId: "o1",
	});
	await source.cancelTurn("s1", "t1");
	expect(cancelTurn).toHaveBeenCalledWith("o1", "s1", "t1", expect.anything());
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `npx vitest run lib/cloud/source.test.ts`
Expected: FAIL — the stubs throw "arrives in Task 13".

- [ ] **Step 3: Implement**

```ts
		sendMessage: async (id, input) => {
			// The composer's clientMessageId is already a per-message unique id,
			// so it doubles as the idempotency key: a retry after a dropped
			// response cannot post the message twice.
			const turn = await client.sendMessage(
				orgId, id, { text: input.text }, { idempotencyKey: input.clientMessageId },
			);
			return { turnId: turn.turnId, duplicate: false };
		},
		cancelTurn: async (id, turnId) => { await client.cancelTurn(orgId, id, turnId, {}); },
```

Confirm `sendMessage`'s return shape against `packages/cloud-client/src/client.ts:375`; map whatever it returns onto `SendMessageResult`.

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/source.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Verify against the local control plane**

With `npm run cloud:local` running and the dev build signed in, spawn a session, send a message, and watch a reply arrive. Confirm the composer's optimistic message is not duplicated when the reply lands.

- [ ] **Step 6: Commit**

```bash
git add lib/cloud/source.ts lib/cloud/source.test.ts
git commit -m "feat(mobile): send and cancel turns on cloud sessions"
```

---

### Task 14: Pause and resume

**Files:**
- Create: `lib/cloud/lifecycle.ts`
- Test: `lib/cloud/lifecycle.test.ts`

**Interfaces:**
- Produces: `cloudLifecycleStage(session)`, `isResumable(stage)`, `stageLabel(stage)`.

Ported from `frontend/src/renderer/lib/cloud-lifecycle.ts`, which is pure. `cloud/internal/idlepause` pauses sandboxes after quiet time, so on a phone this is the most visible cloud behavior.

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/lifecycle.test.ts
import { describe, expect, it } from "vitest";
import { cloudLifecycleStage, isResumable, stageLabel } from "./lifecycle";

const base = { sandboxProvider: "docker", desiredState: "running", observedState: "running" };

describe("cloudLifecycleStage", () => {
	it("is undefined when the session carries no lifecycle", () => {
		expect(cloudLifecycleStage({})).toBeUndefined();
	});

	it("reports a coder-paused sandbox", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, sandboxProvider: "coder", desiredState: "paused", observedState: "stopped" },
		})).toBe("paused_by_coder");
	});

	it("reports a sandbox coming back up", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, desiredState: "running", observedState: "stopped" },
		})).toBe("resuming_workspace");
	});

	it("waits for the agent while provisioning", () => {
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "provisioning" } }))
			.toBe("waiting_for_coder_agent");
	});

	it("separates starting the worker from restoring the agent", () => {
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: false }))
			.toBe("starting_ao_worker");
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: true }))
			.toBe("restoring_agent");
	});

	it("is connected only once the runtime is attached", () => {
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: true })).toBe("connected");
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: false })).toBe("restoring_agent");
	});
});

describe("isResumable", () => {
	// Only a paused session offers a resume button; the rest are already moving.
	it("is true only for a paused sandbox", () => {
		expect(isResumable("paused_by_coder")).toBe(true);
		expect(isResumable("resuming_workspace")).toBe(false);
		expect(isResumable("connected")).toBe(false);
		expect(isResumable(undefined)).toBe(false);
	});
});

describe("stageLabel", () => {
	it("gives every stage a phrase for the chat banner", () => {
		expect(stageLabel("paused_by_coder")).toBe("Paused — tap to resume");
		expect(stageLabel("resuming_workspace")).toBe("Resuming workspace…");
		expect(stageLabel("waiting_for_coder_agent")).toBe("Starting sandbox…");
		expect(stageLabel("starting_ao_worker")).toBe("Starting worker…");
		expect(stageLabel("restoring_agent")).toBe("Restoring agent…");
		expect(stageLabel("connected")).toBe("Connected");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/lifecycle.test.ts`
Expected: FAIL — cannot resolve `./lifecycle`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/lifecycle.ts

/** Ported verbatim from frontend/src/renderer/lib/cloud-lifecycle.ts. */
export type CloudLifecycleStage =
	| "paused_by_coder"
	| "resuming_workspace"
	| "waiting_for_coder_agent"
	| "starting_ao_worker"
	| "restoring_agent"
	| "connected";

export type CloudLifecycleInput = {
	cloud?: { sandboxProvider?: string; desiredState?: string; observedState?: string };
	runtimeConnected?: boolean;
};

/**
 * The control plane stays authoritative for intent and observation; this only
 * translates those provider-neutral facts into something the phone can say.
 */
export function cloudLifecycleStage(session: CloudLifecycleInput): CloudLifecycleStage | undefined {
	const lifecycle = session.cloud;
	if (!lifecycle) return undefined;
	const { desiredState: desired, observedState: observed } = lifecycle;

	if (lifecycle.sandboxProvider === "coder" && desired === "paused" && observed === "stopped") {
		return "paused_by_coder";
	}
	if (desired === "running" && (observed === "stopped" || observed === "restoring")) {
		return "resuming_workspace";
	}
	if (observed === "requested" || observed === "provisioning") {
		return "waiting_for_coder_agent";
	}
	if (observed === "bootstrapping") {
		return session.runtimeConnected ? "restoring_agent" : "starting_ao_worker";
	}
	if (observed === "running") {
		return session.runtimeConnected ? "connected" : "restoring_agent";
	}
	return undefined;
}

/** Only a paused sandbox offers a resume action; everything else is in motion. */
export function isResumable(stage: CloudLifecycleStage | undefined): boolean {
	return stage === "paused_by_coder";
}

export function stageLabel(stage: CloudLifecycleStage): string {
	switch (stage) {
		case "paused_by_coder": return "Paused — tap to resume";
		case "resuming_workspace": return "Resuming workspace…";
		case "waiting_for_coder_agent": return "Starting sandbox…";
		case "starting_ao_worker": return "Starting worker…";
		case "restoring_agent": return "Restoring agent…";
		case "connected": return "Connected";
	}
}
```

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/lifecycle.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Show it in the chat screen**

In `lib/chat/ChatSessionScreen.tsx`, when the active source's `kind` is `"cloud"`, render `stageLabel(stage)` in the existing conversation banner slot (`lib/chat/conversationBanners.ts`) for any stage other than `connected`, and keep the composer disabled until `connected`. When `isResumable(stage)`, the banner's action calls `client.resumeSession(orgId, sessionId)` (added in Task 5).

- [ ] **Step 6: Run the full suite and typecheck**

Run: `npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/cloud/lifecycle.ts lib/cloud/lifecycle.test.ts lib/chat/ChatSessionScreen.tsx
git commit -m "feat(mobile): show and resume a paused cloud session"
```

---

### Task 15: The environment store and switcher

**Files:**
- Create: `lib/environment/store.tsx`
- Test: `lib/environment/store.test.ts`
- Modify: `lib/environment/resolve.ts`
- Modify: `lib/store.tsx`
- Modify: `lib/onboarding.ts`, `lib/onboarding.test.ts`
- Modify: `app/onboarding.tsx`
- Modify: `lib/sidebar-navigation.ts`

**Interfaces:**
- Consumes: everything above.
- Produces: `useEnvironment()`, `setEnvironment(kind)`, `shouldOnboard({ configured, skipped, cloudSignedIn })`.

- [ ] **Step 1: Write the failing test**

```ts
// lib/environment/store.test.ts
import { describe, expect, it, vi, beforeEach } from "vitest";

const storage = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: vi.fn(async (k: string) => storage.get(k) ?? null),
		setItem: vi.fn(async (k: string, v: string) => { storage.set(k, v); }),
		removeItem: vi.fn(async (k: string) => { storage.delete(k); }),
	},
}));

import { loadEnvironment, saveEnvironment } from "./store";

beforeEach(() => storage.clear());

describe("environment persistence", () => {
	it("defaults to local when nothing has been chosen", async () => {
		expect(await loadEnvironment()).toBe("local");
	});

	it("round-trips the chosen environment", async () => {
		await saveEnvironment("cloud");
		expect(await loadEnvironment()).toBe("cloud");
	});

	// A corrupted value must not strand the user in an environment that does
	// not exist.
	it("falls back to local for an unrecognised stored value", async () => {
		storage.set("ao.environment", "mainframe");
		expect(await loadEnvironment()).toBe("local");
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/environment/store.test.ts`
Expected: FAIL — cannot resolve `./store`.

- [ ] **Step 3: Implement persistence**

```ts
// lib/environment/store.tsx
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { EnvironmentKind } from "./types";

const KEY = "ao.environment";

export async function loadEnvironment(): Promise<EnvironmentKind> {
	try {
		const raw = await AsyncStorage.getItem(KEY);
		return raw === "cloud" ? "cloud" : "local";
	} catch {
		return "local";
	}
}

export async function saveEnvironment(kind: EnvironmentKind): Promise<void> {
	try {
		await AsyncStorage.setItem(KEY, kind);
	} catch {
		// A failed write costs the user one re-selection; it must not crash.
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run lib/environment/store.test.ts`
Expected: PASS.

- [ ] **Step 5: Teach the resolver about cloud**

Extend `sessionSourceForConfig` into `resolveSessionSource({ environment, cfg, cloud })`, returning the local source for `"local"` and `createCloudSessionSource({ client, orgId })` for `"cloud"` when signed in and an org has resolved, or `undefined` otherwise. Keep the memoisation, keyed on environment plus config key plus org id.

- [ ] **Step 6: Extend onboarding**

Add `cloudSignedIn: boolean | null` to `OnboardingInput` and return `false` when it is `true`. Keep the existing rule that `null` means "do nothing yet" — a partially loaded state must never bounce a signed-in user to the welcome screen. Update `lib/onboarding.test.ts` with a case for each combination.

- [ ] **Step 7: Build the switcher UI**

In `app/onboarding.tsx`, offer the two environments before pairing: **Cloud** leads to sign-in, **Local** to today's pairing flow, unchanged. Add an environment row to the drawer (`lib/sidebar-navigation.ts`) that calls `setEnvironment`. Follow `DESIGN.md` — build from the existing primitives rather than new components.

- [ ] **Step 8: Give each environment its own empty state**

When `environment === "local"` and the daemon is unreachable, the board says the Mac is unreachable rather than rendering an empty board. When `environment === "cloud"` and the user is signed out, it offers sign-in. Reuse `lib/connectionError.ts` and `UnpairedState.tsx`.

- [ ] **Step 9: Run the full suite and typecheck**

Run: `npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 10: Verify the whole flow on a device**

With `npm run cloud:local` running: fresh install → choose Cloud → register → board renders → spawn a session → chat → switch to Local → pair → local board renders → switch back to Cloud and confirm the cloud board returns without a re-sign-in.

- [ ] **Step 11: Commit**

```bash
git add lib/environment/ lib/onboarding.ts lib/onboarding.test.ts app/onboarding.tsx lib/sidebar-navigation.ts lib/store.tsx
git commit -m "feat(mobile): choose between the local and cloud environments"
```

---

### Task 16: Block spawn without a coding-agent credential

**Files:**
- Create: `lib/cloud/agentReadiness.ts`
- Test: `lib/cloud/agentReadiness.test.ts`
- Modify: `app/spawn.tsx`

**Interfaces:**
- Consumes: `RedactedProviderConnection` from `@aoagents/cloud-client`.
- Produces: `hasConnectionForHarness(connections, harness)`, `readyHarnesses(connections)`.

Cloud `createSession` returns 422 when the org has no valid provider connection
for the chosen harness. Spawning into that error is the failure the desktop
onboarding spec recorded (`docs/superpowers/specs/2026-09-11-cloud-onboarding-design.md`
§2.4): check the connection **for that specific harness**, never "any valid key".

- [ ] **Step 1: Write the failing test**

```ts
// lib/cloud/agentReadiness.test.ts
import { describe, expect, it } from "vitest";
import { hasConnectionForHarness, readyHarnesses } from "./agentReadiness";

const claude = { provider: "anthropic", agent: "claude-code", validationState: "valid" };
const codexInvalid = { provider: "openai", agent: "codex", validationState: "invalid" };

describe("hasConnectionForHarness", () => {
	it("is true for a harness with its own valid connection", () => {
		expect(hasConnectionForHarness([claude] as never, "claude-code")).toBe(true);
	});

	// The bug this function exists to prevent: a Claude key must not make a
	// Codex spawn look ready, which is what an "any valid key" check does.
	it("is false for a different harness, however many other keys exist", () => {
		expect(hasConnectionForHarness([claude] as never, "codex")).toBe(false);
	});

	it("is false when the harness's own connection failed validation", () => {
		expect(hasConnectionForHarness([codexInvalid] as never, "codex")).toBe(false);
	});

	it("is false with no connections at all", () => {
		expect(hasConnectionForHarness([], "claude-code")).toBe(false);
	});
});

describe("readyHarnesses", () => {
	it("names only the harnesses that can actually run", () => {
		expect(readyHarnesses([claude, codexInvalid] as never)).toEqual(["claude-code"]);
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run lib/cloud/agentReadiness.test.ts`
Expected: FAIL — cannot resolve `./agentReadiness`.

- [ ] **Step 3: Implement**

```ts
// lib/cloud/agentReadiness.ts
import type { RedactedProviderConnection } from "@aoagents/cloud-client";

/**
 * Whether this specific harness can run in the cloud.
 *
 * Deliberately per-harness. The desktop shipped an "any valid key" gate beside
 * a per-harness server check, so a Claude key plus a Codex selection passed the
 * UI and 422'd on create. Mobile does not repeat that.
 */
export function hasConnectionForHarness(
	connections: RedactedProviderConnection[],
	harness: string,
): boolean {
	return connections.some(
		(connection) => connection.agent === harness && connection.validationState === "valid",
	);
}

export function readyHarnesses(connections: RedactedProviderConnection[]): string[] {
	return connections
		.filter((connection) => connection.validationState === "valid")
		.map((connection) => connection.agent);
}
```

Confirm the `agent` and `validationState` field names against
`RedactedProviderConnection` in `packages/cloud-client/src/schema.ts` before
writing; correct both the test and the implementation if they differ.

- [ ] **Step 4: Run tests and typecheck**

Run: `npx vitest run lib/cloud/agentReadiness.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Gate the spawn screen**

In `app/spawn.tsx`, when the environment is cloud, load
`client.listProviderConnections(orgId)` and pass the result through
`readyHarnesses`. Show only ready harnesses in the agent picker. When none are
ready, replace the spawn button with a "Connect a coding agent" CTA explaining
that cloud sessions need a provider key, rather than letting the user submit
into a 422. The local environment's picker is unchanged.

- [ ] **Step 6: Run the full suite and typecheck**

Run: `npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/cloud/agentReadiness.ts lib/cloud/agentReadiness.test.ts app/spawn.tsx
git commit -m "feat(mobile): require a coding-agent credential before a cloud spawn"
```

---

## Verification

Before opening a PR:

```bash
cd packages/mobile && npm run typecheck && npm test
cd ../cloud-client && npm run typecheck && npm test
```

If any control-plane code was touched: `cd cloud && go test ./...`.

Device verification against `npm run cloud:local` per Tasks 8, 13, and 15.
