// Package builtin pulls in the apps that ship with pixel-fleet by default.
//
// Importing this package (typically with a blank import from main.go)
// triggers the per-app init() functions, which register each built-in
// app with the apps registry.
//
// Out-of-tree apps follow the same pattern: write a package whose init()
// calls apps.Register, then blank-import it alongside this one.
package builtin

import (
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/appviewer"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/claude"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/codex"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/skillsviewer"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/terminal"
)
