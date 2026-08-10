package api

import (
	"reflect"
	"strings"
	"testing"
)

// memoryBearingField reports whether a struct field carries memory content —
// either by type (the store's memory shapes) or by name.
func memoryBearingField(f reflect.StructField) bool {
	if strings.Contains(f.Type.String(), "LearnedMemory") {
		return true
	}
	return strings.Contains(strings.ToLower(f.Name), "memor")
}

// TestMemoryNeverRidesOnThePublicExport is the containment check for the one
// new field. Memory content is derived from private source code, and
// ReviewExportResponse is served by exportReviewPublic — an UNAUTHENTICATED
// route whose only gate is an HMAC signature that is minted into every posted
// PR comment and therefore readable by anyone who can see the pull request.
// Attaching memory to it would publish a repository's institutional knowledge
// to everyone with a link.
//
// The authenticated ReviewDetailResponse is the only place it belongs; that
// handler proves ownership through GetRepoScoped before reading anything.
func TestMemoryNeverRidesOnThePublicExport(t *testing.T) {
	publicShapes := []struct {
		name string
		typ  reflect.Type
	}{
		{"ReviewExportResponse", reflect.TypeOf(ReviewExportResponse{})},
		{"ExportFinding", reflect.TypeOf(ExportFinding{})},
	}

	for _, s := range publicShapes {
		t.Run(s.name, func(t *testing.T) {
			for i := 0; i < s.typ.NumField(); i++ {
				if f := s.typ.Field(i); memoryBearingField(f) {
					t.Errorf("%s.%s (%s) would publish memory content on the signed public export route",
						s.name, f.Name, f.Type)
				}
			}
		})
	}

	// The mirror assertion: the authenticated shape MUST carry it, or the
	// dashboard has nothing to render and this whole check guards nothing.
	detail := reflect.TypeOf(ReviewDetailResponse{})
	carries := false
	for i := 0; i < detail.NumField(); i++ {
		if memoryBearingField(detail.Field(i)) {
			carries = true
		}
	}
	if !carries {
		t.Error("ReviewDetailResponse carries no memory field; the review page cannot show what the review learned")
	}
}
