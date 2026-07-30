// Command gendocs regenerates the OpenAPI document from the code annotations.
//
//	go run ./cmd/gendocs
//
// It runs swag, then retags the result from OpenAPI 3.1 to 3.0.3.
//
// The retag is necessary because swag emits either Swagger 2.0 or OpenAPI 3.1 —
// it has no 3.0 option — while the Swagger UI bundled by swaggo/files cannot
// render 3.1 and fails with "does not specify a valid version field". Our document
// uses no 3.1-only construct, so the two versions describe it identically. The
// guard below aborts if that ever stops being true, rather than shipping a spec
// that lies about its own version.
//
// This is a Go program rather than a Makefile recipe so it runs on any machine
// with a Go toolchain, with no make, bash, or sed required.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
)

const (
	outputDir  = "platform/docs"
	fromYAML   = "openapi: 3.1.0"
	toYAML     = "openapi: 3.0.3"
	fromJSON   = `"openapi": "3.1.0"`
	toJSON     = `"openapi": "3.0.3"`
	swagVerion = "v2.0.0-rc5"
)

// only31 matches constructs that exist in OpenAPI 3.1 but not in 3.0. If the
// generated document ever contains one, retagging it as 3.0 would be a lie.
var only31 = regexp.MustCompile(`(?m)^[\t ]*(const|contentMediaType|\$schema|webhooks|examples):`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run() error {
	swag, err := findSwag()
	if err != nil {
		return err
	}

	cmd := exec.Command(swag, "init", "--v3.1",
		"-g", "main.go", "-d", "./", "-o", outputDir, "--ot", "yaml,json")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("swag init: %w", err)
	}

	yamlPath := filepath.Join(outputDir, "swagger.yaml")
	jsonPath := filepath.Join(outputDir, "swagger.json")

	spec, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}
	if loc := only31.FindIndex(spec); loc != nil {
		return fmt.Errorf(
			"generated spec uses an OpenAPI 3.1-only construct (%q); retagging to 3.0 is unsafe.\n"+
				"Either drop the construct, or serve a UI that understands 3.1",
			bytes.TrimSpace(spec[loc[0]:loc[1]]))
	}

	if err := retag(yamlPath, fromYAML, toYAML); err != nil {
		return err
	}
	if err := retag(jsonPath, fromJSON, toJSON); err != nil {
		return err
	}
	if err := fixSecuritySchemes(yamlPath, jsonPath); err != nil {
		return err
	}

	fmt.Println("gendocs: wrote", yamlPath, "and", jsonPath, "as OpenAPI 3.0.3")
	return nil
}

// Security schemes, rewritten after generation to work around a swag bug.
//
// swag v2.0.0-rc5 cannot emit two @securityDefinitions blocks: the second block's
// @in/@name overwrite the first, and only one scheme survives — under the first
// block's name but carrying the second block's header. Reproduced in isolation, so
// it is the generator, not the annotations.
//
// The per-route "security:" references are correct, because those come from
// @Security on each handler. Only the securitySchemes block needs repairing, which
// is what these do. Delete this once swag emits both schemes.
const (
	adminScheme       = "ApiKeyAuth"
	adminHeader       = "X-API-Key"
	participantScheme = "ParticipantKeyAuth"
	participantHeader = "X-Participant-Key"
)

var (
	brokenSchemesYAML = "  securitySchemes:\n    " + adminScheme + ":\n" +
		"      in: header\n      name: " + participantHeader + "\n      type: apiKey\n"

	fixedSchemesYAML = "  securitySchemes:\n    " + adminScheme + ":\n" +
		"      description: Static administrative key, from configuration. Guards every /api/admin route.\n" +
		"      in: header\n      name: " + adminHeader + "\n      type: apiKey\n" +
		"    " + participantScheme + ":\n" +
		"      description: Per-broker key, issued by an admin and stored hashed. Guards every /api/participant route. Returned once at issue and never retrievable again.\n" +
		"      in: header\n      name: " + participantHeader + "\n      type: apiKey\n"

	adminDescription = "Static administrative key, from configuration. " +
		"Guards every /api/admin route."
	participantDescription = "Per-broker key, issued by an admin and stored hashed. " +
		"Guards every /api/participant route. Returned once at issue and never retrievable again."
)

// fixSecuritySchemes replaces the single mangled scheme with both real ones.
//
// The YAML is patched textually and the JSON structurally — the JSON is indented,
// so matching it as text would be brittle against a formatting change.
//
// Both fail loudly if the expected broken shape is absent: that means swag changed
// its output, possibly because the bug was fixed, and silently skipping would ship
// a spec whose admin routes advertise the wrong header.
func fixSecuritySchemes(yamlPath, jsonPath string) error {
	if err := replaceExact(yamlPath, brokenSchemesYAML, fixedSchemesYAML); err != nil {
		return err
	}
	return fixSchemesJSON(jsonPath)
}

// fixSchemesJSON rewrites components.securitySchemes in the generated JSON.
func fixSchemesJSON(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var doc map[string]any
	if err := json.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	components, _ := doc["components"].(map[string]any)
	if components == nil {
		return fmt.Errorf("%s: no components object", path)
	}
	schemes, _ := components["securitySchemes"].(map[string]any)
	if len(schemes) != 1 {
		return fmt.Errorf("%s: expected exactly one mangled security scheme, found %d.\n"+
			"swag's output changed — if it now emits both schemes, delete the\n"+
			"fixSecuritySchemes workaround in cmd/gendocs", path, len(schemes))
	}

	components["securitySchemes"] = map[string]any{
		adminScheme: map[string]any{
			"type": "apiKey", "in": "header",
			"name": adminHeader, "description": adminDescription,
		},
		participantScheme: map[string]any{
			"type": "apiKey", "in": "header",
			"name": participantHeader, "description": participantDescription,
		},
	}

	// Match swag's own formatting so the file stays diff-stable between runs.
	out, err := json.MarshalIndent(doc, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// replaceExact swaps one exact occurrence of from for to.
func replaceExact(path, from, to string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(content, []byte(from)) {
		return fmt.Errorf("%s: expected security-scheme text not found.\n"+
			"swag's output changed — if it now emits both schemes, delete the\n"+
			"fixSecuritySchemes workaround in cmd/gendocs", path)
	}
	return os.WriteFile(path, bytes.Replace(content, []byte(from), []byte(to), 1), 0o644)
}

// retag rewrites the version field of a generated file in place.
func retag(path, from, to string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(content, []byte(from)) {
		return fmt.Errorf("%s: expected to find %q — did swag change its output?", path, from)
	}
	updated := bytes.Replace(content, []byte(from), []byte(to), 1)
	return os.WriteFile(path, updated, 0o644)
}

// findSwag locates the swag CLI on PATH, falling back to GOPATH/bin where
// `go install` puts it.
func findSwag() (string, error) {
	if path, err := exec.LookPath("swag"); err == nil {
		return path, nil
	}

	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err == nil {
		name := "swag"
		if runtime.GOOS == "windows" {
			name = "swag.exe"
		}
		candidate := filepath.Join(string(bytes.TrimSpace(out)), "bin", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("swag not found. Install it with:\n"+
		"    go install github.com/swaggo/swag/v2/cmd/swag@%s", swagVerion)
}
