package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// PipelineState holds the schema for pipeline state machine persistence.
type PipelineState struct {
	ent.Schema
}

func (PipelineState) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("state"),
		field.JSON("payload", map[string]any{}).Optional(),
		field.Text("error").Optional().Nillable(),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (PipelineState) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("review", Review.Type).Ref("pipeline_states").Unique().Required(),
	}
}
