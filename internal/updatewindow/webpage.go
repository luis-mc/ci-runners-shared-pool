package updatewindow

import (
	"fmt"
	"html"
	"strings"
)

// The page of the web window. Every colour is a token of the Phosphor dark theme, written into
// custom properties from the generated palette (tokens_gen.go); no colour is typed here. The
// text is set with textContent, never as markup, so a reason or a path cannot inject anything.

func hexColour(c RGB) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func tokensCSS(p Palette) string {
	rows := [][2]string{
		{"page", hexColour(p.Page)}, {"surface", hexColour(p.Surface)}, {"text", hexColour(p.Text)}, {"muted", hexColour(p.TextMuted)},
		{"border", hexColour(p.Border)}, {"divider", hexColour(p.Divider)}, {"primary", hexColour(p.Primary)},
		{"on-primary", hexColour(p.OnPrimary)}, {"focus", hexColour(p.Focus)}, {"selection", hexColour(p.Selection)},
		{"done", hexColour(p.Done)}, {"problem", hexColour(p.Problem)},
	}
	var b strings.Builder
	b.WriteString(":root{")
	for _, r := range rows {
		fmt.Fprintf(&b, "--c-%s:%s;", r[0], r[1])
	}
	b.WriteString("}")
	return b.String()
}

const pageCSS = `
*{box-sizing:border-box}
html,body{margin:0;height:100%}
body{background:var(--c-page);color:var(--c-text);font:15px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,"DejaVu Sans Mono",monospace;display:flex;flex-direction:column}
.band{display:block;width:100%;height:auto;flex:none}
main{flex:1;padding:20px 24px 8px;overflow:auto}
.step{color:var(--c-muted);font-size:13px;margin:0 0 6px}
h1{font-size:18px;margin:0 0 12px;color:var(--c-primary)}
h1.done{color:var(--c-done)}
h1.problem{color:var(--c-problem)}
p{margin:0 0 10px;overflow-wrap:anywhere}
.bar{height:12px;border:1px solid var(--c-border);margin:14px 0}
.bar>i{display:block;height:100%;background:var(--c-primary);width:0}
.bar.indeterminate>i{width:30%;animation:slide 1.4s linear infinite}
@keyframes slide{from{margin-left:-30%}to{margin-left:100%}}
@media (prefers-reduced-motion:reduce){.bar.indeterminate>i{animation:none;width:100%;opacity:.5}}
.pct{color:var(--c-muted);font-size:13px;margin:-8px 0 10px}
fieldset{border:0;margin:14px 0 0;padding:0}
label.opt{display:flex;gap:10px;align-items:flex-start;padding:8px 10px;border:1px solid var(--c-divider);margin-bottom:8px;cursor:pointer}
label.opt:has(input:checked){background:var(--c-selection);border-color:var(--c-border)}
label.opt input{margin-top:4px;accent-color:var(--c-primary)}
label.opt small{display:block;color:var(--c-muted)}
footer{flex:none;border-top:1px solid var(--c-divider);padding:12px 24px;display:flex;justify-content:flex-end;gap:10px;background:var(--c-surface)}
button{font:inherit;color:var(--c-text);background:transparent;border:1px solid var(--c-border);padding:6px 16px;cursor:pointer}
button.primary{background:var(--c-primary);color:var(--c-on-primary);border-color:var(--c-primary);font-weight:bold}
button:focus-visible,label.opt:has(input:focus-visible){outline:2px solid var(--c-focus);outline-offset:-4px}
button:disabled{opacity:.5;cursor:default}
`

const pageJS = `
"use strict";
var base = location.pathname, view = null, busy = false;
function el(t, c, x) { var e = document.createElement(t); if (c) e.className = c; if (x != null) e.textContent = x; return e; }
function opts() { var o = {}; document.querySelectorAll("input[data-id]").forEach(function (i) { o[i.dataset.id] = i.checked; }); return o; }
function draw(v) {
  view = v;
  var m = document.getElementById("main"), f = document.getElementById("foot");
  var keep = opts(), had = Object.keys(keep).length > 0 && m.dataset.state === v.state;
  m.dataset.state = v.state;
  m.replaceChildren();
  if (v.step) m.appendChild(el("p", "step", v.step));
  m.appendChild(el("h1", v.tone === "normal" ? "" : v.tone, v.heading));
  v.body.forEach(function (t) { m.appendChild(el("p", "", t)); });
  if (v.bar !== "none") {
    var b = el("div", "bar " + v.bar); b.setAttribute("role", "progressbar");
    var i = el("i"); if (v.bar === "determinate") { i.style.width = v.percent + "%"; b.setAttribute("aria-valuenow", v.percent); }
    b.appendChild(i); m.appendChild(b);
    if (v.bar === "determinate") m.appendChild(el("p", "pct", v.percent + "%"));
  }
  if (v.options && v.options.length) {
    var fs = el("fieldset");
    v.options.forEach(function (o) {
      var l = el("label", "opt"), c = el("input"); c.type = "checkbox"; c.dataset.id = o.id; c.checked = had ? !!keep[o.id] : o.on;
      var s = el("span", "", o.label); if (o.hint) s.appendChild(el("small", "", o.hint));
      l.appendChild(c); l.appendChild(s); fs.appendChild(l);
    });
    m.appendChild(fs);
  }
  f.replaceChildren();
  v.buttons.forEach(function (b) {
    var x = el("button", b.primary ? "primary" : "", b.busy || b.label); x.type = "button"; x.dataset.id = b.id; x.disabled = !!b.disabled;
    x.addEventListener("click", function () { press(b.id); }); f.appendChild(x);
  });
  var d = f.querySelector('button[data-id="' + v.default + '"]');
  if (d && !d.disabled && document.activeElement === document.body) d.focus();
}
function press(id) {
  if (busy) return; busy = true;
  fetch(base + "press", { method: "POST", headers: { "X-Crsp-Window": "1", "Content-Type": "application/json" }, body: JSON.stringify({ id: id, options: opts() }) })
    .then(function (r) { return r.json(); })
    .then(function (a) { busy = false; if (a.close) window.close(); else tick(); })
    .catch(function () { busy = false; });
}
function tick() {
  fetch(base + "view", { cache: "no-store" }).then(function (r) { return r.json(); }).then(draw).catch(function () {});
}
document.addEventListener("keydown", function (e) {
  if (!view) return;
  if (e.key === "Escape" && view.escape) { var b = document.querySelector('button[data-id="' + view.escape + '"]'); if (b && !b.disabled) press(view.escape); }
});
tick(); setInterval(tick, 1000);
`

// RenderPage is the one HTML page of the web window.
func RenderPage(title string) string {
	return "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>" + html.EscapeString(title) + "</title><style>" + tokensCSS(Phosphor) + pageCSS + "</style></head><body>" +
		"<img class=\"band\" src=\"band.png\" alt=\"" + html.EscapeString(ProductName) + "\">" +
		"<main id=\"main\" aria-live=\"polite\"></main><footer id=\"foot\"></footer><script>" + pageJS + "</script></body></html>"
}
