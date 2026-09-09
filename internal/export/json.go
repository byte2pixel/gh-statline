package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// JSON renders a Doc as one object: the view, where its numbers came from,
// and an array per sheet.
//
// Keys are the columns' machine keys in declared column order, which a Go
// map would have thrown away. Durations are whole seconds, timestamps are
// RFC 3339 UTC, and a "no data" sentinel is null rather than the 0 or -1 a
// dashboard would average in as a measurement.
func JSON(d Doc) ([]byte, error) {
	doc := &jsonObj{}
	doc.field("view", d.View)
	doc.field("title", heading(d.Title))
	if d.Lead != "" {
		doc.field("summary", heading(d.Lead))
	}

	meta := &jsonObj{}
	if d.Meta.Team != "" {
		meta.field("team", heading(d.Meta.Team))
	}
	if w := d.Meta.Window; w != nil {
		win := &jsonObj{}
		win.field("label", heading(w.Label))
		win.field("start", rfc3339(w.Start))
		win.field("end", rfc3339(w.End))
		meta.raw("window", win.bytes())
		meta.err(win.errored())
	}
	if !d.Meta.GeneratedAt.IsZero() {
		meta.field("generated_at", d.Meta.GeneratedAt.UTC().Format(time.RFC3339))
	}
	doc.raw("meta", meta.bytes())
	doc.err(meta.errored())

	for _, s := range d.sheets(true) {
		cols := s.columns(true)
		var arr bytes.Buffer
		arr.WriteByte('[')
		for i, r := range s.Rows {
			if i > 0 {
				arr.WriteByte(',')
			}
			row := &jsonObj{}
			for j, v := range s.cells(r, true) {
				enc, err := jsonCell(v)
				if err != nil {
					return nil, err
				}
				row.raw(cols[j].Key, enc)
			}
			if err := row.errored(); err != nil {
				return nil, err
			}
			arr.Write(row.bytes())
		}
		arr.WriteByte(']')
		doc.raw(s.Key, arr.Bytes())
	}
	if err := doc.errored(); err != nil {
		return nil, err
	}

	// Indented: as often read by a person as piped into jq.
	var out bytes.Buffer
	if err := json.Indent(&out, doc.bytes(), "", "  "); err != nil {
		return nil, fmt.Errorf("formatting json: %w", err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// jsonObj builds an object with its keys in insertion order. A map sorts
// them and a struct would have to be declared per view.
type jsonObj struct {
	buf   bytes.Buffer
	n     int
	fault error
}

func (o *jsonObj) field(key string, v any) {
	enc, err := json.Marshal(v)
	if err != nil {
		o.err(fmt.Errorf("encoding %s: %w", key, err))
		return
	}
	o.raw(key, enc)
}

func (o *jsonObj) raw(key string, enc []byte) {
	if o.n > 0 {
		o.buf.WriteByte(',')
	}
	k, err := json.Marshal(key)
	if err != nil {
		o.err(fmt.Errorf("encoding key %s: %w", key, err))
		return
	}
	o.buf.Write(k)
	o.buf.WriteByte(':')
	o.buf.Write(enc)
	o.n++
}

func (o *jsonObj) err(e error) {
	if o.fault == nil {
		o.fault = e
	}
}

func (o *jsonObj) errored() error { return o.fault }

func (o *jsonObj) bytes() []byte {
	out := make([]byte, 0, o.buf.Len()+2)
	out = append(out, '{')
	out = append(out, o.buf.Bytes()...)
	return append(out, '}')
}

func jsonCell(c Cell) ([]byte, error) {
	switch c.kind {
	case kindNone:
		return []byte("null"), nil
	case kindText:
		enc, err := json.Marshal(c.plain())
		if err != nil {
			return nil, fmt.Errorf("encoding cell: %w", err)
		}
		return enc, nil
	default:
		// Numbers, seconds and booleans are already JSON literals.
		return []byte(c.plain()), nil
	}
}

func rfc3339(ts int64) string { return time.Unix(ts, 0).UTC().Format(time.RFC3339) }
