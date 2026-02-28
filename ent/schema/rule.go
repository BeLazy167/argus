package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Rule holds org-wide review rules.
type Rule struct {
	ent.Schema
}

func (Rule) Fields() []ent.Field {
	return []ent.Field{
		field.String("category"),
		field.Text("content"),
		field.Int("priority").Default(0),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
