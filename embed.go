// Package agentinbrowser embeds repository assets (agent skills) into the binary
// so `agent-in-browser skill install` works without a checkout.
package agentinbrowser

import "embed"

// Skills holds skills/<name>/SKILL.md and their references.
//
//go:embed skills
var Skills embed.FS
