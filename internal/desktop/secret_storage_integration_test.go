package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"gorequest/internal/platform"
)

func TestSecretStorageCorruptPreferenceDoesNotSelectAnotherBackend(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "preferences.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t)
	m.secretStore = platform.NewSecretStore(root, &memoryIdentityDriver{data: map[string]string{}})
	if err := m.openProject(writeProject(t, "Unreadable Storage Preference")); err == nil {
		t.Fatal("corrupt storage preference silently selected another backend")
	}
}

func TestSecretStorageEnumeratedFileNamesSurviveSwitchToKeychain(t *testing.T) {
	_, a, ps := openStorageProject(t, "Imported Local Secrets", t.TempDir(), newMemoryIdentityDriver())
	if ps.secretKind != platform.SecretBackendFile {
		t.Fatalf("default backend = %q, want %q", ps.secretKind, platform.SecretBackendFile)
	}
	// A file may contain entries that did not come through the management UI.
	if err := ps.secretBackend.Set("importedSecret", "example-value"); err != nil {
		t.Fatal(err)
	}
	switchBackend(t, a, ps, platform.SecretBackendKeychain)
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := mc.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "importedSecret" {
			return
		}
	}
	t.Fatal("file-only secret disappeared from the managed inventory after switching")
}
