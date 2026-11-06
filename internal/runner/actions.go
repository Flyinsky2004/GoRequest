package runner

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	mrand "math/rand"

	"gorequest/internal/core"
)

func validActionName(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n{}.") {
		return false
	}
	return !core.IsReservedNamespace(s)
}

func validateActions(actions []Action, where string) error {
	for i := range actions {
		a := actions[i]
		n := 0
		if a.Set != nil {
			n++
		}
		if a.Generate != nil {
			n++
		}
		if n != 1 {
			return fmt.Errorf("%s action %d must set exactly one of set/generate", where, i+1)
		}
		if a.Set != nil {
			if !validActionName(a.Set.Name) {
				return fmt.Errorf("%s action %d: invalid variable name %q", where, i+1, a.Set.Name)
			}
		}
		if a.Generate != nil {
			g := a.Generate
			if !validActionName(g.Name) {
				return fmt.Errorf("%s action %d: invalid variable name %q", where, i+1, g.Name)
			}
			switch g.Kind {
			case "uuid", "timestamp", "timestampMs":
			case "randomString":
				if g.Length < 1 || g.Length > 4096 {
					return fmt.Errorf("%s action %d: randomString length must be 1..4096", where, i+1)
				}
			case "randomInt":
				if g.Min > g.Max {
					return fmt.Errorf("%s action %d: randomInt min must be <= max", where, i+1)
				}
			default:
				return fmt.Errorf("%s action %d: unsupported generate kind %q", where, i+1, g.Kind)
			}
		}
	}
	return nil
}

// rowGenerator is per-row, so concurrent rows never share random state and a
// fixed seed reproduces the same dynamic values regardless of scheduling.
type rowGenerator struct {
	mu  sync.Mutex
	rng *mrand.Rand
}

func newRowGenerator(seed int64) *rowGenerator {
	return &rowGenerator{rng: mrand.New(mrand.NewSource(seed))}
}

func (g *rowGenerator) bytes(n int) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.rng.Intn(256))
	}
	return b
}

func (g *rowGenerator) intn(n int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rng.Intn(n)
}

func (g *rowGenerator) uuid() string {
	b := g.bytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}

const randomAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func (g *rowGenerator) randomString(n int) string {
	if n <= 0 {
		n = 16
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = randomAlphabet[g.intn(len(randomAlphabet))]
	}
	return string(out)
}

func (g *rowGenerator) randomInt(min, max int) int {
	if min > max {
		return min
	}
	span := max - min + 1
	if span <= 0 { // overflow guard
		return min
	}
	return min + g.intn(span)
}

// executeActions applies declarative actions to working in place. Values
// resolved from secrets are rejected: ordinary run variables must not become a
// plaintext transport for credential material.
func executeActions(actions []Action, working map[string]string, data map[string]string,
	outputs map[string]core.StepOutput, secrets core.SecretProvider, gen *rowGenerator,
	clock func() time.Time) error {
	if len(actions) == 0 {
		return nil
	}
	if clock == nil {
		clock = time.Now
	}
	r := core.NewResolver(working, secrets).WithDataVars(data).WithStepOutputs(outputs)
	for i := range actions {
		a := actions[i]
		switch {
		case a.Set != nil:
			tv, err := r.ResolveTracked(a.Set.Value)
			if err != nil {
				return fmt.Errorf("action set %s: %w", a.Set.Name, err)
			}
			if tv.Sensitive {
				return fmt.Errorf("action set %s: refusing to copy a secret value into a plain run variable", a.Set.Name)
			}
			working[a.Set.Name] = tv.Value
		case a.Generate != nil:
			v, err := generateValue(a.Generate, gen, clock)
			if err != nil {
				return err
			}
			working[a.Generate.Name] = v
		default:
			return fmt.Errorf("action %d has no operation", i+1)
		}
	}
	return nil
}

func generateValue(g *GenerateAction, gen *rowGenerator, clock func() time.Time) (string, error) {
	switch g.Kind {
	case "uuid":
		return gen.uuid(), nil
	case "timestamp":
		return clock().UTC().Format(time.RFC3339Nano), nil
	case "timestampMs":
		return fmt.Sprintf("%d", clock().UnixMilli()), nil
	case "randomString":
		return gen.randomString(g.Length), nil
	case "randomInt":
		return fmt.Sprintf("%d", gen.randomInt(g.Min, g.Max)), nil
	default:
		return "", fmt.Errorf("unsupported generate kind %q", g.Kind)
	}
}
