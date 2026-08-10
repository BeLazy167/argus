package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

// The drift guard. repoSettings is the source of truth for what a setting is.
// This struct is the write whitelist, and a key missing from it is SILENTLY
// DROPPED on save — which is how auto_run and the memory thresholds shipped as
// no-ops after being added to repoSettings but not here.
//
// The delete handler now derives its list from repoSettings directly, so this
// is the last copy that can fall behind. It no longer can without failing.
func TestOrgDefaultsBodyMatchesSettingKeys(t *testing.T) {
	want := pipeline.SettingKeys()

	got := make(map[string]bool)
	tp := reflect.TypeOf(orgDefaultsBody{})
	for i := range tp.NumField() {
		tag := tp.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.Index(tag, ","); comma >= 0 {
			tag = tag[:comma]
		}
		got[tag] = true
	}

	for k := range want {
		if !got[k] {
			t.Errorf("repoSettings defines %q but orgDefaultsBody does not — saving it would silently drop the value", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("orgDefaultsBody accepts %q but repoSettings does not define it — the value would be written and never read", k)
		}
	}
}
