package store

import (
	"fmt"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

func patternFromSQLC(id int, installationID int64, repoID *int64, content string, memoryDocID, createdBy *string, source string, category *string, prNumber *int, createdAt, updatedAt *time.Time) (Pattern, error) {
	if createdAt == nil || updatedAt == nil {
		return Pattern{}, fmt.Errorf("pattern %d has NULL timestamps", id)
	}
	return Pattern{
		ID: int64(id), InstallationID: installationID, RepoID: repoID, Content: content,
		MemoryDocID: memoryDocID, CreatedBy: createdBy, Source: source, Category: category,
		PRNumber: prNumber, CreatedAt: *createdAt, UpdatedAt: *updatedAt,
	}, nil
}

func installationFromSQLC(id, installationID int64, orgLogin string, clerkOrgID *string, createdAt time.Time, suspendedAt *time.Time) Installation {
	return Installation{ID: id, InstallationID: installationID, OrgLogin: orgLogin, ClerkOrgID: clerkOrgID, CreatedAt: createdAt, SuspendedAt: suspendedAt}
}

func repoFromSQLC(id, installationID, githubID int64, fullName, defaultBranch string, enabled bool, settingsJSON []byte, createdAt, updatedAt time.Time) Repo {
	return Repo{ID: id, InstallationID: installationID, GithubID: githubID, FullName: fullName, DefaultBranch: defaultBranch, Enabled: enabled, SettingsJSON: settingsJSON, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func reviewCommentFromSQLC(row db.GetReviewCommentsRow) (ReviewComment, error) {
	return reviewCommentFromValues(row.ID, row.ReviewID, row.FilePath, row.StartLine, row.EndLine, row.Side, row.Body, row.Severity, row.Category, row.Specialist, row.ConfidenceScore, row.CodeSnippet, row.GithubCommentID, row.MatchedPatternID, row.MatchedPatternScore, row.EnforcedRuleContent, row.IsNewFinding, row.CreatedAt, row.State, row.SuppressedReason, row.ResolvedSHA, row.AttemptGeneration)
}

func completedReviewCommentFromSQLC(row db.GetPRCompletedReviewCommentsRow) (ReviewComment, error) {
	return reviewCommentFromValues(row.ID, row.ReviewID, row.FilePath, row.StartLine, row.EndLine, row.Side, row.Body, row.Severity, row.Category, row.Specialist, row.ConfidenceScore, row.CodeSnippet, row.GithubCommentID, row.MatchedPatternID, row.MatchedPatternScore, row.EnforcedRuleContent, row.IsNewFinding, row.CreatedAt, row.State, row.SuppressedReason, row.ResolvedSHA, row.AttemptGeneration)
}

func reviewCommentFromValues(id, reviewID uuid.UUID, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist *string, confidenceScore *int, codeSnippet *string, githubCommentID, matchedPatternID *int64, matchedPatternScore *float32, enforcedRuleContent *string, isNewFinding *bool, createdAt time.Time, state string, suppressedReason, resolvedSHA *string, attemptGeneration int) (ReviewComment, error) {
	if isNewFinding == nil {
		return ReviewComment{}, fmt.Errorf("review comment %s has NULL is_new_finding", id)
	}
	return ReviewComment{
		ID: id, ReviewID: reviewID, FilePath: filePath, StartLine: startLine, EndLine: endLine,
		Side: side, Body: body, Severity: severity, Category: category, Specialist: specialist,
		ConfidenceScore: confidenceScore, CodeSnippet: codeSnippet, GithubCommentID: githubCommentID,
		MatchedPatternID: matchedPatternID, MatchedPatternScore: matchedPatternScore,
		EnforcedRuleContent: enforcedRuleContent, IsNewFinding: *isNewFinding, CreatedAt: createdAt,
		State: state, SuppressedReason: suppressedReason, ResolvedSHA: resolvedSHA,
		AttemptGeneration: attemptGeneration,
	}, nil
}

func modelConfigFromValues(id int64, repoID, installationID *int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32, createdAt, updatedAt time.Time) ModelConfig {
	return ModelConfig{ID: id, RepoID: repoID, InstallationID: installationID, Stage: stage, Provider: provider, Model: model, BaseURL: baseURL, MaxTokens: maxTokens, Temperature: temperature, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func promptTemplateFromSQLC(row db.PromptTemplate) (PromptTemplate, error) {
	if row.CreatedAt == nil || row.UpdatedAt == nil {
		return PromptTemplate{}, fmt.Errorf("prompt template %d has NULL timestamps", row.ID)
	}
	return PromptTemplate{ID: int64(row.ID), RepoID: row.RepoID, Stage: row.Stage, PromptText: row.PromptText, CreatedAt: *row.CreatedAt, UpdatedAt: *row.UpdatedAt}, nil
}
