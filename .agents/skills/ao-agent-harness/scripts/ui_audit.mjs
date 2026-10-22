#!/usr/bin/env node
// Step 4 of the harness workflow: drive the REAL AO desktop app the way a
// user does and save a screenshot at every checkpoint, pass or fail.
//
// Start the dev app with a debugging port (see SKILL.md step 4), then:
//   node scripts/ui_audit.mjs --harness codex --out ~/Desktop/agents/ao-audit-reports/run-ui
//
// Checkpoints: harness listed → install → account/models → auth → update (v0:
// skipped) → New Task agent → New Task models → spawn and run (TUI and/or
// Chat). Writes <out>/NN-<checkpoint>-<STATUS>.png, ui-report.json, ui-report.md.
//
// Selectors are in SELECTORS below; update them there when the UI changes.

import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const SELECTORS = Object.freeze({
  settingsButton: { role: "button", name: "Settings" },
  settingsDialog: '[data-testid="settings-page"]',
  settingsClose: { role: "button", name: "Close settings" },
  navSection: (name) => ({ role: "button", name }),
  harnessSection: '[data-testid="settings-section"][data-section="harness"]',
  harnessSearch: { label: "Search harnesses" },
  harnessRow: (id) => `[data-agent="${id}"]`,
  harnessName: (id) => `#harness-agent-${id}`,
  rowStatus: "p.text-xs",
  installButton: { role: "button", name: "Install" },
  installedChip: { label: "Installed" },
  progress: '[role="status"]',
  loginButton: { role: "button", name: /^(Login|Set up|Refresh login)$/ },
  authTerminal: '[data-testid="harness-auth-terminal"]',
  modelsSection: /models?/i,
  accountSection: /account|subscription|usage|remaining/i,
  standaloneStart: { role: "button", name: /Start a standalone agent|New agent/ },
  agentPicker: { label: "Agent" },
  modelPicker: { label: "Model" },
  menuItem: "[role=menuitem]",
  taskField: { label: "Task" },
  submit: 'button[type="submit"]',
  sessionDetail: '[data-testid="session-detail"]',
  terminal: '[data-testid="session-terminal"]',
  chat: 'section[data-session-mode="chat"]',
  chatMessages: "[data-chat-message-role=assistant]",
  interfaceSetting: { label: /Default session interface/ },
});

const STATUS_TEXT = {
  connected: /^Connected$/i,
  configured: /^Configured$/i,
  signedOut: /^(Signed out|Not set up|Not connected)$/i,
  unknown: /^Unknown$/i,
  available: /^Available via /i,
};

function loadPlaywright(repo) {
  const require = createRequire(join(resolve(repo), "frontend", "package.json"));
  for (const name of ["playwright-core", "playwright", "@playwright/test"]) {
    try { return require(name); } catch { /* try the next package */ }
  }
  throw new Error(`Playwright is not installed in ${resolve(repo)}/frontend. Run \`npm ci\` in that frontend once.`);
}

const locate = (scope, spec) => (typeof spec === "string" ? scope.locator(spec)
  : spec.role ? scope.getByRole(spec.role, { name: spec.name })
  : scope.getByLabel(spec.label));

async function visible(locator, timeout = 5_000) {
  try { await locator.first().waitFor({ state: "visible", timeout }); return true; } catch { return false; }
}

// One checkpoint: run it, then always screenshot, whatever happened.
async function checkpoint(context, id, title, run) {
  const index = String(context.results.length + 1).padStart(2, "0");
  let outcome;
  try { outcome = await run(); } catch (error) { outcome = { status: "FAIL", reason: `error: ${error.message.split("\n")[0]}` }; }
  const screenshot = join(context.out, `${index}-${id}-${outcome.status}.png`);
  try { await context.page.screenshot({ path: screenshot, fullPage: false }); } catch (error) { outcome.screenshotError = error.message; }
  const result = { id, title, ...outcome, screenshot: outcome.screenshotError ? null : screenshot };
  context.results.push(result);
  console.error(`${result.status.padEnd(7)} ${id}: ${result.reason}`);
  return result;
}

