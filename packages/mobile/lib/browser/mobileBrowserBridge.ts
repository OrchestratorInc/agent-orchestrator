import type { MobileBrowserCommand, MobileBrowserCommandResult } from "./mobileBrowserRuntime";

const MESSAGE_KEY = "__aoMobileBrowser";

export type BrowserBridgeMessage = {
	__aoMobileBrowser: true;
	requestId: string;
	ok: boolean;
	result?: Record<string, unknown>;
	error?: { code: string; message: string };
};

// Installed in every document before its content loads. It intentionally uses
// no eval and exposes only the bounded command vocabulary implemented below.
export const MOBILE_BROWSER_BOOTSTRAP = `
(function () {
  if (window.__aoMobileBrowserBridge) return true;
  var refs = Object.create(null), refInfo = Object.create(null);
  var generation = 0;
  function visible(el) {
    if (!el || !el.getBoundingClientRect) return false;
    var r = el.getBoundingClientRect(), s = window.getComputedStyle(el);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none';
  }
  function clean(value) { return String(value || '').replace(/\\s+/g, ' ').trim().slice(0, 240); }
  function role(el) {
    return el.getAttribute('role') || ({A:'link',BUTTON:'button',INPUT:el.type === 'checkbox' ? 'checkbox' : 'textbox',TEXTAREA:'textbox',SELECT:'combobox'}[el.tagName] || el.tagName.toLowerCase());
  }
  function name(el) { return clean(el.getAttribute('aria-label') || el.getAttribute('alt') || el.getAttribute('placeholder') || el.innerText || el.value || el.title); }
  function snapshot(interactive) {
    generation += 1; refs = Object.create(null); refInfo = Object.create(null);
    var selector = interactive ? 'a,button,input,textarea,select,[role],[tabindex]' : 'a,button,input,textarea,select,[role],[tabindex],h1,h2,h3,p,li';
    var nodes = Array.prototype.slice.call(document.querySelectorAll(selector));
    var lines = [], count = 0;
    nodes.forEach(function (el) {
      if (lines.length >= 500 || !visible(el)) return;
      var interactiveEl = el.matches('a,button,input,textarea,select,[role],[tabindex]');
      var label = name(el); if (!label) return;
      var ref = '';
      if (interactiveEl) { ref = 'e' + (++count); refs[ref] = el; refInfo[ref] = { role: role(el), name: label }; }
      lines.push('- ' + role(el) + ' "' + label.replace(/"/g, '\\"') + '"' + (ref ? ' [ref=' + ref + ']' : ''));
    });
    return { text: lines.join('\\n'), refs: refInfo, generation: generation, url: location.href, title: document.title };
  }
  function target(ref) {
    var el = refs[String(ref || '')];
    if (!el || !el.isConnected) { var e = new Error('Element reference is stale; take another snapshot.'); e.code = 'STALE_REFERENCE'; throw e; }
    return el;
  }
  function inputValue(el, value) {
    var proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    var setter = Object.getOwnPropertyDescriptor(proto, 'value');
    if (setter && setter.set) setter.set.call(el, value); else el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
  }
  function wait(args) {
    var timeout = Math.min(Number(args.timeoutMs || 10000), 55000), started = Date.now();
    return new Promise(function (resolve, reject) {
      function check() {
        var body = document.body ? document.body.innerText : '';
        var ok = args.ms ? Date.now() - started >= Number(args.ms) :
          args.text ? body.indexOf(String(args.text)) >= 0 :
          args.textGone ? body.indexOf(String(args.textGone)) < 0 :
          args.selector ? !!document.querySelector(String(args.selector)) :
          args.selectorGone ? !document.querySelector(String(args.selectorGone)) :
          args.url ? location.href.indexOf(String(args.url)) >= 0 :
          args.load ? document.readyState === 'complete' :
          args.stableMs ? Date.now() - started >= Number(args.stableMs) : false;
        if (ok) return resolve({ matched: true, url: location.href });
        if (Date.now() - started >= timeout) { var e = new Error('Timed out waiting for page condition.'); e.code = 'WAIT_TIMEOUT'; return reject(e); }
        setTimeout(check, 100);
      }
      check();
    });
  }
  window.__aoMobileBrowserBridge = {
    run: function (command) {
      var a = command.args || {}, result;
      try {
        switch (command.action) {
          case 'snapshot': result = snapshot(!!a.interactive); break;
          case 'click': target(a.ref).click(); result = { clicked: a.ref }; break;
          case 'dblclick': target(a.ref).dispatchEvent(new MouseEvent('dblclick', { bubbles:true, cancelable:true, view:window })); result = { clicked: a.ref }; break;
          case 'focus': target(a.ref).focus(); result = { focused: a.ref }; break;
          case 'hover': target(a.ref).dispatchEvent(new MouseEvent('mouseover', { bubbles:true, cancelable:true, view:window })); result = { hovered:a.ref }; break;
          case 'fill': var f=target(a.ref); f.focus(); inputValue(f, String(a.text || a.value || '')); result={ filled:a.ref }; break;
          case 'type': var t=target(a.ref); t.focus(); inputValue(t, String(t.value || '') + String(a.text || a.value || '')); result={ typed:a.ref }; break;
          case 'check': var c=target(a.ref); if(!c.checked)c.click(); result={ checked:a.ref }; break;
          case 'uncheck': var u=target(a.ref); if(u.checked)u.click(); result={ unchecked:a.ref }; break;
          case 'press': var active=document.activeElement || document.body, key=String(a.key || ''); active.dispatchEvent(new KeyboardEvent('keydown',{key:key,bubbles:true})); active.dispatchEvent(new KeyboardEvent('keyup',{key:key,bubbles:true})); result={ pressed:key }; break;
          case 'scroll': var amount=Number(a.amount || 500), x=0,y=0; if(a.direction==='up')y=-amount;else if(a.direction==='left')x=-amount;else if(a.direction==='right')x=amount;else y=amount; window.scrollBy({left:x,top:y,behavior:'smooth'}); result={ scrolled:true }; break;
          case 'scrollintoview': target(a.ref).scrollIntoView({block:'center',behavior:'smooth'}); result={ scrolled:a.ref }; break;
          case 'get': if(!a.ref){result={url:location.href,title:document.title,text:document.body?clean(document.body.innerText).slice(0,20000):''};}else{var g=target(a.ref);result={text:clean(g.innerText||g.textContent),value:g.value,checked:!!g.checked};} break;
          case 'wait': return wait(a).then(function(v){ post(command.requestId,true,v); },function(e){ post(command.requestId,false,null,e); });
          default: var unsupported=new Error('This command is not available on the mobile browser yet.'); unsupported.code='BROWSER_ACTION_UNSUPPORTED'; throw unsupported;
        }
        post(command.requestId, true, result || {});
      } catch (error) { post(command.requestId, false, null, error); }
    }
  };
  function post(requestId, ok, result, error) {
    window.ReactNativeWebView.postMessage(JSON.stringify({ ${JSON.stringify(MESSAGE_KEY)}: true, requestId: requestId, ok: ok, result: result, error: error ? { code:error.code || 'MOBILE_BROWSER_COMMAND_FAILED', message:error.message || String(error) } : undefined }));
  }
  return true;
})(); true;
`;

export function browserCommandScript(command: MobileBrowserCommand): string {
	return `${MOBILE_BROWSER_BOOTSTRAP}\nwindow.__aoMobileBrowserBridge && window.__aoMobileBrowserBridge.run(${JSON.stringify(command)}); true;`;
}

export function parseBrowserBridgeMessage(raw: string): BrowserBridgeMessage | undefined {
	try {
		const message = JSON.parse(raw) as Partial<BrowserBridgeMessage>;
		if (message[MESSAGE_KEY] !== true || typeof message.requestId !== "string" || typeof message.ok !== "boolean") return undefined;
		return message as BrowserBridgeMessage;
	} catch {
		return undefined;
	}
}

export function bridgeResult(message: BrowserBridgeMessage): MobileBrowserCommandResult {
	return message.ok
		? { ok: true, result: message.result ?? {} }
		: { ok: false, error: message.error ?? { code: "MOBILE_BROWSER_COMMAND_FAILED", message: "Mobile browser command failed" } };
}
