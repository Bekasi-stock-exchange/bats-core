package api

import (
	_ "embed"
	"net/http"
)

// openapiSpec is the OpenAPI 3.0 document, embedded at build time so the binary
// is self-contained (no runtime file dependency). Served at /openapi.yaml.
//
//go:embed openapi.yaml
var openapiSpec []byte

// swaggerUIPage is a minimal Swagger UI host page. The swagger-ui assets are
// loaded from a CDN; the page itself points the viewer at /openapi.yaml. This
// keeps the Go module free of any Swagger dependency.
const swaggerUIPage = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>JAST Core API — Swagger UI</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
      });
    };
  </script>
</body>
</html>`

// handleDocs serves the Swagger UI host page at GET /docs.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIPage))
}

// handleOpenAPISpec serves the embedded OpenAPI document at GET /openapi.yaml.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openapiSpec)
}
