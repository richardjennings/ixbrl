// Package ixbrl builds inline-XBRL (iXBRL) documents: XHTML that reads as a normal
// report in a browser while carrying machine-readable XBRL facts tagged inline. It
// is the format UK companies file at Companies House and attach to a CT600.
//
// The package is deliberately taxonomy-agnostic. It knows how to construct contexts,
// units and the two kinds of inline fact (numeric and non-numeric), and how to lay a
// document out from a small block model (headings, paragraphs, tables). The caller
// supplies the taxonomy: the report namespaces, the schema reference, and the
// concept names (e.g. "uk-core:FixedAssets"). That keeps this module reusable for
// FRS 105, FRS 102, or any other taxonomy.
//
// Everything is escaped and the finished document is parsed back as XML by Render,
// so a returned document is always well-formed — there is no unchecked string
// assembly.
package ixbrl

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"encoding/xml"
)

// defaultNamespaces are the fixed inline-XBRL / XBRL namespaces every document needs.
// Report-taxonomy namespaces (e.g. uk-core, uk-bus) are added by the caller.
var defaultNamespaces = map[string]string{
	"":        "http://www.w3.org/1999/xhtml",
	"ix":      "http://www.xbrl.org/2013/inlineXBRL",
	"xbrli":   "http://www.xbrl.org/2003/instance",
	"link":    "http://www.xbrl.org/2003/linkbase",
	"xlink":   "http://www.w3.org/1999/xlink",
	"iso4217": "http://www.xbrl.org/2003/iso4217",
}

// Amount is a signed numeric magnitude for a numeric fact. Magnitude is the
// non-negative decimal string as it should appear (e.g. "1234.56"); Negative records
// the sign separately, because inline XBRL states the magnitude as element content
// and the sign as an attribute.
type Amount struct {
	Magnitude string
	Negative  bool
}

// Context identifies the entity and period a fact applies to. Set Instant for a
// point in time (balance-sheet facts) or Start and End for a duration (P&L facts).
type Context struct {
	ID           string
	EntityScheme string
	EntityID     string
	Instant      string // YYYY-MM-DD; set this, or Start+End
	Start, End   string
}

func (c Context) validate() error {
	if c.ID == "" {
		return fmt.Errorf("ixbrl: context needs an ID")
	}
	if c.Instant == "" && (c.Start == "" || c.End == "") {
		return fmt.Errorf("ixbrl: context %q needs an Instant or a Start and End", c.ID)
	}
	return nil
}

// Unit is a measure a numeric fact is expressed in, e.g. {ID:"GBP", Measure:"iso4217:GBP"}.
type Unit struct {
	ID      string
	Measure string
}

// Document is an inline-XBRL report being assembled. Namespaces is merged over the
// fixed defaults, so callers add only their taxonomy prefixes.
type Document struct {
	Title      string
	Lang       string // defaults to "en"
	SchemaRef  string // xlink:href of the taxonomy entry point
	Style      string // optional CSS for the <style> element
	Namespaces map[string]string
	Contexts   []Context
	Units      []Unit
	Body       []Block
}

// Block is a top-level document element.
type Block interface{ writeBlock(*writer) }

// Inline is content within a block; plain text or a tagged fact.
type Inline interface{ writeInline(*writer) }

// --- inline constructors ---

// Text is escaped literal text.
func Text(s string) Inline { return textNode{s} }

// Numeric is an ix:nonFraction fact: a tagged number in a unit and context, reported
// to the given number of decimal places.
func Numeric(concept, contextRef, unitRef string, a Amount, decimals int) Inline {
	return numFact{concept, contextRef, unitRef, a, decimals}
}

// NonNumeric is an ix:nonNumeric fact: tagged text such as the entity name.
func NonNumeric(concept, contextRef, value string) Inline {
	return strFact{concept, contextRef, value}
}

// Seq groups several inlines so they render in order where a single Inline is
// expected — e.g. a currency symbol next to a tagged number. Composition lives here
// because Inline is satisfied only within this package.
func Seq(parts ...Inline) Inline { return seqNode(parts) }

