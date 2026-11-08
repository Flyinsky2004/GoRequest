package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorequest/internal/core"
)

type unreadableSwitchBackend struct {
	SecretBackend
	unreadable map[string]bool
}

func (b *unreadableSwitchBackend) Get(name string) (string, bool) {
	if b.unreadable[name] {
		return "", false
	}
	return b.SecretBackend.Get(name)
}

func (b *unreadableSwitchBackend) InspectSecret(name string) core.SecretStatus {
	if b.unreadable[name] {
		return core.SecretStatus{Name: name, State: core.SecretStateUnavailable}
	}
	return b.SecretBackend.InspectSecret(name)
}

func switchFileForTest(t *testing.T, values map[string]string) *FileSecrets {
	t.Helper()
	b, err := NewFileSecrets(filepath.Join(t.TempDir(), "store"), "test:scope")
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range values {
		if err := b.Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestSecretSwitchPlanRejectsUnreadableValues(t *testing.T) {
	for _, side := range []string{"source", "target"} {
		t.Run(side, func(t *testing.T) {
			source := SecretBackend(switchFileForTest(t, map[string]string{"token": "source-value"}))
			target := SecretBackend(switchFileForTest(t, map[string]string{"token": "target-value"}))
			if side == "source" {
				source = &unreadableSwitchBackend{source, map[string]bool{"token": true}}
			} else {
				target = &unreadableSwitchBackend{target, map[string]bool{"token": true}}
			}
			if _, err := BuildSecretSwitchPlan("test:scope", source, target, []string{"token"}, nil); err == nil {
				t.Fatal("unreadable value was treated as absent")
			}
		})
	}
}

func TestSecretSwitchReadFailureRollsBackEarlierCopies(t *testing.T) {
	source := &unreadableSwitchBackend{switchFileForTest(t, map[string]string{"alpha": "a", "beta": "b"}), map[string]bool{}}
	target := switchFileForTest(t, nil)
	plan, err := BuildSecretSwitchPlan("test:scope", source, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	source.unreadable["beta"] = true
	if _, err := ExecuteSecretSwitch(source, target, plan, ConflictOverwriteTarget); err == nil {
		t.Fatal("switch succeeded after a source read failed")
	}
	if _, ok := target.Get("alpha"); ok {
		t.Fatal("earlier copy was not rolled back")
	}
}

type partialWriteSwitchBackend struct {
	SecretBackend
	fail bool
}

func (b *partialWriteSwitchBackend) Set(name, value string) error {
	if err := b.SecretBackend.Set(name, value); err != nil {
		return err
	}
	if b.fail {
		b.fail = false
		return errors.New("write reported failure after persisting")
	}
	return nil
}

func TestSecretSwitchRestoresWriteThatReportsFailure(t *testing.T) {
	source := switchFileForTest(t, map[string]string{"token": "new-value"})
	target := &partialWriteSwitchBackend{switchFileForTest(t, map[string]string{"token": "old-value"}), true}
	plan, err := BuildSecretSwitchPlan("test:scope", source, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteSecretSwitch(source, target, plan, ConflictOverwriteTarget); err == nil {
		t.Fatal("expected injected write failure")
	}
	if got, _ := target.Get("token"); got != "old-value" {
		t.Fatal("failed write was not restored")
	}
}

func TestFileSecretsRejectsSymlinkedRootOnRead(t *testing.T) {
	original := switchFileForTest(t, map[string]string{"token": "example-value"})
	link := filepath.Join(t.TempDir(), "linked-store")
	if err := os.Symlink(original.root, link); err != nil {
		t.Fatal(err)
	}
	throughLink, err := NewFileSecrets(link, "test:scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := throughLink.Get("token"); ok {
		t.Fatal("read followed a symlinked storage root")
	}
}

func TestSecretSwitchRejectsStoreCorruptionAfterEmptyPlan(t *testing.T) {
	source := switchFileForTest(t, nil)
	target := switchFileForTest(t, nil)
	plan, err := BuildSecretSwitchPlan("test:scope", source, target, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source.Path(), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteSecretSwitch(source, target, plan, ConflictKeepTarget); err == nil {
		t.Fatal("empty switch plan ignored a now-corrupt source store")
	}
}

func TestSecretStoreRejectsInvalidPreferences(t *testing.T) {
	for _, contents := range []string{
		"{",
		`{"version":2,"scopes":{}}`,
		`{"version":1,"scopes":{"test:scope":"unknown"}}`,
	} {
		t.Run(contents, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "preferences.json"), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewSecretStore(root, newMemoryKeychainDriver())
			if _, err := store.SelectedKind("test:scope"); err == nil {
				t.Fatal("invalid preferences selected a backend instead of returning an error")
			}
		})
	}
}
