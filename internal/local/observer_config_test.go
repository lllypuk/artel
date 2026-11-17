package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestObserverConfigAtomicConfirmation(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "private", "config.json")
	tokenPath := path + ".token"
	token, e := InitToken(tokenPath)
	if e != nil {
		t.Fatal(e)
	}
	c := ObserverConfig{Status: "pending", ObserverID: uuid.NewString(), DatabaseInstanceID: uuid.NewString(), ObserverSocket: "/tmp/fictional.sock", TokenFile: tokenPath, ProvisioningKey: uuid.NewString(), Payload: ObserverProvision{Label: "Fictional", Verifier: TokenVerifier(token)}}
	if e = WriteObserverConfig(path, c, false); e != nil {
		t.Fatal(e)
	}
	if e = WriteObserverConfig(path, c, false); e == nil {
		t.Fatal("config overwritten")
	}
	if _, e = ReadObserverConfig(path, true); e == nil {
		t.Fatal("pending accepted")
	}
	changed := c
	changed.Status = "confirmed"
	changed.Payload.Label = "Changed"
	if e = WriteObserverConfig(path, changed, true); e == nil {
		t.Fatal("canonical payload changed")
	}
	c.Status = "confirmed"
	if e = WriteObserverConfig(path, c, true); e != nil {
		t.Fatal(e)
	}
	got, e := ReadObserverConfig(path, true)
	if e != nil || got != c {
		t.Fatal("confirmed config invalid", e)
	}
	c.Payload.Verifier = strings.Repeat("ff", 32)
	if e = WriteObserverConfig(path, c, true); e == nil {
		t.Fatal("verifier changed")
	}
	link := filepath.Join(filepath.Dir(path), "link.json")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadObserverConfig(link, true); e == nil {
		t.Fatal("config symlink accepted")
	}
	if e = os.WriteFile(tokenPath, []byte(strings.Repeat("ef", 32)+"\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadObserverConfig(path, true); e == nil {
		t.Fatal("token mismatch accepted")
	}
}
