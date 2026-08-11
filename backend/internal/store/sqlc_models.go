package store

import (
	"encoding/json"
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

func scopedReviewFromSQLC(row db.ListReviewsScopedRow) Review {
	return reviewListFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.Trigger, row.TriggeredBy, row.BudgetNote, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt, row.CrossPRHash, row.TraceID)
}

func allScopedReviewFromSQLC(row db.ListAllReviewsScopedRow) Review {
	return reviewListFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.Trigger, row.TriggeredBy, row.BudgetNote, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt, row.CrossPRHash, row.TraceID)
}

func reviewListFromValues(id uuid.UUID, repoID int64, prNumber int, prTitle, prAuthor, headSHA, baseSHA, headRef string, githubReviewID *int64, status string, summary *string, score *int, trigger string, triggeredBy, budgetNote *string, durationMs *int, reviewErr *string, deepReview bool, persona *string, isIncremental bool, createdAt time.Time, completedAt *time.Time, crossPRHash, traceID *string) Review {
	return Review{
		ID: id, RepoID: repoID, PRNumber: prNumber, PRTitle: prTitle, PRAuthor: prAuthor,
		HeadSHA: headSHA, BaseSHA: baseSHA, HeadRef: headRef, GithubReviewID: githubReviewID,
		Status: status, Summary: summary, Score: score, Trigger: trigger, TriggeredBy: triggeredBy,
		BudgetNote: budgetNote, DurationMs: durationMs, Error: reviewErr, DeepReview: deepReview,
		Persona: persona, IsIncremental: isIncremental, CreatedAt: createdAt, CompletedAt: completedAt,
		Diagrams: json.RawMessage("[]"), TruncatedFiles: json.RawMessage("[]"), CrossPRHash: crossPRHash, TraceID: traceID,
	}
}

func fileMemoryCommentFromSQLC(row db.GetFileMemoryCommentsRow) (ReviewComment, error) {
	return reviewCommentFromValues(row.ID, row.ReviewID, row.FilePath, row.StartLine, row.EndLine, row.Side, row.Body, row.Severity, row.Category, row.Specialist, row.ConfidenceScore, row.CodeSnippet, row.GithubCommentID, row.MatchedPatternID, row.MatchedPatternScore, row.EnforcedRuleContent, row.IsNewFinding, row.CreatedAt, row.State, row.SuppressedReason, row.ResolvedSHA, row.AttemptGeneration)
}

func providerKeyFromValues(id, installationID int64, repoID *int64, provider, apiKeyEnc string, baseURL, model, keyHint *string, createdAt, updatedAt time.Time) ProviderKey {
	hint := ""
	if keyHint != nil {
		hint = *keyHint
	}
	return ProviderKey{ID: id, InstallationID: installationID, RepoID: repoID, Provider: provider, APIKeyEnc: apiKeyEnc, KeyHint: hint, BaseURL: baseURL, Model: model, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func rawMessagePtr(value []byte) *json.RawMessage {
	if value == nil {
		return nil
	}
	raw := json.RawMessage(value)
	return &raw
}

func reviewCoreFromValues(id uuid.UUID, repoID int64, prNumber int, prTitle, prAuthor, headSHA, baseSHA, headRef string, githubReviewID *int64, status string, summary *string, score *int, tokenUsage []byte, trigger string, triggeredBy *string, durationMs *int, reviewErr *string, deepReview bool, persona *string, isIncremental bool, createdAt time.Time, completedAt *time.Time) Review {
	return Review{ID: id, RepoID: repoID, PRNumber: prNumber, PRTitle: prTitle, PRAuthor: prAuthor, HeadSHA: headSHA, BaseSHA: baseSHA, HeadRef: headRef, GithubReviewID: githubReviewID, Status: status, Summary: summary, Score: score, TokenUsage: rawMessagePtr(tokenUsage), Trigger: trigger, TriggeredBy: triggeredBy, DurationMs: durationMs, Error: reviewErr, DeepReview: deepReview, Persona: persona, IsIncremental: isIncremental, CreatedAt: createdAt, CompletedAt: completedAt}
}

func fullReviewFromSQLC(row db.GetReviewRow) Review {
	review := reviewCoreFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.TokenUsage, row.Trigger, row.TriggeredBy, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt)
	review.Diagram, review.DiagramTitle = row.Diagram, row.DiagramTitle
	review.Diagrams, review.TruncatedFiles = row.Diagrams, row.TruncatedFiles
	review.Brief, review.CrossPRHash, review.TraceID = row.Brief, row.CrossPRHash, row.TraceID
	review.ReviewContract, review.BudgetNote = rawMessagePtr(row.ReviewContract), row.BudgetNote
	return review
}

func lastCompletedReviewFromSQLC(row db.GetLastCompletedReviewRow) Review {
	review := reviewCoreFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.TokenUsage, row.Trigger, row.TriggeredBy, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt)
	review.Diagram, review.DiagramTitle, review.TraceID = row.Diagram, row.DiagramTitle, row.TraceID
	return review
}

func latestReviewBySHAFromSQLC(row db.GetLatestReviewBySHARow) Review {
	review := reviewCoreFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.TokenUsage, row.Trigger, row.TriggeredBy, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt)
	review.Diagram, review.DiagramTitle, review.TraceID = row.Diagram, row.DiagramTitle, row.TraceID
	return review
}

func latestReviewByPRFromSQLC(row db.GetLatestReviewByPRRow) Review {
	review := reviewCoreFromValues(row.ID, row.RepoID, row.PRNumber, row.PRTitle, row.PRAuthor, row.HeadSHA, row.BaseSHA, row.HeadRef, row.GithubReviewID, row.Status, row.Summary, row.Score, row.TokenUsage, row.Trigger, row.TriggeredBy, row.DurationMs, row.Error, row.DeepReview, row.Persona, row.IsIncremental, row.CreatedAt, row.CompletedAt)
	review.Diagram, review.DiagramTitle, review.TraceID = row.Diagram, row.DiagramTitle, row.TraceID
	return review
}

func githubReviewCommentFromSQLC(row db.GetCommentByGithubIDRow) (ReviewComment, error) {
	return reviewCommentFromValues(row.ID, row.ReviewID, row.FilePath, row.StartLine, row.EndLine, row.Side, row.Body, row.Severity, row.Category, row.Specialist, row.ConfidenceScore, row.CodeSnippet, row.GithubCommentID, row.MatchedPatternID, row.MatchedPatternScore, row.EnforcedRuleContent, row.IsNewFinding, row.CreatedAt, row.State, row.SuppressedReason, row.ResolvedSHA, row.AttemptGeneration)
}
