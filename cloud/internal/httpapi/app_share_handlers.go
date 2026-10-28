package httpapi

import "net/http"

// appShareLinkHTML is the page a shared session's link opens in the browser:
// https://<cp-host>/app/share#<orgId>/<token>. It hands the share to the
// desktop app through the ao-app://share/<orgId>/<token> deep link, so a link
// pasted into a chat or an email is one click instead of a paste into the app.
//
// The share sits in the URL fragment, which the browser never sends, so the
// token reaches neither this server's logs nor a proxy's. The page checks its
// shape before building the deep link and never writes it into the HTML.
const appShareLinkHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Open a shared session</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
         max-width: 30rem; margin: 18vh auto; padding: 0 1.5rem; text-align: center;
         color: #1a1a1a; background: #ffffff; }
  @media (prefers-color-scheme: dark) { body { color: #ededed; background: #0b0b0c; } }
  h1 { font-size: 1.2rem; font-weight: 600; margin: 0 0 .5rem; }
  p { color: #6b7280; margin: 0 0 1.5rem; }
  a.button { display: inline-block; padding: .6rem 1.2rem; border-radius: .5rem;
             background: #3b82f6; color: #ffffff; text-decoration: none; font-weight: 500; }
  [hidden] { display: none !important; }
</style>
</head>
<body>
<div id="open">
<h1>Opening the shared session</h1>
<p>Agent Orchestrator should open it now. If it doesn&rsquo;t, use the button.</p>
<a class="button" id="link" href="#">Open in Agent Orchestrator</a>
</div>
<div id="invalid" hidden>
<h1>This share link is incomplete</h1>
<p>Ask the person who shared it to copy the link again.</p>
</div>
<script>
  (function () {
    var share = window.location.hash.replace(/^#/, "").split("/");
    var uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
    var token = /^[A-Za-z0-9_-]{16,256}$/;
    if (share.length !== 2 || !uuid.test(share[0]) || !token.test(share[1])) {
      document.getElementById("open").hidden = true;
      document.getElementById("invalid").hidden = false;
      return;
    }
    var target = "ao-app://share/" + share[0] + "/" + share[1];
    document.getElementById("link").setAttribute("href", target);
    window.location.replace(target);
  })();
</script>
</body>
</html>
`

// appShareLink serves the shared-session page. It is public and takes no
// input on the server: the share is in the fragment, read by the page itself.
func (s *Server) appShareLink(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(appShareLinkHTML))
}
