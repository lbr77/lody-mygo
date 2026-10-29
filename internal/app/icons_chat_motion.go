package app

import "github.com/egoist/mygo/ui"

// iconInProgress is Carbon's in-progress mark, which Lody's plan bar puts
// before a step under way: a ring with its left half filled to the
// center and a quarter swept from it.
var iconInProgress = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path fill="currentColor" d="M16 2a14 14 0 1 0 14 14A14.016 14.016 0 0 0 16 2m0 26a12 12 0 0 1 0-24v12l8.481 8.481A11.96 11.96 0 0 1 16 28"/></svg>`))
