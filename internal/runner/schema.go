package runner

// SchemaRef identifies the schema assertion target. A03 owns the concrete
// OpenAPI/JSON-Schema implementation; the runner only consumes this port.
type SchemaRef struct {
	// Ref is a project-relative schema/operation reference such as
	// "openapi.yaml#/components/schemas/User" or an operation identifier. The
	// runner treats it as opaque.
	Ref string
	// Operation optionally names an OpenAPI operation whose response schema
	// should be used instead of Ref.
	Operation string
	// Status is the observed HTTP status, allowing a validator to pick the
	// matching response definition.
	Status int
	// Headers carries actual response media type; additive A02 contract field.
	Headers [][2]string
}

// SchemaIssue is one validation finding. InstancePath uses JSONPath notation
// ($.user.id) so the desktop can point at the failing element.
type SchemaIssue struct {
	InstancePath string `json:"instancePath,omitempty"`
	Message      string `json:"message"`
	Expected     string `json:"expected,omitempty"`
	Actual       string `json:"actual,omitempty"`
}

// SchemaValidator validates a response body against a schema or operation.
// Returning an error means validation could not run (for example an unreadable
// schema); that is classified as a config error and is never retried.
type SchemaValidator interface {
	ValidateSchema(instance []byte, ref SchemaRef) ([]SchemaIssue, error)
}

// SchemaPreflighter is an optional interface: when a validator implements it,
// the runner verifies every schema reference before the first request is sent.
type SchemaPreflighter interface {
	PreflightSchema(ref SchemaRef) error
}

// secretEnumerator is an optional interface implemented by local secret
// providers that can list bound names without returning values. The runner
// uses it to pre-register every bound secret before a report is written.
type secretEnumerator interface {
	SecretNames() []string
}
