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
	// iconSVG is the same contact-card icon as site/public/favicon.svg. Keep
	// both in sync. It is inlined as a data URI so clients show it without
	// fetching a URL, which works for self-hosted instances too.
	iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect x="3" y="6" width="26" height="20" rx="2" fill="#ffffff" stroke="#b4cbe3" stroke-width="1.5"/><rect x="3" y="11" width="26" height="2" fill="#d33f49"/><rect x="7" y="17" width="14" height="1.6" fill="#b4cbe3"/><rect x="7" y="21" width="10" height="1.6" fill="#b4cbe3"/></svg>`
)

// serverIcons returns the icons advertised in the server's implementation info.
func serverIcons() []mcp.Icon {
	return []mcp.Icon{{
		Source:   "data:" + iconMIMEType + ";base64," + base64.StdEncoding.EncodeToString([]byte(iconSVG)),
		MIMEType: iconMIMEType,
		Sizes:    []string{iconSizeAny},
	}}
}
