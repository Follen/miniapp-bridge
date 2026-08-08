package frida

import (
	_ "embed"
	"encoding/json"
	"miniapp-bridge/internal/version"
	"strings"
)

//go:embed hook.js
var AgentSource string

func SourceForConfig(c version.AddressConfig) string {
	b, _ := json.Marshal(c)
	return strings.Replace(AgentSource, "@@CONFIG@@", string(b), 1)
}
