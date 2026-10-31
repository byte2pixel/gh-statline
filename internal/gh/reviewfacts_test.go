package gh

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// cannedDoer answers every request with one JSON document and records the
// variables it was asked with.
type cannedDoer struct {
	payload string
	vars    map[string]interface{}
}

func (d *cannedDoer) DoWithContext(_ context.Context, _ string, vars map[string]interface{}, resp interface{}) error {
	d.vars = vars
	return json.Unmarshal([]byte(d.payload), resp)
}

// The lookup returns one entry per id asked for, in that order: a reply
// (no body, first comment replies), a review with a body, a single
// comment that opens a thread, an id the API answers null for, and an id
// that resolves to some other kind of node.
func TestFetchReviewFactsOnePerIDInOrder(t *testing.T) {
	doer := &cannedDoer{payload: `{
		"rateLimit": {"cost": 2, "remaining": 4990, "resetAt": "2026-03-10T13:00:00Z"},
		"nodes": [
			{"id": "RV_reply", "body": "", "comments": {"nodes": [{"replyTo": {"id": "PRRC_1"}}]}},
			{"id": "RV_body", "body": "LGTM", "comments": {"nodes": [{"replyTo": {"id": "PRRC_2"}}]}},
			{"id": "RV_opens", "body": "", "comments": {"nodes": [{"replyTo": null}]}},
			null,
			{}
		]}`}
	ids := []string{"RV_reply", "RV_body", "RV_opens", "RV_gone", "PR_notareview"}
	got, rl, err := FetchReviewFacts(context.Background(), doer, ids)
	if err != nil {
		t.Fatal(err)
	}
	want := []ReviewFacts{
		{ID: "RV_reply", Known: true, HasBody: false, FirstReply: true},
		{ID: "RV_body", Known: true, HasBody: true, FirstReply: true},
		{ID: "RV_opens", Known: true, HasBody: false, FirstReply: false},
		{ID: "RV_gone"},
		{ID: "PR_notareview"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts =\n %+v\nwant\n %+v", got, want)
	}
	if rl.Cost != 2 || rl.Remaining != 4990 {
		t.Errorf("rate limit = %+v, want the block from the response", rl)
	}
	if asked, _ := doer.vars["ids"].([]string); !reflect.DeepEqual(asked, ids) {
		t.Errorf("asked for %v, want %v", doer.vars["ids"], ids)
	}
}

// A review with no comments at all cannot be a reply; the comment list is
// simply empty rather than absent.
func TestFetchReviewFactsNoComments(t *testing.T) {
	doer := &cannedDoer{payload: `{
		"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": "2026-03-10T13:00:00Z"},
		"nodes": [{"id": "RV_bare", "body": "", "comments": {"nodes": []}}]}`}
	got, _, err := FetchReviewFacts(context.Background(), doer, []string{"RV_bare"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ReviewFacts{{ID: "RV_bare", Known: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("facts = %+v, want %+v", got, want)
	}
}
