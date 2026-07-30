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
	if err := fixRequestBodySchemas(yamlPath, jsonPath); err != nil {
		return err
	}

	fmt.Println("gendocs: wrote", yamlPath, "and", jsonPath, "as OpenAPI 3.0.3")
	return nil
}

// Security schemes, rewritten after generation to work around a swag bug.
//
// swag v2.0.0-rc5 sometimes cannot emit two @securityDefinitions blocks: the
// second block's @in/@name overwrite the first, and only one scheme survives —
// under the first block's name but carrying the second block's header.
// Reproduced in isolation, so it is the generator, not the annotations. It does
// not reproduce on every run, seemingly depending on annotation scan order.
//
// The per-route "security:" references are correct, because those come from
// @Security on each handler. Only the securitySchemes block needs repairing,
// which is what these do.
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
// swag rc5 has been observed to hit the bug on some runs and emit both schemes
// correctly on others — seemingly depending on annotation scan order — so this
// is not a one-time fix to delete once "the bug is gone". Both patchers are
// idempotent: if the document already has both schemes correct, they leave it
// untouched. They fail loudly only when the document matches neither the known
// broken shape nor the known correct one, which means swag emitted a third
// shape this workaround does not understand yet.
func fixSecuritySchemes(yamlPath, jsonPath string) error {
	if err := fixSchemesYAML(yamlPath); err != nil {
		return err
	}
	return fixSchemesJSON(jsonPath)
}

func fixSchemesYAML(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if bytes.Contains(content, []byte(fixedSchemesYAML)) {
		return nil // already correct
	}
	if !bytes.Contains(content, []byte(brokenSchemesYAML)) {
		return fmt.Errorf("%s: security schemes match neither the known-broken nor the known-correct shape.\n"+
			"swag's output changed — inspect the securitySchemes block and update cmd/gendocs", path)
	}
	fixed := bytes.Replace(content, []byte(brokenSchemesYAML), []byte(fixedSchemesYAML), 1)
	return os.WriteFile(path, fixed, 0o644)
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

	if schemesAlreadyCorrect(schemes) {
		return nil
	}
	if len(schemes) != 1 {
		return fmt.Errorf("%s: security schemes match neither the known-broken nor the known-correct shape.\n"+
			"swag's output changed — inspect components.securitySchemes and update cmd/gendocs", path)
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

// schemesAlreadyCorrect reports whether both schemes are already present with
// their correct header names, so a run following a successful one is a no-op.
func schemesAlreadyCorrect(schemes map[string]any) bool {
	if len(schemes) != 2 {
		return false
	}
	admin, ok := schemes[adminScheme].(map[string]any)
	if !ok || admin["name"] != adminHeader {
		return false
	}
	participant, ok := schemes[participantScheme].(map[string]any)
	if !ok || participant["name"] != participantHeader {
		return false
	}
	return true
}

// requestBodyOneOf matches the shape swag emits for every @Param body body
// annotation: a oneOf of a bare empty object and the real schema. Swagger UI's
// "Try it out" picks the first oneOf branch to seed its example, which is the
// empty object — so every request body shows "{}" instead of the schema's
// example values. Collapsing it to a plain $ref (with its sibling description
// and summary) fixes the example without changing what the schema accepts,
// since a bare "type: object" already permits anything the real schema does.
var requestBodyOneOf = regexp.MustCompile(`(?m)^([\t ]*)oneOf:\n` +
	`[\t ]*- type: object\n` +
	`[\t ]*- (\$ref: '[^']+')\n` +
	`[\t ]*  (description: .*)\n[\t ]*  (summary: body)\n`)

// fixRequestBodySchemas collapses swag's oneOf[empty object, real schema]
// request body wrapper down to the real schema, in both generated files.
func fixRequestBodySchemas(yamlPath, jsonPath string) error {
	if err := fixRequestBodyYAML(yamlPath); err != nil {
		return err
	}
	return fixRequestBodyJSON(jsonPath)
}

func fixRequestBodyYAML(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	fixed := requestBodyOneOf.ReplaceAllFunc(content, func(m []byte) []byte {
		sub := requestBodyOneOf.FindSubmatch(m)
		indent, ref, description, summary := sub[1], sub[2], sub[3], sub[4]
		line := string(indent)
		return []byte(line + string(ref) + "\n" + line + string(description) + "\n" + line + string(summary) + "\n")
	})
	if bytes.Equal(fixed, content) {
		return fmt.Errorf("%s: expected at least one request-body oneOf wrapper, found none.\n"+
			"swag's output changed — if it no longer wraps request bodies in oneOf, delete the\n"+
			"fixRequestBodySchemas workaround in cmd/gendocs", path)
	}
	return os.WriteFile(path, fixed, 0o644)
}

// fixRequestBodyJSON walks every requestBody in the document and replaces the
// oneOf[empty object, real schema] wrapper with the real schema directly.
func fixRequestBodyJSON(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var doc map[string]any
	if err := json.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	fixed := 0
	paths, _ := doc["paths"].(map[string]any)
	for _, item := range paths {
		methods, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, op := range methods {
			opMap, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if simplifyRequestBody(opMap) {
				fixed++
			}
		}
	}

	if fixed == 0 {
		return fmt.Errorf("%s: expected at least one request-body oneOf wrapper, found none.\n"+
			"swag's output changed — if it no longer wraps request bodies in oneOf, delete the\n"+
			"fixRequestBodySchemas workaround in cmd/gendocs", path)
	}

	out, err := json.MarshalIndent(doc, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// simplifyRequestBody replaces one operation's requestBody schema if it
// matches the oneOf[empty object, real schema] wrapper. Reports whether it did.
func simplifyRequestBody(op map[string]any) bool {
	reqBody, ok := op["requestBody"].(map[string]any)
	if !ok {
		return false
	}
	content, ok := reqBody["content"].(map[string]any)
	if !ok {
		return false
	}
	body, ok := content["application/json"].(map[string]any)
	if !ok {
		return false
	}
	schema, ok := body["schema"].(map[string]any)
	if !ok {
		return false
	}
	oneOf, ok := schema["oneOf"].([]any)
	if !ok || len(oneOf) != 2 {
		return false
	}
	empty, ok := oneOf[0].(map[string]any)
	if !ok || empty["type"] != "object" || len(empty) != 1 {
		return false
	}
	real, ok := oneOf[1].(map[string]any)
	if !ok {
		return false
	}
	if _, hasRef := real["$ref"]; !hasRef {
		return false
	}

	delete(real, "description")
	delete(real, "summary")
	body["schema"] = real
	return true
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