type seqNode []Inline

func (s seqNode) writeInline(w *writer) {
	for _, p := range s {
		p.writeInline(w)
	}
}

type textNode struct{ s string }
type numFact struct {
	concept, contextRef, unitRef string
	a                            Amount
	decimals                     int
}
type strFact struct{ concept, contextRef, value string }

func (t textNode) writeInline(w *writer) { w.esc(t.s) }
func (f numFact) writeInline(w *writer) {
	w.raw(`<ix:nonFraction`)
	w.attr("name", f.concept)
	w.attr("contextRef", f.contextRef)
	w.attr("unitRef", f.unitRef)
	w.attr("decimals", strconv.Itoa(f.decimals))
	if f.a.Negative {
		w.attr("sign", "-")
	}
	w.raw(`>`)
	mag := f.a.Magnitude
	if mag == "" {
		mag = "0"
	}
	w.esc(mag)
	w.raw(`</ix:nonFraction>`)
}
func (f strFact) writeInline(w *writer) {
	w.raw(`<ix:nonNumeric`)
	w.attr("name", f.concept)
	w.attr("contextRef", f.contextRef)
	w.raw(`>`)
	w.esc(f.value)
	w.raw(`</ix:nonNumeric>`)
}

// --- block constructors ---

// Heading is an <h{level}> line.
func Heading(level int, content ...Inline) Block { return heading{level, content} }

// Paragraph is a <p>, optionally with a CSS class.
func Paragraph(class string, content ...Inline) Block { return para{class, content} }

// Rows assembles a table from rows with no heading row.
func Rows(rows ...Row) Block { return table{rows: rows} }

// Table assembles a table with a heading over each figure column — for example
// the two period-end dates above a current column and a comparative column.
func Table(heads []string, rows ...Row) Block { return table{heads: heads, rows: rows} }

// Row is one table row. Amount is the figure for the period reported on; Prior,
// when set, is the comparative figure for the period before and goes in a second
// column. Emphasis selects a CSS class ("", "subtotal", "total") the stylesheet
// can rule on.
type Row struct {
	Label    string
	Amount   Inline
	Prior    Inline
	Emphasis string
}

type heading struct {
	level   int
	content []Inline
}
type para struct {
	class   string
	content []Inline
}
type table struct {
	heads []string
	rows  []Row
}

// twoColumns reports whether the table carries a comparative column: any row has
// a prior figure, or the headings name two columns.
func (t table) twoColumns() bool {
	if len(t.heads) > 1 {
		return true
	}
	for _, r := range t.rows {
		if r.Prior != nil {
			return true
		}
	}
	return false
}

func (h heading) writeBlock(w *writer) {
	tag := "h" + strconv.Itoa(clamp(h.level, 1, 6))
	w.raw("<" + tag + ">")
	for _, in := range h.content {
		in.writeInline(w)
	}
	w.raw("</" + tag + ">\n")
}
func (p para) writeBlock(w *writer) {
	w.raw("<p")
	if p.class != "" {
		w.attr("class", p.class)
	}
	w.raw(">")
	for _, in := range p.content {
		in.writeInline(w)
	}
	w.raw("</p>\n")
}
func (t table) writeBlock(w *writer) {
	two := t.twoColumns()
	w.raw("<table>\n")
	if len(t.heads) > 0 {
		w.raw(`<tr class="head"><th></th>`)
		for _, h := range t.heads {
			w.raw(`<th class="num">`)
			w.esc(h)
			w.raw("</th>")
		}
		w.raw("</tr>\n")
	}
	for _, r := range t.rows {
		w.raw("<tr")
		if r.Emphasis != "" {
			w.attr("class", r.Emphasis)
		}
		w.raw("><td>")
		w.esc(r.Label)
		w.raw(`</td><td class="num">`)
		if r.Amount != nil {
			r.Amount.writeInline(w)
		}
		w.raw("</td>")
		if two {
			w.raw(`<td class="num">`)
			if r.Prior != nil {
				r.Prior.writeInline(w)
			}
			w.raw("</td>")
		}
		w.raw("</tr>\n")
	}
	w.raw("</table>\n")
}

