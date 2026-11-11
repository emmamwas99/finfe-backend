// Package pagination parses page/pageSize and builds list metadata.
package pagination

import (
	"net/url"
	"strconv"
)

const (
	DefaultSize = 50
	MaxSize     = 200
)

type Page struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"pageSize"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"totalPages"`
}

// FromQuery reads ?page=&pageSize= with sane bounds.
func FromQuery(q url.Values) Page {
	p, _ := strconv.Atoi(q.Get("page"))
	s, _ := strconv.Atoi(q.Get("pageSize"))
	if p < 1 {
		p = 1
	}
	if s < 1 {
		s = DefaultSize
	}
	if s > MaxSize {
		s = MaxSize
	}
	return Page{Page: p, PageSize: s}
}

func (p Page) Limit() int32  { return int32(p.PageSize) }
func (p Page) Offset() int32 { return int32((p.Page - 1) * p.PageSize) }

func (p Page) Meta(total int64) Meta {
	pages := (total + int64(p.PageSize) - 1) / int64(p.PageSize)
	return Meta{Page: p.Page, PageSize: p.PageSize, Total: total, TotalPages: pages}
}
