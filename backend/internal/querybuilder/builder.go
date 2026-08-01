// Package querybuilder is a lightweight CTE-aware SQL builder adapted from
// a production query builder. It never string-concatenates user values
// into identifiers — callers bind parameters separately.
package querybuilder

import (
	"fmt"
	"strings"
)

type clause struct {
	key  string
	expr string
}

type cteDefinition struct {
	name  string
	query *Builder
	raw   string // optional raw SQL body instead of nested Builder
}

type Builder struct {
	selectClauses   []clause
	preWhereClauses []clause
	whereClauses    []clause
	groupByClauses  []clause
	orderByClauses  []clause
	havingClauses   []clause
	joins           []clause
	from            string
	limit           *int
	settings        []string
	ctes            []*cteDefinition
	filterOperator  string
}

func New(fromTable string) *Builder {
	return &Builder{
		from:           fromTable,
		filterOperator: "AND",
		ctes:           make([]*cteDefinition, 0),
	}
}

func (b *Builder) Select(key, expr string) *Builder {
	if expr != "" {
		b.selectClauses = setOrAppend(b.selectClauses, key, expr)
	}
	return b
}

func (b *Builder) PreWhere(key, condition string) *Builder {
	if condition != "" {
		b.preWhereClauses = setOrAppend(b.preWhereClauses, key, condition)
	}
	return b
}

func (b *Builder) Where(key, condition string) *Builder {
	if condition != "" {
		b.whereClauses = setOrAppend(b.whereClauses, key, condition)
	}
	return b
}

func (b *Builder) GroupBy(key, expr string) *Builder {
	if expr != "" {
		b.groupByClauses = setOrAppend(b.groupByClauses, key, expr)
	}
	return b
}

func (b *Builder) OrderBy(key, expr string) *Builder {
	if expr != "" {
		b.orderByClauses = setOrAppend(b.orderByClauses, key, expr)
	}
	return b
}

func (b *Builder) Having(key, condition string) *Builder {
	if condition != "" {
		b.havingClauses = setOrAppend(b.havingClauses, key, condition)
	}
	return b
}

func (b *Builder) Join(key, joinExpr string) *Builder {
	if joinExpr != "" {
		b.joins = setOrAppend(b.joins, key, joinExpr)
	}
	return b
}

func (b *Builder) From(table string) *Builder {
	b.from = table
	return b
}

func (b *Builder) Limit(n int) *Builder {
	b.limit = &n
	return b
}

func (b *Builder) Setting(s string) *Builder {
	if s != "" {
		b.settings = append(b.settings, s)
	}
	return b
}

func (b *Builder) With(name string, query *Builder) *Builder {
	b.ctes = append(b.ctes, &cteDefinition{name: name, query: query})
	return b
}

// WithRaw adds a CTE whose body is already-formed SQL (for window functions etc.).
func (b *Builder) WithRaw(name, rawSQL string) *Builder {
	b.ctes = append(b.ctes, &cteDefinition{name: name, raw: rawSQL})
	return b
}

func (b *Builder) Build() string {
	parts := make([]string, 0, 8)
	if cte := b.buildCTEs(); cte != "" {
		parts = append(parts, cte)
	}
	parts = append(parts, b.getSelect())
	if b.from != "" {
		parts = append(parts, "FROM "+b.from)
	}
	for _, j := range b.joins {
		if j.expr != "" {
			parts = append(parts, j.expr)
		}
	}
	if pw := exprs(b.preWhereClauses); len(pw) > 0 {
		parts = append(parts, "PREWHERE "+strings.Join(pw, " AND "))
	}
	if w := exprs(b.whereClauses); len(w) > 0 {
		parts = append(parts, "WHERE "+strings.Join(w, " AND "))
	}
	if g := exprs(b.groupByClauses); len(g) > 0 {
		parts = append(parts, "GROUP BY "+strings.Join(g, ", "))
	}
	if h := exprs(b.havingClauses); len(h) > 0 {
		parts = append(parts, "HAVING "+strings.Join(h, " AND "))
	}
	if o := exprs(b.orderByClauses); len(o) > 0 {
		parts = append(parts, "ORDER BY "+strings.Join(o, ", "))
	}
	if b.limit != nil {
		parts = append(parts, fmt.Sprintf("LIMIT %d", *b.limit))
	}
	if len(b.settings) > 0 {
		parts = append(parts, "SETTINGS "+strings.Join(b.settings, ", "))
	}
	return strings.Join(parts, "\n")
}

func (b *Builder) buildCTEs() string {
	if len(b.ctes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.ctes))
	for _, c := range b.ctes {
		body := c.raw
		if body == "" && c.query != nil {
			body = c.query.Build()
		}
		parts = append(parts, fmt.Sprintf("%s AS (\n%s\n)", c.name, body))
	}
	return "WITH " + strings.Join(parts, ",\n")
}

func (b *Builder) getSelect() string {
	cols := exprs(b.selectClauses)
	if len(cols) == 0 {
		return "SELECT *"
	}
	return "SELECT " + strings.Join(cols, ", ")
}

func setOrAppend(list []clause, key, expr string) []clause {
	for i := range list {
		if list[i].key == key {
			list[i].expr = expr
			return list
		}
	}
	return append(list, clause{key: key, expr: expr})
}

func exprs(list []clause) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		if c.expr != "" {
			out = append(out, c.expr)
		}
	}
	return out
}

// QuoteString escapes a string literal for ClickHouse (use only for trusted constants).
func QuoteString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
}
