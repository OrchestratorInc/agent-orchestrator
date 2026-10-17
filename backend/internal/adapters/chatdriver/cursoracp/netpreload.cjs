// Written by Agent Orchestrator and loaded into `cursor-agent acp` through
// NODE_OPTIONS. It only changes how Cursor's own API calls connect.
//
// Cursor bootstraps global-agent, whose replacement HTTP(S) agents declare no
// keepAlive, so Node sends "Connection: close" and every API call pays a new
// TCP and TLS handshake. With no proxy configured, global-agent hands each
// request to Node's keep-alive agent; marking the replacement keep-alive lets
// those connections be reused. A few TLS connections to the API host are also
// opened ahead of the first calls, which run in parallel when a session opens.
"use strict";
(() => {
  const path = require("node:path");
  const self = path.basename(__filename);
  const options = process.env.NODE_OPTIONS;
  if (options !== undefined) {
    // Commands the agent runs for the user must not load this file.
    const kept = options
      .replace(/--require\s+(?:"[^"]*"|\S+)/g, (flag) => (flag.includes(self) ? "" : flag))
      .replace(/\s+/g, " ")
      .trim();
    if (kept) process.env.NODE_OPTIONS = kept;
    else delete process.env.NODE_OPTIONS;
  }
  if (!process.argv.includes("acp")) return;
  const proxyVars = [
    "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy",
    "GLOBAL_AGENT_HTTP_PROXY", "GLOBAL_AGENT_HTTPS_PROXY", "NODE_USE_ENV_PROXY",
  ];
  if (proxyVars.some((name) => process.env[name])) return;

  const http = require("node:http");
  const https = require("node:https");
  const tls = require("node:tls");

  for (const mod of [http, https]) {
    const descriptor = Object.getOwnPropertyDescriptor(mod, "globalAgent");
    if (!descriptor || !descriptor.configurable) continue;
    let current = mod.globalAgent;
    Object.defineProperty(mod, "globalAgent", {
      configurable: true,
      enumerable: true,
      get() {
        return current;
      },
      set(agent) {
        if (agent && agent.keepAlive === undefined && agent.fallbackAgent && agent.fallbackAgent.keepAlive) {
          agent.keepAlive = true;
        }
        current = agent;
      },
    });
  }

  if (process.env.CURSOR_API_ENDPOINT) return;
  const host = "api2.cursor.sh";
  const warm = [];
  for (let i = 0; i < 3; i++) {
    const socket = tls.connect({ host, port: 443, servername: host, ALPNProtocols: ["http/1.1"] });
    socket.on("error", () => {});
    socket.unref();
    warm.push(socket);
  }
  setTimeout(() => {
    for (const socket of warm.splice(0)) socket.destroy();
  }, 20000).unref();
  const agent = https.globalAgent;
  const createConnection = agent.createConnection;
  agent.createConnection = function (opts, callback) {
    // Only a request with Node's default TLS checks may take a socket dialed
    // before it existed; anything that pins, swaps, or resolves differently
    // dials its own.
    const plain = !opts.ca && !opts.cert && !opts.key && !opts.pfx && opts.rejectUnauthorized !== false &&
      !opts.checkServerIdentity && !opts.secureContext && !opts.lookup && !opts.family &&
      (!opts.servername || opts.servername === host);
    if (plain && (opts.host === host || opts.hostname === host) && Number(opts.port || 443) === 443) {
      while (warm.length) {
        const socket = warm.shift();
        if (!socket.destroyed && socket.writable) {
          socket.ref();
          return socket;
        }
      }
    }
    return createConnection.call(this, opts, callback);
  };
})();
