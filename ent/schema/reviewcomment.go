package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// ReviewComment holds the schema for per-file review comments.
type ReviewComment struct {
	ent.Schema
}

func (ReviewComment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("file_path"),
		field.Int("start_line").Optional().Nillable(),
		field.Int("end_line").Optional().Nillable(),
		field.Enum("side").Values("LEFT", "RIGHT").Optional().Nillable(),
		field.Text("body"),
		field.Enum("severity").Values("critical", "warning", "suggestion", "praise").Optional().Nillable(),
		field.String("category").Optional().Nillable(),
		field.String("specialist").Optional().Nillable(),
		field.Int("confidence_score").Optional().Nillable(),
		field.Time("created_at").Default(time.Now),
	}
}

func (ReviewComment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("review", Review.Type).Ref("comments").Unique().Required(),
	}
}
