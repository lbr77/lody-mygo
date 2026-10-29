package sharing

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func history(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCaptureHistory(t *testing.T) {
	h := history(t, `[
		{"id":"u1","role":"user","userId":"x","read":true,"inputConfig":{"modelId":"m","inputBlocks":[{"type":"text","text":"hi"}]},"items":[{"type":"text","text":"see https://share.lody.ai/s/a#access=v1.`+strings.Repeat("ab", 32)+`"}]},
		{"id":"a1","role":"assistant","acpTurnId":"t","fileDiff":{},"items":[
			{"type":"system_notice","name":"task_proposal"},
			{"type":"terminal","id":"x"},{"type":"terminal_output","id":"y"},
			{"type":"tool_call","title":"ls -la","permissionRequest":{"id":"p"},"content":[{"type":"terminal_command","command":"ls -la"},{"type":"terminal","id":"t"}]},
			{"type":"tool_call","title":"Read file","content":[{"type":"terminal_command","command":"cat x"}]}
		]}
	]`)
	got, err := CaptureHistory(h)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	s := string(b)
	for _, gone := range []string{"userId", `"read"`, "modelId", "acpTurnId", "fileDiff", "task_proposal", `"terminal"`, "terminal_output", "permissionRequest", `"title":"ls -la"`, strings.Repeat("ab", 32)} {
		if strings.Contains(s, gone) {
			t.Errorf("%s kept in %s", gone, s)
		}
	}
	for _, kept := range []string{`"inputBlocks":[{"text":"hi","type":"text"}]`, "#access=omitted", `"title":"Read file"`, `"command":"ls -la"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("%s lost from %s", kept, s)
		}
	}
	if _, err := CaptureHistory(history(t, `[{"id":"a","role":"user"},{"id":"a","role":"user"}]`)); err == nil {
		t.Error("duplicate ids pass")
	}
	if _, err := project(history(t, `[{"id":"a","role":"assistant","items":[{"type":"system_notice","name":"task_proposal"}]}]`), false); err == nil {
		t.Error("readers accept task proposals")
	}
}

func TestPrepare(t *testing.T) {
	png := []byte("\x89PNG fake")
	reads := 0
	p, err := Prepare(context.Background(), Options{
		RootSourceID: "s1",
		Previous:     []SourceID{{"s2", "c1"}},
		CapturedAt:   time.Date(2026, 10, 29, 9, 12, 0, 0, time.UTC),
		Sources: []Source{
			{SourceID: "s1", Title: "Root", History: history(t, `[{"id":"u","role":"user","items":[{"type":"image","imageId":"img1","machineId":"m"},{"type":"file","fileId":"f","sourcePath":"/secret"}]}]`)},
			{SourceID: "s2", Title: "Child", ParentSourceID: "s1", SidePanel: true, History: history(t, `[{"id":"u","role":"user","items":[{"type":"image_group","images":[{"type":"image","imageId":"img1","storageSessionId":"s1","machineId":"m"}]}]}]`)},
		},
		ReadAttachment: func(ctx context.Context, a AttachmentSource) ([]byte, string, error) {
			reads++
			if a.Kind != "image" || a.Reference["imageId"] != "img1" || a.ConversationSourceID != "s1" {
				t.Errorf("read %+v", a)
			}
			return png, "image/png", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Errorf("the shared image was read %d times", reads)
	}
	m := p.Manifest
	if m.RootConversationID != "c2" || m.Conversations[0].ID != "c2" || m.Conversations[1].ID != "c1" {
		t.Errorf("ids %+v", m.Conversations)
	}
	if c := m.Conversations[1]; c.ParentConversationID != "c2" || c.ChildSessionPlacement != "side-panel" || c.HistoryObjectID != "h2" {
		t.Errorf("child %+v", c)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ID != "a1" || len(m.Objects) != 3 || m.Objects[0].ID != "a1" || m.CapturedAt != "2026-10-29T09:12:00.000Z" {
		t.Errorf("manifest %+v", m)
	}
	sum := sha256.Sum256(png)
	if m.Objects[0].SHA256 != hex.EncodeToString(sum[:]) || m.Objects[0].SizeBytes != len(png) {
		t.Errorf("object %+v", m.Objects[0])
	}
	h1 := string(p.Objects["h1"])
	if strings.Contains(h1, "/secret") || strings.Contains(h1, "machineId") || !strings.Contains(h1, `"imageId":"a1"`) || !strings.Contains(h1, "File attachment not included") {
		t.Errorf("history %s", h1)
	}
	if !strings.Contains(string(p.ManifestBytes), `"attachments":[{"id":"a1","kind":"image","fileName":"a1","objectId":"a1"}]`) {
		t.Errorf("manifest %s", p.ManifestBytes)
	}
	if p.SourceIDs[0] != (SourceID{"s1", "c2"}) || p.SourceIDs[1] != (SourceID{"s2", "c1"}) {
		t.Errorf("source ids %+v", p.SourceIDs)
	}
}

func TestCredentials(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	if h, _ := HashSecret(secret); h != "8681f0d9e6299e30d86b88c1647e6b60766355da64dd923867b5eae7c934298f" {
		t.Fatalf("hash %s", h)
	}
	if u, _ := URL("share_1", secret); u != "https://share.lody.ai/s/share_1#access=v1."+secret {
		t.Fatal(u)
	}
	if !IsSecret(NewSecret()) || IsSecret("xyz") {
		t.Fatal("secret form")
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	env, err := EncryptDelivery(hex.EncodeToString(der), "req-1", Origin, secret)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := hex.DecodeString(env.Ciphertext)
	plain, err := rsa.DecryptOAEP(sha256.New(), nil, key, ct, []byte(`["lody-share-delivery-v1","req-1","https://share.lody.ai"]`))
	if err != nil || string(plain) != secret || len(env.Ciphertext) != 512 {
		t.Fatalf("delivery %v %q", err, plain)
	}
}

func TestUpload(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	UploadOrigin = srv.URL
	secret := strings.Repeat("cd", 32)
	p, err := Prepare(context.Background(), Options{RootSourceID: "s", CapturedAt: time.Now(), Sources: []Source{{SourceID: "s", History: []any{}}}})
	if err != nil {
		t.Fatal(err)
	}
	var progress []int
	if err := Upload(context.Background(), nil, "dep1", secret, p, func(done, total int) { progress = append(progress, done) }); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "PUT /api/share-deployments/dep1/objects/h1 Bearer "+secret+" []" || !strings.HasPrefix(calls[1], "POST /api/share-deployments/dep1/seal Bearer "+secret+` {"formatVersion":1`) {
		t.Fatalf("calls %q", calls)
	}
	if len(progress) != 1 || progress[0] != 2 {
		t.Fatalf("progress %v", progress)
	}
}
