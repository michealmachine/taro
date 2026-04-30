package templates

import (
	"html"
	"net/http"
	"strings"
)

type PageMeta struct {
	Title string
	Path  string
}

func RenderLayout(w http.ResponseWriter, meta PageMeta, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(buildLayout(meta, body)))
}

func buildLayout(meta PageMeta, body string) string {
	title := meta.Title
	if strings.TrimSpace(title) == "" {
		title = "taro"
	}
	return "<!doctype html>" +
		"<html lang=\"en\">" +
		"<head>" +
		"<meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>" + html.EscapeString(title) + "</title>" +
		"<script src=\"https://unpkg.com/htmx.org@1.9.12\"></script>" +
		"<style>" +
		baseStyles() +
		"</style>" +
		"</head>" +
		"<body>" +
		renderHeader(meta) +
		"<main class=\"container\">" + body + "</main>" +
		"</body>" +
		"</html>"
}

func renderHeader(meta PageMeta) string {
	return "<header class=\"topbar\">" +
		"<div class=\"brand\">taro</div>" +
		"<nav class=\"nav\">" +
		renderNavLink("/entries", "Entries", meta.Path) +
		renderNavLink("/pending", "Pending", meta.Path) +
		renderNavLink("/status", "Status", meta.Path) +
		"</nav>" +
		"</header>"
}

func renderNavLink(path, label, current string) string {
	class := "nav-link"
	if path == current {
		class += " active"
	}
	return "<a class=\"" + class + "\" href=\"" + path + "\">" + html.EscapeString(label) + "</a>"
}

func baseStyles() string {
	return strings.Join([]string{
		":root{--bg:#0f1115;--panel:#161a22;--panel-2:#1c2230;--text:#eef1f6;--muted:#a0aec0;--accent:#64b5f6;--accent-2:#7dd3fc;--danger:#f87171;--warn:#fbbf24;--ok:#34d399;--border:#2a3243}",
		"*{box-sizing:border-box}",
		"body{margin:0;font-family:'IBM Plex Sans','Space Grotesk','Segoe UI',sans-serif;background:radial-gradient(circle at top, #1c2230 0%, #0f1115 50%, #0b0d12 100%);color:var(--text)}",
		"a{color:inherit;text-decoration:none}",
		".container{padding:28px 28px 64px}",
		".topbar{display:flex;align-items:center;justify-content:space-between;padding:20px 28px;border-bottom:1px solid var(--border);background:rgba(10,12,18,0.75);backdrop-filter:blur(12px);position:sticky;top:0;z-index:10}",
		".brand{font-weight:600;letter-spacing:0.5px}",
		".nav{display:flex;gap:16px}",
		".nav-link{padding:6px 12px;border-radius:999px;color:var(--muted);border:1px solid transparent}",
		".nav-link.active{color:var(--text);border-color:var(--accent)}",
		".page-title{font-size:22px;font-weight:600;margin:0 0 6px}",
		".subtitle{color:var(--muted);margin:0 0 24px}",
		".card{background:var(--panel);border:1px solid var(--border);border-radius:14px;padding:18px;margin-bottom:18px;box-shadow:0 12px 30px rgba(5,7,12,0.35)}",
		".card h3{margin:0 0 12px;font-size:16px}",
		".grid{display:grid;gap:16px}",
		".grid-2{grid-template-columns:repeat(auto-fit,minmax(280px,1fr))}",
		".grid-3{grid-template-columns:repeat(auto-fit,minmax(220px,1fr))}",
		".kpi{padding:14px;border-radius:12px;background:var(--panel-2);border:1px solid var(--border)}",
		".kpi span{display:block;font-size:12px;color:var(--muted)}",
		".kpi strong{font-size:20px}",
		"table{width:100%;border-collapse:collapse;font-size:13px}",
		"th,td{padding:10px 8px;border-bottom:1px solid var(--border);text-align:left}",
		"th{color:var(--muted);font-weight:500;letter-spacing:0.02em;text-transform:uppercase;font-size:11px}",
		"tr:hover{background:rgba(255,255,255,0.02)}",
		".tag{display:inline-flex;align-items:center;padding:2px 8px;border-radius:999px;font-size:11px;border:1px solid var(--border);background:rgba(255,255,255,0.03)}",
		".tag.ok{border-color:rgba(52,211,153,0.4);color:var(--ok)}",
		".tag.warn{border-color:rgba(251,191,36,0.4);color:var(--warn)}",
		".tag.danger{border-color:rgba(248,113,113,0.4);color:var(--danger)}",
		".tag.info{border-color:rgba(125,211,252,0.5);color:var(--accent-2)}",
		".pill{padding:4px 10px;border-radius:999px;background:rgba(100,181,246,0.15);color:var(--accent);font-size:12px}",
		".muted{color:var(--muted)}",
		".row{display:flex;gap:12px;flex-wrap:wrap}",
		".button{padding:6px 12px;border-radius:8px;border:1px solid var(--border);background:rgba(255,255,255,0.02);color:var(--text);cursor:pointer}",
		".button.primary{border-color:rgba(100,181,246,0.6);color:var(--accent)}",
		".button.danger{border-color:rgba(248,113,113,0.6);color:var(--danger)}",
		".split{display:flex;justify-content:space-between;gap:12px;align-items:center}",
		".list{display:flex;flex-direction:column;gap:10px}",
		".divider{height:1px;background:var(--border);margin:16px 0}",
		".empty{padding:18px;border:1px dashed var(--border);border-radius:12px;color:var(--muted);text-align:center}",
		".nowrap{white-space:nowrap}",
		".scroll{overflow:auto}",
		".code{font-family:'IBM Plex Mono','Fira Code',monospace;font-size:12px;background:rgba(255,255,255,0.04);padding:2px 6px;border-radius:6px;border:1px solid var(--border)}",
		"@media (max-width:720px){.topbar{flex-direction:column;align-items:flex-start;gap:12px}.container{padding:20px}.nav{flex-wrap:wrap}}",
	}, "")
}
