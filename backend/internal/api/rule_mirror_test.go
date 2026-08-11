package api

import (
	"context"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
)

func TestSyncRuleMirrorDisableAndReenable(t *testing.T) {
	idx := &memorytest.Fake{}
	rule := memory.RuleMemory{RuleID: 42, Category: "security", Priority: 10, Content: "validate inputs"}

	if err := syncRuleMirror(context.Background(), idx, rule, true); err != nil {
		t.Fatal(err)
	}
	if len(idx.Rules) != 1 || len(idx.Deleted) != 0 {
		t.Fatalf("enabled mirror = rules %d deleted %v", len(idx.Rules), idx.Deleted)
	}

	if err := syncRuleMirror(context.Background(), idx, rule, false); err != nil {
		t.Fatal(err)
	}
	if len(idx.Rules) != 1 || len(idx.Deleted) != 1 || idx.Deleted[0] != memory.RuleCustomID(42) {
		t.Fatalf("disabled mirror = rules %d deleted %v", len(idx.Rules), idx.Deleted)
	}

	rule.Content = "validate all external inputs"
	if err := syncRuleMirror(context.Background(), idx, rule, true); err != nil {
		t.Fatal(err)
	}
	if len(idx.Rules) != 2 || idx.Rules[1].Content != rule.Content {
		t.Fatalf("re-enabled mirror did not restore current rule: %#v", idx.Rules)
	}
}