// Render assembles the document and returns it as a well-formed inline-XBRL XHTML
// string. It parses the result as XML before returning; a non-nil error means the
// inputs produced malformed markup (it never should, given escaping).
func (d Document) Render() (string, error) {
	for _, c := range d.Contexts {
		if err := c.validate(); err != nil {
			return "", err
		}
	}
	w := &writer{b: &strings.Builder{}}
	d.write(w)
	out := w.b.String()
	if err := wellFormed(out); err != nil {
		return "", fmt.Errorf("ixbrl: produced malformed XML: %w", err)
	}
	return out, nil
}

func (d Document) write(w *writer) {
	lang := d.Lang
	if lang == "" {
		lang = "en"
	}
	ns := map[string]string{}
	for k, v := range defaultNamespaces {
		ns[k] = v
	}
	for k, v := range d.Namespaces {
		ns[k] = v
	}

	w.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<html")
	for _, prefix := range sortedKeys(ns) {
		name := "xmlns"
		if prefix != "" {
			name = "xmlns:" + prefix
		}
		w.attr(name, ns[prefix])
	}
	w.attr("xml:lang", lang)
	w.raw(">\n<head><meta charset=\"utf-8\"></meta><title>")
	w.esc(d.Title)
	w.raw("</title>")
	if d.Style != "" {
		w.raw("<style>")
		w.esc(d.Style)
		w.raw("</style>")
	}
	w.raw("</head>\n<body>\n")

	// Hidden XBRL header: schema reference, contexts and units.
	w.raw(`<div style="display:none"><ix:header><ix:references>`)
	if d.SchemaRef != "" {
		w.raw(`<link:schemaRef`)
		w.attr("xlink:type", "simple")
		w.attr("xlink:href", d.SchemaRef)
		w.raw(`></link:schemaRef>`)
	}
	w.raw(`</ix:references><ix:resources>`)
	for _, c := range d.Contexts {
		w.writeContext(c)
	}
	for _, u := range d.Units {
		w.raw(`<xbrli:unit`)
		w.attr("id", u.ID)
		w.raw(`><xbrli:measure>`)
		w.esc(u.Measure)
		w.raw(`</xbrli:measure></xbrli:unit>`)
	}
	w.raw(`</ix:resources></ix:header></div>` + "\n")

	for _, blk := range d.Body {
		blk.writeBlock(w)
	}
	w.raw("</body></html>\n")
}

func (w *writer) writeContext(c Context) {
	w.raw(`<xbrli:context`)
	w.attr("id", c.ID)
	w.raw(`><xbrli:entity><xbrli:identifier`)
	w.attr("scheme", c.EntityScheme)
	w.raw(`>`)
	w.esc(c.EntityID)
	w.raw(`</xbrli:identifier></xbrli:entity><xbrli:period>`)
	if c.Instant != "" {
		w.raw(`<xbrli:instant>`)
		w.esc(c.Instant)
		w.raw(`</xbrli:instant>`)
	} else {
		w.raw(`<xbrli:startDate>`)
		w.esc(c.Start)
		w.raw(`</xbrli:startDate><xbrli:endDate>`)
		w.esc(c.End)
		w.raw(`</xbrli:endDate>`)
	}
	w.raw(`</xbrli:period></xbrli:context>`)
}

// --- low-level escaped writer ---

type writer struct{ b *strings.Builder }

func (w *writer) raw(s string) { w.b.WriteString(s) }
func (w *writer) esc(s string) { _ = xml.EscapeText(w.b, []byte(s)) }
func (w *writer) attr(name, value string) {
	w.b.WriteString(" " + name + `="`)
	_ = xml.EscapeText(w.b, []byte(value))
	w.b.WriteString(`"`)
}

func wellFormed(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	dec.Strict = true
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