async function openHarnessSettings(page, harness) {
  if (!(await visible(page.locator(SELECTORS.settingsDialog), 500))) await locate(page, SELECTORS.settingsButton).first().click();
  await page.locator(SELECTORS.settingsDialog).waitFor({ timeout: 10_000 });
  await locate(page, SELECTORS.navSection("Harness")).first().click();
  const section = page.locator(SELECTORS.harnessSection);
  await section.waitFor({ timeout: 10_000 });
  const search = locate(section, SELECTORS.harnessSearch);
  if (await visible(search, 1_000)) await search.fill(harness);
  return section;
}

async function closeSettings(page) {
  if (await visible(page.locator(SELECTORS.settingsDialog), 500)) {
    await locate(page, SELECTORS.settingsClose).first().click().catch(() => page.keyboard.press("Escape"));
  }
}

async function rowStatus(row) {
  return (await row.locator(SELECTORS.rowStatus).first().textContent().catch(() => "") ?? "").trim();
}

async function setInterface(page, label) {
  await locate(page, SELECTORS.settingsButton).first().click();
  await page.locator(SELECTORS.settingsDialog).waitFor({ timeout: 10_000 });
  await locate(page, SELECTORS.navSection("General")).first().click();
  const control = locate(page, SELECTORS.interfaceSetting).first();
  await control.click();
  const option = page.getByRole("menuitem", { name: label }).or(page.getByRole("option", { name: label })).or(page.getByRole("radio", { name: label }));
  await option.first().click();
  await closeSettings(page);
}

