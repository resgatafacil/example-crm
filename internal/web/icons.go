package web

import (
	"bytes"
	"encoding/json"
	"html/template"
)

var iconPaths = map[string]string{
	"home":     `<path d="M3 11l9-8 9 8"/><path d="M5 10v10h14V10"/>`,
	"board":    `<rect x="3" y="4" width="5" height="16" rx="1.5"/><rect x="10" y="4" width="5" height="10" rx="1.5"/><rect x="17" y="4" width="4" height="13" rx="1.5"/>`,
	"users":    `<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20c0-3.6 2.9-6 6.5-6s6.5 2.4 6.5 6"/><path d="M16 4.5a3.5 3.5 0 010 7"/><path d="M18 14.3c2 .8 3.5 2.6 3.5 5.7"/>`,
	"plug":     `<path d="M9 3v5M15 3v5"/><path d="M6 8h12v3a6 6 0 01-12 0V8z"/><path d="M12 17v4"/>`,
	"bolt":     `<path d="M13 2L4 14h7l-1 8 9-12h-7l1-8z"/>`,
	"flask":    `<path d="M9 3h6"/><path d="M10 3v6L4.5 19a1.5 1.5 0 001.3 2.2h12.4a1.5 1.5 0 001.3-2.2L14 9V3"/><path d="M7.5 15h9"/>`,
	"key":      `<circle cx="8" cy="15" r="4"/><path d="M11 12l9-9"/><path d="M16 7l3 3"/>`,
	"send":     `<path d="M22 2L11 13"/><path d="M22 2l-7 20-4-9-9-4 20-7z"/>`,
	"check":    `<path d="M20 6L9 17l-5-5"/>`,
	"alert":    `<path d="M12 3l10 18H2L12 3z"/><path d="M12 10v5"/><path d="M12 18v.5"/>`,
	"info":     `<circle cx="12" cy="12" r="9"/><path d="M12 11v6"/><path d="M12 7.5v.5"/>`,
	"copy":     `<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 00-2-2H6a2 2 0 00-2 2v8a2 2 0 002 2h2"/>`,
	"external": `<path d="M14 4h6v6"/><path d="M20 4l-9 9"/><path d="M18 14v4a2 2 0 01-2 2H6a2 2 0 01-2-2V8a2 2 0 012-2h4"/>`,
	"left":     `<path d="M15 5l-7 7 7 7"/>`,
	"right":    `<path d="M9 5l7 7-7 7"/>`,
	"plus":     `<path d="M12 5v14M5 12h14"/>`,
	"refresh":  `<path d="M20 11a8 8 0 00-14.5-4.5L4 8"/><path d="M4 4v4h4"/><path d="M4 13a8 8 0 0014.5 4.5L20 16"/><path d="M20 20v-4h-4"/>`,
	"phone":    `<path d="M5 4h4l2 5-2.5 1.5a11 11 0 005 5L15 13l5 2v4a2 2 0 01-2 2A16 16 0 013 6a2 2 0 012-2z"/>`,
	"mail":     `<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>`,
	"note":     `<path d="M5 3h10l4 4v14H5z"/><path d="M15 3v4h4"/><path d="M8 12h8M8 16h5"/>`,
	"ban":      `<circle cx="12" cy="12" r="9"/><path d="M5.6 5.6l12.8 12.8"/>`,
	"inbox":    `<path d="M3 13l3-8h12l3 8"/><path d="M3 13v6h18v-6h-5a4 4 0 01-8 0H3z"/>`,
	"edit":     `<path d="M4 20h4L19 9l-4-4L4 16v4z"/><path d="M13.5 6.5l4 4"/>`,
	"trash":    `<path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 13h10l1-13"/>`,
	"menu":     `<path d="M4 7h16M4 12h16M4 17h16"/>`,
	"close":    `<path d="M6 6l12 12M18 6L6 18"/>`,
	"link":     `<path d="M10 14a4 4 0 005.7 0l3-3a4 4 0 00-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 00-5.7 0l-3 3a4 4 0 005.7 5.7l1-1"/>`,
	"funnel":   `<path d="M3 4h18l-7 8.5V19l-4 2v-8.5L3 4z"/>`,
}

func icon(name string) template.HTML {
	paths, ok := iconPaths[name]
	if !ok {
		return ""
	}
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">` + paths + `</svg>`)
}

func prettyJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}
