// Demo site for the credential-vault e2e: /login (username + password) then
// /2fa (TOTP) then /welcome. Logs only paths and outcomes, never values.
const http = require("node:http");
const crypto = require("node:crypto");

const USER = "alice@example.test";
const PASSWORD = process.env.DEMO_PASSWORD;
const SEED = process.env.DEMO_SEED;
if (!PASSWORD || !SEED) throw new Error("DEMO_PASSWORD and DEMO_SEED are required");

function base32(input) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0, value = 0; const out = [];
  for (const c of input) { value = (value << 5) | alphabet.indexOf(c); bits += 5; if (bits >= 8) { out.push((value >>> (bits - 8)) & 255); bits -= 8; } }
  return Buffer.from(out);
}
function totp(step) {
  const counter = Buffer.alloc(8); counter.writeBigUInt64BE(BigInt(step));
  const h = crypto.createHmac("sha1", base32(SEED)).update(counter).digest();
  const o = h[h.length - 1] & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1e6).padStart(6, "0");
}
const page = (title, body) => `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title>
<style>body{font:15px system-ui;background:#f4f4f5;display:grid;place-items:center;height:100vh;margin:0}
form,main{background:#fff;padding:28px;border-radius:10px;width:300px;box-shadow:0 1px 4px #0002;display:grid;gap:10px}
input{padding:8px;font-size:15px}button{padding:9px;font-size:15px;background:#2563eb;color:#fff;border:0;border-radius:6px}
.err{color:#b91c1c}</style></head><body>${body}</body></html>`;
const sessions = new Map();

http.createServer((req, res) => {
  const url = new URL(req.url, "http://localhost");
  const cookie = /sid=([a-f0-9]+)/.exec(req.headers.cookie || "")?.[1];
  const state = cookie && sessions.get(cookie);
  const send = (html, headers = {}) => { res.writeHead(200, { "content-type": "text/html", ...headers }); res.end(html); };
  const redirect = (to, headers = {}) => { res.writeHead(303, { location: to, ...headers }); res.end(); };
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    const form = new URLSearchParams(body);
    if (url.pathname === "/" || url.pathname === "/login") {
      if (req.method === "POST") {
        const ok = form.get("username") === USER && form.get("password") === PASSWORD;
        console.log(`POST /login ${ok ? "ok" : "rejected"}`);
        if (!ok) return send(page("Sign in", `<form method="post" action="/login"><h2>Sign in to Demo</h2><p class="err">Wrong username or password</p><label>Email<input name="username" type="email" autocomplete="username"></label><label>Password<input name="password" type="password" autocomplete="current-password"></label><button>Continue</button></form>`));
        const sid = crypto.randomBytes(8).toString("hex");
        sessions.set(sid, "password");
        return redirect("/2fa", { "set-cookie": `sid=${sid}; HttpOnly; Path=/` });
      }
      return send(page("Sign in", `<form method="post" action="/login"><h2>Sign in to Demo</h2><label>Email<input name="username" type="email" autocomplete="username"></label><label>Password<input name="password" type="password" autocomplete="current-password"></label><button>Continue</button></form>`));
    }
    if (url.pathname === "/2fa") {
      if (state !== "password") return redirect("/login");
      if (req.method === "POST") {
        const step = Math.floor(Date.now() / 30000);
        const code = form.get("code");
        const ok = [step - 1, step, step + 1].some((s) => totp(s) === code);
        console.log(`POST /2fa ${ok ? "ok" : "rejected"}`);
        if (ok) { sessions.set(cookie, "done"); return redirect("/welcome"); }
      }
      return send(page("Two-factor", `<form method="post" action="/2fa"><h2>Two-factor authentication</h2><label>Authenticator code<input name="code" inputmode="numeric" autocomplete="one-time-code"></label><button>Verify</button></form>`));
    }
    if (url.pathname === "/welcome") {
      if (state !== "done") return redirect("/login");
      return send(page("Welcome", `<main><h2>Welcome, ${USER}</h2><p>You are signed in with two-factor authentication.</p></main>`));
    }
    res.writeHead(404); res.end();
  });
}).listen(4173, "127.0.0.1", () => console.log("demo listening on http://localhost:4173"));