export async function uiAudit({ cdp, repo, harness, label = "", model = "", out, interfaces = ["tui", "chat"], install = false, login = false, spawn = true, prompt }) {
  const { chromium } = loadPlaywright(repo);
  await mkdir(out, { recursive: true });
  let browser;
  try { browser = await chromium.connectOverCDP(cdp); } catch (error) {
    throw new Error(`cannot reach the AO app at ${cdp} (${error.message.split("\n")[0]}). Start it with a debugging port; see SKILL.md step 4.`);
  }
  const page = browser.contexts().flatMap((context) => context.pages()).find((candidate) => !candidate.url().startsWith("devtools://"));
  if (!page) throw new Error("connected, but the AO window has no page");
  await page.setViewportSize?.({ width: 1440, height: 900 }).catch(() => {});
  const context = { page, out, results: [] };
  let row;
  let displayName = label;

  await checkpoint(context, "harness-listed", "Agent Harness page lists the harness", async () => {
    const section = await openHarnessSettings(page, harness);
    row = section.locator(SELECTORS.harnessRow(harness));
    if (!(await visible(row))) return { status: "FAIL", reason: `no row [data-agent="${harness}"] on the Harness page: not registered, or hidden on this host` };
    displayName ||= ((await page.locator(SELECTORS.harnessName(harness)).first().textContent().catch(() => "")) ?? "").trim() || harness;
    return { status: "PASS", reason: `listed as "${displayName}" (${await rowStatus(row) || "no status text"})` };
  });
  if (!row || !(await visible(row, 500))) return finish(browser, context, { harness, displayName });

  await checkpoint(context, "install", "Install from the Harness page", async () => {
    const status = await rowStatus(row);
    const installButton = locate(row, SELECTORS.installButton);
    if (!(await visible(installButton, 1_000))) {
      return STATUS_TEXT.available.test(status)
        ? { status: "FAIL", reason: `shows "${status}" but no Install button` }
        : { status: "PASS", reason: `already installed (${status || "installed"}); the install action was not exercised` };
    }
    if (!install) return { status: "NOT_RUN", reason: "Install is offered; pass --install to click it (installs the CLI on this machine)" };
    await installButton.first().click();
    await row.locator(SELECTORS.progress).first().waitFor({ state: "detached", timeout: 600_000 }).catch(() => {});
    const after = await rowStatus(row);
    return /fail|interrupt|error/i.test(after) ? { status: "FAIL", reason: `install ended with "${after}"` } : { status: "PASS", reason: `installed; row now shows "${after}"` };
  });

  await checkpoint(context, "account-models", "Account health and Models section with listed models", async () => {
    const text = (await row.textContent()) ?? "";
    const hasModels = SELECTORS.modelsSection.test(text);
    const hasAccount = SELECTORS.accountSection.test(text);
    if (!hasModels && !hasAccount) return { status: "FAIL", reason: "this build's Harness row shows no account health or Models section (models only appear in the New Task picker)" };
    return { status: hasModels && hasAccount ? "PASS" : "FAIL", reason: `account section ${hasAccount ? "shown" : "missing"}; models section ${hasModels ? "shown" : "missing"}` };
  });

  await checkpoint(context, "auth", "Login or configure, then Connected", async () => {
    const status = await rowStatus(row);
    if (STATUS_TEXT.connected.test(status)) return { status: "PASS", reason: "Connected" };
    if (STATUS_TEXT.configured.test(status)) return { status: "BLOCKED", reason: "Configured, not verified: AO found credentials but could not verify them" };
    const loginButton = locate(row, SELECTORS.loginButton);
    if (!(await visible(loginButton, 1_000))) return { status: "FAIL", reason: `status "${status || "none"}" and no Login/Set up button` };
    if (!login) return { status: "NEEDS_USER", reason: `status "${status}"; Login/Set up is offered. Log in (or pass --login to open it), then rerun` };
    await loginButton.first().click();
    const opened = await visible(page.locator(SELECTORS.authTerminal), 15_000);
    return { status: "NEEDS_USER", reason: opened ? "login panel opened; finish the login, then rerun" : "clicked Login but no login panel appeared" };
  });

  await checkpoint(context, "update", "Update the harness", async () => ({ status: "NOT_RUN", reason: "skipped in v0" }));
  await closeSettings(page);

  for (const mode of interfaces) {
    if (interfaces.length > 1) {
      await setInterface(page, mode === "chat" ? "Chat" : "Terminal").catch((error) => console.error(`could not set the default interface to ${mode}: ${error.message.split("\n")[0]}`));
    }
    let dialog;
    await checkpoint(context, `new-task-agent-${mode}`, `New Task lists the agent (${mode})`, async () => {
      await page.goto(page.url().replace(/#.*$/, "#/"));
      await locate(page, SELECTORS.standaloneStart).first().click();
      dialog = page.getByRole("dialog");
      await dialog.waitFor({ timeout: 10_000 });
      await locate(dialog, SELECTORS.agentPicker).first().click();
      const option = page.getByRole("menuitem", { name: new RegExp(`^${displayName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`) });
      if (!(await visible(option))) return { status: "FAIL", reason: `"${displayName}" is not in the New Task agent list` };
      await option.first().click();
      return { status: "PASS", reason: `"${displayName}" selectable in New Task` };
    });
    await checkpoint(context, `new-task-models-${mode}`, `New Task lists the harness models (${mode})`, async () => {
      if (!dialog) return { status: "NOT_RUN", reason: "New Task did not open" };
      const picker = locate(dialog, SELECTORS.modelPicker).first();
      if (!(await visible(picker))) return { status: "FAIL", reason: "no Model picker for this agent" };
      await picker.click();
      const items = page.locator(SELECTORS.menuItem);
      await visible(items, 15_000);
      const names = (await items.allTextContents()).map((item) => item.trim()).filter(Boolean);
      if (!names.length) return { status: "FAIL", reason: "Model picker opened with no models" };
      const choice = model ? items.filter({ hasText: model }) : items;
      await choice.first().click();
      return { status: "PASS", reason: `${names.length} model option(s), e.g. ${names.slice(0, 3).join(", ")}` };
    });
    if (!spawn) continue;
    await checkpoint(context, `running-${mode}`, `Spawned agent runs in ${mode === "chat" ? "Chat" : "the terminal (TUI)"}`, async () => {
      if (!dialog) return { status: "NOT_RUN", reason: "New Task did not open" };
      await locate(dialog, SELECTORS.taskField).first().fill(prompt);
      await dialog.locator(SELECTORS.submit).first().click();
      await page.locator(SELECTORS.sessionDetail).first().waitFor({ timeout: 60_000 });
      const surface = page.locator(mode === "chat" ? SELECTORS.chat : SELECTORS.terminal);
      if (!(await visible(surface, 30_000))) return { status: "FAIL", reason: `session opened without a ${mode} surface` };
      if (mode === "chat") {
        const answered = await visible(page.locator(SELECTORS.chatMessages), 120_000);
        return answered ? { status: "PASS", reason: "Chat session answered" } : { status: "FAIL", reason: "Chat session opened but no assistant reply within 2 minutes" };
      }
      await page.waitForTimeout(20_000);
      return { status: "PASS", reason: "terminal session is running (see screenshot for the agent's output)" };
    });
  }
  return finish(browser, context, { harness, displayName });
}

async function finish(browser, context, meta) {
  const report = { schemaVersion: 1, ...meta, finishedAt: new Date().toISOString(), checkpoints: context.results };
  const lines = [`# AO UI audit: ${meta.displayName || meta.harness}`, "", "| # | Checkpoint | Result | Reason | Screenshot |", "|---|---|---|---|---|"];
  context.results.forEach((item, index) => lines.push(`| ${index + 1} | ${item.title} | ${item.status} | ${String(item.reason).replaceAll("|", "\\|")} | ${item.screenshot ? `![](${item.screenshot.split("/").pop()})` : "none"} |`));
  await writeFile(join(context.out, "ui-report.json"), `${JSON.stringify(report, null, 2)}\n`);
  await writeFile(join(context.out, "ui-report.md"), `${lines.join("\n")}\n`);
  await browser.close().catch(() => {});
  return report;
}

async function main(argv = process.argv.slice(2)) {
  const options = {
    cdp: "http://127.0.0.1:9222", repo: process.env.AO_REPO || join(homedir(), "Desktop", "agent-orchestrator"), harness: "", label: "", model: "",
    out: "", interfaces: ["tui", "chat"], install: false, login: false, spawn: true,
    prompt: "AO UI audit: reply with the single word READY, then stop.",
  };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    const next = () => { if (argv[index + 1] === undefined) throw new Error(`missing value for ${argument}`); return argv[++index]; };
    if (argument === "--install") options.install = true;
    else if (argument === "--login") options.login = true;
    else if (argument === "--no-spawn") options.spawn = false;
    else if (argument === "--interfaces") options.interfaces = next().split(",").filter((item) => ["tui", "chat"].includes(item));
    else if (["--cdp", "--repo", "--harness", "--label", "--model", "--out", "--prompt"].includes(argument)) options[argument.slice(2)] = next();
    else if (argument === "-h" || argument === "--help") { console.log("Usage: node scripts/ui_audit.mjs --harness ID [--out DIR] [--cdp URL] [--repo AO_CHECKOUT] [--label NAME] [--model ID] [--interfaces tui,chat] [--install] [--login] [--no-spawn]"); return 0; }
    else throw new Error(`unknown option ${argument}`);
  }
  if (!options.harness) throw new Error("--harness is required");
  options.out = resolve(options.out || join(homedir(), "Desktop", "agents", "ao-audit-reports", `${new Date().toISOString().replace(/[:.]/g, "-")}-ui-${options.harness}`));
  const report = await uiAudit(options);
  console.log(`UI report: ${join(options.out, "ui-report.md")}`);
  return report.checkpoints.every((item) => ["PASS", "NOT_RUN"].includes(item.status)) ? 0 : 1;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) {
  try { process.exitCode = await main(); } catch (error) { console.error(`error: ${error.message}`); process.exitCode = 2; }
}
