package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Review holds the schema for PR review runs.
type Review struct {
	ent.Schema
}

func (Review) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Int("pr_number"),
		field.String("pr_title"),
		field.String("pr_author"),
		field.String("head_sha"),
		field.String("base_sha"),
		field.Int64("github_review_id").Optional().Nillable(),
		field.Enum("status").Values("pending", "in_progress", "completed", "failed").Default("pending"),
		field.Text("summary").Optional().Nillable(),
		field.Int("score").Optional().Nillable(),
		field.JSON("token_usage", map[string]any{}).Optional(),
		field.Enum("trigger").Values("webhook", "manual").Default("webhook"),
		field.String("triggered_by").Optional().Nillable(),
		field.Int("duration_ms").Optional().Nillable(),
		field.Text("error").Optional().Nillable(),
		field.Bool("deep_review").Default(false),
		field.String("persona").Optional().Nillable(),
		field.Bool("is_incremental").Default(false),
		field.Time("created_at").Default(time.Now),
		field.Time("completed_at").Optional().Nillable(),
	}
}

func (Review) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("repo", Repo.Type).Ref("reviews").Unique().Required(),
		edge.To("comments", ReviewComment.Type),
		edge.To("pipeline_states", PipelineState.Type),
	}
}
