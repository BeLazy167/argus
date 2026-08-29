package github

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

func TestCommentReactionListErrorTypesOnlyNotFoundAsDeleted(t *testing.T) {
	apiError := func(status int) error {
		return fmt.Errorf("github response: %w", &gh.ErrorResponse{
			Response: &http.Response{StatusCode: status},
		})
	}

	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "deleted review comment", status: http.StatusNotFound, want: true},
		{name: "authentication failure", status: http.StatusUnauthorized},
		{name: "permission or rate limit", status: http.StatusForbidden},
		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "transient server failure", status: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := apiError(tt.status)
			err := commentReactionListError(501, nil, original)
			if got := errors.Is(err, ErrReviewCommentNotFound); got != tt.want {
				t.Fatalf("errors.Is(ErrReviewCommentNotFound) = %v, want %v (err=%v)", got, tt.want, err)
			}
			var responseErr *gh.ErrorResponse
			if !errors.As(err, &responseErr) {
				t.Fatalf("typed error lost original GitHub response: %v", err)
			}
		})
	}
}

func TestCommentReactionListErrorUsesResponseStatusForDeletedComment(t *testing.T) {
	original := errors.New("not found")
	response := &gh.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
	err := commentReactionListError(501, response, original)
	if !errors.Is(err, ErrReviewCommentNotFound) {
		t.Fatalf("error = %v, want ErrReviewCommentNotFound", err)
	}
	if !errors.Is(err, original) {
		t.Fatalf("typed error lost original cause: %v", err)
	}
}
