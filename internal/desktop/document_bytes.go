package desktop

import "bytes"

// documentBytesForSave restores the buffer's original line endings. String()
// is normalized for editor offsets; WriteTo is the persistence representation.
func documentBytesForSave(tab *Tab) []byte {
	if tab == nil || tab.DocEditor == nil || tab.DocEditor.Buffer == nil {
		return nil
	}
	var out bytes.Buffer
	_, _ = tab.DocEditor.Buffer.WriteTo(&out) // bytes.Buffer writes cannot fail.
	return out.Bytes()
}
