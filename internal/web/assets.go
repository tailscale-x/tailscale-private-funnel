package web

import "embed"

// Assets are embedded in the single control binary so the tailnet-only UI has
// no runtime dependency on a separate frontend server.
//
//go:embed assets/control/* assets/api/* assets/app.css assets/icons/*.svg assets/brand/*.png
var Assets embed.FS
