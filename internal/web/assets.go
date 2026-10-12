package web

import "embed"

// Assets are embedded in the single control binary so the tailnet-only UI has
// no runtime dependency on a separate frontend server.
//
//go:embed assets/app.css assets/*.js assets/install-oci.py assets/icons/*.svg assets/brand/*.png
var Assets embed.FS
