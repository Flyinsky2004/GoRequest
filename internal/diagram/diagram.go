package diagram

import (
	"context"
	"strings"
)

// Render parses source, lays it out and returns the geometry scene. It
// never returns a partial scene: any unsupported construct is a diagnostic
// with the source line and column, and the caller keeps the source.
func Render(ctx context.Context, source string, opts Options) (*Scene, *Diagnostic) {
	limits := opts.Limits.effective()
	if ctx == nil {
		ctx = context.Background()
	}
	if len(source) > limits.MaxSourceBytes {
		return nil, &Diagnostic{Message: "diagram source exceeds the 64 KiB limit; the source is shown unchanged"}
	}
	source = strings.TrimPrefix(source, "\xef\xbb\xbf")
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")

	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	lines := sourceLines(source)
	kind, d := detectKind(lines)
	if d != nil {
		return nil, d
	}
	switch kind {
	case KindFlowchart:
		fc, d := parseFlowchart(lines, limits)
		if d != nil {
			return nil, d
		}
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		return layoutFlowchart(ctx, fc, opts, limits)
	case KindSequence:
		sd, d := parseSequence(lines, limits)
		if d != nil {
			return nil, d
		}
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		return layoutSequence(ctx, sd, opts, limits)
	}
	return nil, &Diagnostic{Message: "unsupported diagram type; the offline renderer supports flowchart, graph and sequenceDiagram"}
}

func detectKind(lines []parsedLine) (Kind, *Diagnostic) {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if strings.HasPrefix(trimmed, "%%{") {
			return "", diagAt(line.num, 1, "Mermaid init directives are not supported by the offline renderer")
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch {
		case lower == "sequencediagram" || strings.HasPrefix(lower, "sequencediagram "):
			return KindSequence, nil
		case lower == "flowchart" || strings.HasPrefix(lower, "flowchart "),
			lower == "graph" || strings.HasPrefix(lower, "graph "):
			return KindFlowchart, nil
		}
		return "", diagAt(line.num, 1, "unsupported diagram type %q; the offline renderer supports flowchart, graph and sequenceDiagram", firstWord(trimmed))
	}
	return "", &Diagnostic{Message: "the diagram is empty"}
}
