// HTML templates. Static strings with escaped interpolation; no framework.
const esc = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");

export const htmlHeaders = {
  "content-type": "text/html; charset=utf-8",
  "cache-control": "no-store",
};

export const html = (status: number, body: string): Response => new Response(body, { status, headers: htmlHeaders });

const layout = (title: string, body: string): string => `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:36rem;margin:3rem auto;padding:0 1rem;color:#1a1a1a}
code{font-family:ui-monospace,monospace}
.key{display:block;word-break:break-all;background:#f3f3f3;padding:.75rem;border-radius:6px}
input[name=m]{width:100%;box-sizing:border-box;font-family:ui-monospace,monospace;padding:.5rem}
button{margin-top:.75rem;padding:.5rem 1rem;font-size:1rem}
</style>
</head>
<body>
${body}
</body>
</html>
`;

/** `mid` is the prefilled machine code, or null when the query had no valid one. */
export function homePage(mid: string | null): string {
  const help = mid === null
    ? `<p>Run <code>rootout license</code> on your Mac to see its machine code, then paste it below.</p>`
    : "";
  return layout("Rootout licence key", `<h1>Rootout licence key</h1>
${help}
<form method="GET" action="/start">
<label>Machine code<br><input name="m" value="${mid === null ? "" : esc(mid)}" pattern="[0-9a-f]{32}" required autocomplete="off" spellcheck="false"></label>
<button type="submit">Log in with Patreon</button>
</form>`);
}

export function keyPage(key: string, devices: { mid: string }[], mid: string): string {
  const others = devices.filter((d) => d.mid !== mid).length;
  return layout("Your Rootout key", `<h1>Your licence key</h1>
<p>Run this on the Mac with machine code <code>${esc(mid)}</code>:</p>
<p><code>rootout license ${esc(key)}</code></p>
<p>The key:</p>
<code id="key" class="key">${esc(key)}</code>
<button type="button" id="copy">Copy key</button>
<p>This Mac, and ${others} other${others === 1 ? "" : "s"}. A key works on up to two Macs; a third replaces the oldest.</p>
<script>
document.getElementById("copy").addEventListener("click",function(){
  var b=this;navigator.clipboard.writeText(document.getElementById("key").textContent).then(function(){b.textContent="Copied"});
});
</script>`);
}

export function deniedPage(reason: string, patreonUrl: string): string {
  return layout("No active membership", `<h1>No key issued</h1>
<p>Sorry, ${esc(reason)}.</p>
<p><a href="${esc(patreonUrl)}">Open the membership page on Patreon</a>, then come back and log in again.</p>`);
}

export function errorPage(msg: string): string {
  return layout("Rootout licence key", `<h1>Something went wrong</h1>
<p>${esc(msg)}</p>`);
}
