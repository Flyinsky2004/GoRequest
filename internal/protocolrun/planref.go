package protocolrun

// This file binds a saved plan file to a request without copying the plan body
// or any resolved secret into the shared project YAML. It mirrors
// core.GraphQLSchemaExtensionKey: the request carries only a project-relative
// path plus the digest of the plan bytes the user last reviewed.
//
// The reference is what lets an execution port find the plan for a WebSocket
// request, because the runner's protocol request carries the request entry but
// no plan-file path and no variable map. See docs/task-results/F02-integration.patch.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"gorequest/internal/core"
)

// PlanExtensionKey is the request extension key that binds a saved plan file.
const PlanExtensionKey = "wsPlan"

// PlanRef is the persisted, secret-free plan reference.
type PlanRef struct {
	Path   string `json:"path" yaml:"path"`
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
}

// PlanRefFromRequest reads the plan reference from the request's extension bag.
// It never opens the file or resolves the path.
func PlanRefFromRequest(req *core.Request) (PlanRef, bool) {
	if req == nil || req.Extensions == nil {
		return PlanRef{}, false
	}
	raw, ok := req.Extensions[PlanExtensionKey]
	if !ok || raw == nil {
		return PlanRef{}, false
	}
	switch v := raw.(type) {
	case PlanRef:
		ref := normalizePlanRef(v)
		return ref, ref.Path != ""
	case *PlanRef:
		if v == nil {
			return PlanRef{}, false
		}
		ref := normalizePlanRef(*v)
		return ref, ref.Path != ""
	case map[string]any:
		ref := normalizePlanRef(PlanRef{Path: planString(v["path"]), Digest: planString(v["digest"])})
		return ref, ref.Path != ""
	case map[any]any:
		ref := normalizePlanRef(PlanRef{Path: planString(v["path"]), Digest: planString(v["digest"])})
		return ref, ref.Path != ""
	case string:
		ref := normalizePlanRef(PlanRef{Path: v})
		return ref, ref.Path != ""
	default:
		return PlanRef{}, false
	}
}

// SetPlanRef stores (or clears, for an empty path) the plan reference in the
// request extension bag.
func SetPlanRef(req *core.Request, ref PlanRef) {
	if req == nil {
		return
	}
	ref = normalizePlanRef(ref)
	if ref.Path == "" {
		if req.Extensions != nil {
			delete(req.Extensions, PlanExtensionKey)
		}
		return
	}
	if req.Extensions == nil {
		req.Extensions = map[string]any{}
	}
	req.Extensions[PlanExtensionKey] = map[string]any{
		"path":   ref.Path,
		"digest": ref.Digest,
	}
}

func normalizePlanRef(ref PlanRef) PlanRef {
	ref.Path = strings.TrimSpace(ref.Path)
	ref.Digest = strings.TrimSpace(ref.Digest)
	return ref
}

func planString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// LoadPlanFile reads, validates and digests a plan file in one bounded pass. The
// digest covers the exact bytes that were decoded, so it can be compared with a
// stored PlanRef.Digest to detect a plan edited after it was reviewed.
func LoadPlanFile(path string) (*Plan, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("open plan %q: %w", path, err)
	}
	if info.Size() > MaxPlanBytes {
		return nil, "", fmt.Errorf("plan %q exceeds %d bytes", path, MaxPlanBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open plan %q: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxPlanBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read plan %q: %w", path, err)
	}
	if len(data) > MaxPlanBytes {
		return nil, "", fmt.Errorf("plan %q exceeds %d bytes", path, MaxPlanBytes)
	}
	p, err := DecodePlan(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	return p, core.Digest(data), nil
}

// ReadPlanFromProject loads a project-relative plan through the supplied opener,
// enforcing ValidateProjectRelPath and MaxPlanBytes before the whole body is
// retained. It performs no network I/O, so it is safe during preflight.
func ReadPlanFromProject(ctx context.Context, opener core.ProjectFileOpener, rel string) (*Plan, string, error) {
	clean, err := core.ValidateProjectRelPath(rel)
	if err != nil {
		return nil, "", err
	}
	if opener == nil {
		return nil, "", &core.ConfigError{Err: fmt.Errorf("no project file opener for plan %q", clean)}
	}
	opened, err := opener.OpenProjectFile(ctx, clean)
	if err != nil {
		return nil, "", fmt.Errorf("open plan %q: %w", clean, err)
	}
	if opened == nil || opened.Reader == nil {
		return nil, "", &core.ConfigError{Err: fmt.Errorf("plan %q could not be opened", clean)}
	}
	if opened.Size > MaxPlanBytes {
		return nil, "", fmt.Errorf("plan %q exceeds %d bytes", clean, MaxPlanBytes)
	}
	data, err := io.ReadAll(io.LimitReader(opened.Reader, MaxPlanBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read plan %q: %w", clean, err)
	}
	if len(data) > MaxPlanBytes {
		return nil, "", fmt.Errorf("plan %q exceeds %d bytes", clean, MaxPlanBytes)
	}
	p, err := DecodePlan(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	return p, core.Digest(data), nil
}
