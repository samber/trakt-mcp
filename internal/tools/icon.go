package tools

import (
	"encoding/base64"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// iconMIMEType is the media type of the embedded icon.
	iconMIMEType = "image/svg+xml"
	// iconSizeAny is the MCP size hint for scalable formats such as SVG.
	iconSizeAny = "any"
	// iconSVG is the same mark as site/public/favicon.svg: a navy film frame
	// with sprocket holes and an amber play triangle, plugged in by two amber
	// prongs on its right edge. Keep both in sync. It is inlined as a data URI
	// so clients show it without fetching a URL, which works for self-hosted
	// instances too.
	iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect x="1.5" y="4" width="23" height="24" rx="4" fill="#1d2b53"/><g fill="#ffffff"><rect x="5" y="6.5" width="3" height="2.5" rx=".6"/><rect x="11.5" y="6.5" width="3" height="2.5" rx=".6"/><rect x="18" y="6.5" width="3" height="2.5" rx=".6"/><rect x="5" y="23" width="3" height="2.5" rx=".6"/><rect x="11.5" y="23" width="3" height="2.5" rx=".6"/><rect x="18" y="23" width="3" height="2.5" rx=".6"/></g><path d="M10.2 12 17.6 16l-7.4 4z" fill="#f6a823" stroke="#f6a823" stroke-width="1.6" stroke-linejoin="round"/><rect x="24" y="10.5" width="6.5" height="3" rx="1.5" fill="#f6a823"/><rect x="24" y="18.5" width="6.5" height="3" rx="1.5" fill="#f6a823"/></svg>`
)

// serverIcons returns the icons advertised in the server's implementation info.
func serverIcons() []mcp.Icon {
	return []mcp.Icon{{
		Source:   "data:" + iconMIMEType + ";base64," + base64.StdEncoding.EncodeToString([]byte(iconSVG)),
		MIMEType: iconMIMEType,
		Sizes:    []string{iconSizeAny},
	}}
}
