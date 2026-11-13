// Command dock is a terminal workspace that hosts plugins: native Go
// plugins that live in tabs and tiled panes, and external programs that
// take over the terminal while they run.
//
// This plain build includes no native plugins. `dock plugin install`
// builds a dock that includes them (see internal/build).
package main

import "github.com/dewald-L/dock/cli"

func main() { cli.Main() }
