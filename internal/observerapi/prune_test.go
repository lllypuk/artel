package observerapi_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestOwnerPruneExactSet(t *testing.T) {
	f := newFixture(t)
	a, b := reg(), reg()
	path := "/v1/observers/" + f.id
	key := uuid.NewString()
	prune := func(body any, status int) []byte {
		return f.request(t, false, "POST", path+"/prune", f.ownerToken, key, body, status)
	}
	payload := func(version int, ids ...string) map[string]any {
		return map[string]any{"expected_version": version, "expected_runtime_ids": ids}
	}
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", a, 200)
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", b, 200)
	for _, list := range []string{`null`, `[]`, `"wrong"`, `[42]`, `[null]`, `["junk"]`, `["` + uuid.Nil.String() + `"]`, `["ABCDEF00-1111-4111-8111-111111111111"]`, fmt.Sprintf(`[%q,%q]`, a.RuntimeID, a.RuntimeID)} {
		prune(`{"expected_version":1,"expected_runtime_ids":`+list+`}`, 400)
	}
	tooMany := make([]string, 9)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	prune(payload(1, tooMany...), 400)
	prune(`{"expected_version":1}`, 400)
	if got := prune(payload(1, a.RuntimeID), 409); !bytes.Contains(got, []byte(`"version_conflict"`)) {
		t.Fatal(string(got))
	}
	first := prune(payload(1, a.RuntimeID, b.RuntimeID), 200)
	if retry := prune(payload(1, b.RuntimeID, a.RuntimeID), 200); !bytes.Equal(first, retry) {
		t.Fatal("reordered set did not replay")
	}
	for _, body := range []map[string]any{payload(2, a.RuntimeID, b.RuntimeID), payload(1, a.RuntimeID)} {
		if got := prune(body, 409); !bytes.Contains(got, []byte(`"idempotency_conflict"`)) {
			t.Fatal(string(got))
		}
	}
	prune(`{"expected_version":1}`, 400)
	key = uuid.NewString()
	if got := prune(payload(2, a.RuntimeID), 409); !bytes.Contains(got, []byte(`"version_conflict"`)) {
		t.Fatal(string(got))
	}
}
