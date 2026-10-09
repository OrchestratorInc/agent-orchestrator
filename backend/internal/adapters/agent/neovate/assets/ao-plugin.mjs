// agent-orchestrator: managed neovate plugin
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

const config = __AO_CONFIG__;
const launchID = process.env.AO_RUNTIME_LAUNCH_ID || '';
const args = process.argv.slice(2);
const optionArgs = args.slice(0, args.indexOf('--') < 0 ? args.length : args.indexOf('--'));
const resumeIndex = optionArgs.indexOf('--resume');
const resumedID = resumeIndex < 0 ? '' : optionArgs[resumeIndex + 1];
let mainID = resumedID;
let pendingAcceptance;

function readEntries(transcript) {
  try {
    return fs.readFileSync(transcript, 'utf8').split('\n').filter(x => x.trim()).map(x => JSON.parse(x));
  } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
}

function checkRestore(context) {
  if (!resumedID) return;
  const marker = JSON.parse(fs.readFileSync(config.marker, 'utf8'));
  const transcript = context.paths.getSessionLogPath(resumedID);
  if (marker.nativeID !== resumedID || marker.workspace !== config.workspace || marker.transcript !== transcript) {
    throw new Error('AO: Neovate native session/workspace mismatch');
  }
  const entries = readEntries(transcript);
  const messages = entries.filter(x => x.type === 'message');
  if (!messages.some(x => x.role === 'user') || messages.some(x => x.sessionId !== resumedID || !x.uuid || !['system', 'user', 'assistant', 'tool'].includes(x.role) || !(typeof x.content === 'string' || Array.isArray(x.content)))) {
    throw new Error('AO: Neovate native history is missing or corrupt');
  }
}

function report(event, nativeID, extra = {}) {
  if (!nativeID || nativeID !== mainID) return;
  try {
    spawnSync('ao', ['hooks', 'neovate', event], {
      input: JSON.stringify({session_id: nativeID, launch_id: launchID, ...extra}) + '\n',
      cwd: config.workspace, env: process.env, timeout: 5000, windowsHide: true,
      stdio: ['pipe', 'ignore', 'ignore'],
    });
  } catch { /* Reporting must not break provider work. */ }
}

// The native userPrompt hook precedes context construction and JSONL append.
// Its next provider-resolution hook occurs after the user row is durable.
// Confirm that row before reporting semantic acceptance or binding its ID.
function confirmAcceptance(context) {
  if (!pendingAcceptance || path.resolve(context.cwd) !== config.workspace) return;
  const {sessionId, prompt, transcript, previousIDs} = pendingAcceptance;
  const message = readEntries(transcript).find(entry => entry.type === 'message' && entry.role === 'user' && entry.sessionId === sessionId && entry.uuid && !previousIDs.has(entry.uuid) && (
    entry.content === prompt || (Array.isArray(entry.content) && entry.content.some(part => part.type === 'text' && part.text === prompt))
  ));
  if (!message) return;
  const marker = {nativeID: sessionId, workspace: config.workspace, transcript};
  fs.writeFileSync(config.marker + '.tmp', JSON.stringify(marker), {mode: 0o600});
  fs.renameSync(config.marker + '.tmp', config.marker);
  pendingAcceptance = undefined;
  report('user-prompt-submit', sessionId, {prompt, transcript_path: transcript, native_message_id: message.uuid});
}

export default {
  name: 'ao-neovate',
  enforce: 'post',
  initialized() {
    if (path.resolve(this.cwd) !== config.workspace) return;
    checkRestore(this);
  },
  systemPrompt(defaults, {sessionId}) {
    if (path.resolve(this.cwd) !== config.workspace) return defaults;
    if (!mainID) mainID = sessionId;
    return sessionId === mainID && config.prompt ? defaults + '\n\n' + config.prompt : defaults;
  },
  userPrompt(prompt, {sessionId}) {
    if (path.resolve(this.cwd) !== config.workspace) return prompt;
    if (!mainID) mainID = sessionId;
    if (sessionId !== mainID) return prompt;
    checkRestore(this);
    const transcript = this.paths.getSessionLogPath(sessionId);
    const entries = readEntries(transcript);
    const sessionModel = entries.find(entry => entry.type === 'config')?.config?.model;
    if (!(this.argvConfig.model || sessionModel || this.config.model)) {
      throw new Error('AO: Neovate model configuration is required; use native /login and /model before spawning');
    }
    pendingAcceptance = {sessionId, prompt, transcript, previousIDs: new Set(entries.map(entry => entry.uuid).filter(Boolean))};
    return prompt;
  },
  provider(providers) { confirmAcceptance(this); return providers; },
  toolUse(tool, {sessionId}) { report('active', sessionId); return tool; },
  toolResult(result, {sessionId}) { report('active', sessionId); return result; },
  stop({sessionId}) { report('stop', sessionId); },
};
