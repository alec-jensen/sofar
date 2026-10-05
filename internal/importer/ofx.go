package importer

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
)

// ParseOFX reads OFX and QFX statements, both the older SGML form (tags without
// closing tags) and OFX 2 XML. Only STMTTRN records are read.
func ParseOFX(r io.Reader) (Parsed, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Parsed{}, err
	}
	if len(data) > MaxBytes {
		return Parsed{}, ErrTooLarge
	}
	text := decodeText(data)
	upper := asciiUpper(text)
	if !strings.Contains(upper, "<STMTTRN>") && !strings.Contains(upper, "OFXHEADER") && !strings.Contains(upper, "<OFX>") {
		return Parsed{}, fmt.Errorf("%w: no OFX statement found", ErrFormat)
	}
	var out Parsed
	pos := 0
	for n := 1; ; n++ {
		i := strings.Index(upper[pos:], "<STMTTRN>")
		if i < 0 {
			break
		}
		start := pos + i + len("<STMTTRN>")
		end := len(upper)
		for _, stop := range []string{"</STMTTRN>", "<STMTTRN>", "</BANKTRANLIST>", "</CCSTMTRS>", "</STMTRS>"} {
			if j := strings.Index(upper[start:], stop); j >= 0 && start+j < end {
				end = start + j
			}
		}
		pos = end
		if len(out.Rows) >= MaxRows {
			out.Skipped = append(out.Skipped, Skipped{n, "row limit reached"})
			break
		}
		row, err := ofxRecord(text[start:end])
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{n, err.Error()})
			continue
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// ofxField returns the value of a tag inside one record: the text after <TAG>
// up to the next "<" (SGML has no closing tag, XML does).
func ofxField(rec, tag string) string {
	upper := asciiUpper(rec)
	i := strings.Index(upper, "<"+tag+">")
	if i < 0 {
		return ""
	}
	rest := rec[i+len(tag)+2:]
	if j := strings.IndexAny(rest, "<\r\n"); j >= 0 {
		rest = rest[:j]
	}
	return clip(unescapeEntities(rest))
}

func unescapeEntities(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'").Replace(s)
}

// ofxDate reads YYYYMMDD with optional time and zone: 20261001120000.000[-5:CDT].
// OFX dates are used as calendar dates (S3): the time part is ignored.
func ofxDate(s string) (civil.Date, error) {
	if len(s) < 8 {
		return civil.Date{}, fmt.Errorf("unreadable date %q", s)
	}
	t, err := time.Parse("20060102", s[:8])
	if err != nil {
		return civil.Date{}, fmt.Errorf("unreadable date %q", s)
	}
	return civil.DateOf(t), nil
}

func ofxRecord(rec string) (Row, error) {
	date, err := ofxDate(ofxField(rec, "DTPOSTED"))
	if err != nil {
		return Row{}, err
	}
	amount, err := parseAmount(ofxField(rec, "TRNAMT"))
	if err != nil {
		return Row{}, err
	}
	name, memo := ofxField(rec, "NAME"), ofxField(rec, "MEMO")
	desc := name
	if desc == "" {
		desc = memo
	}
	payee := ofxField(rec, "PAYEE")
	if payee == "" {
		payee = name
	}
	return Row{ProviderID: ofxField(rec, "FITID"), Date: date, Amount: amount, Description: desc, Payee: payee, Memo: memo}, nil
}

// asciiUpper upper-cases ASCII letters only, so byte offsets in the result are
// valid in the original (strings.ToUpper can change the length of non-ASCII text).
func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
